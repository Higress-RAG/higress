package rag

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/common/httpx"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/config"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/crag"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/embedding"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/llm"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/orchestrator"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/post"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/retriever"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/schema"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/textsplitter"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/vectordb"
	"github.com/google/uuid"
)

const (
	MAX_LIST_KNOWLEDGE_ROW_COUNT = 1000
	MAX_LIST_DOCUMENT_ROW_COUNT  = 1000
)

// RAGClient represents the RAG (Retrieval-Augmented Generation) client
type RAGClient struct {
	config            *config.Config
	vectordbProvider  vectordb.VectorStoreProvider
	embeddingProvider embedding.Provider
	textSplitter      textsplitter.TextSplitter
	llmProvider       llm.Provider
	orch              *orchestrator.Orchestrator
	sessions          SessionStore
}

// NewRAGClient creates a new RAG client instance
func NewRAGClient(config *config.Config) (*RAGClient, error) {
	ragclient := &RAGClient{
		config: config,
	}
	textSplitter, err := textsplitter.NewTextSplitter(&config.RAG.Splitter)
	if err != nil {
		return nil, fmt.Errorf("create text splitter failed, err: %w", err)
	}
	ragclient.textSplitter = textSplitter

	embeddingProvider, err := embedding.NewEmbeddingProvider(ragclient.config.Embedding)
	if err != nil {
		return nil, fmt.Errorf("create embedding provider failed, err: %w", err)
	}
	ragclient.embeddingProvider = embeddingProvider

	if ragclient.config.LLM.Provider == "" {
		ragclient.llmProvider = nil
	} else {
		llmProvider, err := llm.NewLLMProvider(ragclient.config.LLM)
		if err != nil {
			return nil, fmt.Errorf("create llm provider failed, err: %w", err)
		}
		ragclient.llmProvider = llmProvider
	}

	dim := ragclient.config.Embedding.Dimensions
	provider, err := vectordb.NewVectorDBProvider(&ragclient.config.VectorDB, dim)
	if err != nil {
		return nil, fmt.Errorf("create vector store provider failed, err: %w", err)
	}
	ragclient.vectordbProvider = provider

	// Build enhanced pipeline orchestrator if configured
	if ragclient.config.Pipeline != nil {
		retrievers := make([]retriever.Retriever, 0, len(ragclient.config.Pipeline.Retrievers)+1)
		retrieverMap := make(map[string]retriever.Retriever)
		register := func(r retriever.Retriever, typ, provider, name string) {
			if r == nil {
				return
			}
			key := strings.ToLower(strings.TrimSpace(typ))
			if key != "" {
				retrieverMap[key] = r
			}
			if provider != "" && key != "" {
				retrieverMap[key+":"+strings.ToLower(strings.TrimSpace(provider))] = r
			}
			if name != "" {
				retrieverMap[strings.ToLower(strings.TrimSpace(name))] = r
			}
		}

		vectorRet := &retriever.VectorRetriever{
			Embed:     ragclient.embeddingProvider,
			Store:     ragclient.vectordbProvider,
			TopK:      ragclient.config.RAG.TopK,
			Threshold: ragclient.config.RAG.Threshold,
		}
		retrievers = append(retrievers, vectorRet)
		register(vectorRet, "vector", ragclient.config.VectorDB.Provider, "vector")

		// Optional: add BM25 / Web retrievers from config
		for _, rc := range ragclient.config.Pipeline.Retrievers {
			switch rc.Type {
			case "bm25":
				bm := &retriever.BM25Retriever{
					Endpoint: rc.Params["endpoint"],
					Index:    rc.Params["index"],
					Client:   httpx.NewFromConfig(ragclient.config.Pipeline.HTTP),
				}
				retrievers = append(retrievers, bm)
				register(bm, rc.Type, rc.Provider, rc.Params["name"])
			case "web":
				web := &retriever.WebSearchRetriever{
					Provider: rc.Provider,
					Endpoint: rc.Params["endpoint"],
					APIKey:   rc.Params["api_key"],
					Client:   httpx.NewFromConfig(ragclient.config.Pipeline.HTTP),
				}
				retrievers = append(retrievers, web)
				register(web, rc.Type, rc.Provider, rc.Params["name"])
			case "vector":
				// Allow registering additional vector retrievers with custom name/provider if needed.
				register(vectorRet, rc.Type, rc.Provider, rc.Params["name"])
			default:
				// unknown type ignored for now
			}
		}
		var rr post.Reranker
		if ragclient.config.Pipeline.Post != nil && ragclient.config.Pipeline.Post.Rerank.Enable {
			rr = post.NewHTTPReranker(ragclient.config.Pipeline.Post.Rerank.Endpoint)
		}
		var ev crag.Evaluator
		if ragclient.config.Pipeline.CRAG != nil {
			cragCfg := ragclient.config.Pipeline.CRAG
			if cragCfg.Evaluator.Provider == "http" && cragCfg.Evaluator.Endpoint != "" {
				ev = &crag.HTTPEvaluator{
					Endpoint:    cragCfg.Evaluator.Endpoint,
					CorrectTh:   cragCfg.Evaluator.Correct,
					IncorrectTh: cragCfg.Evaluator.Incorrect,
				}
			}
		}
		ragclient.orch = &orchestrator.Orchestrator{Cfg: ragclient.config, Retrievers: retrievers, RetrieverMap: retrieverMap, Reranker: rr, Evaluator: ev}
	}
	return ragclient, nil
}

// ListChunks lists document chunks by knowledge ID, returns in ascending order of DocumentIndex
func (r *RAGClient) ListChunks() ([]schema.Document, error) {
	docs, err := r.vectordbProvider.ListDocs(context.Background(), MAX_LIST_DOCUMENT_ROW_COUNT)
	if err != nil {
		return nil, fmt.Errorf("list chunks failed, err: %w", err)
	}
	return docs, nil
}

// DeleteChunk deletes a specific document chunk
func (r *RAGClient) DeleteChunk(id string) error {
	if err := r.vectordbProvider.DeleteDocs(context.Background(), []string{id}); err != nil {
		return fmt.Errorf("delete chunk failed, err: %w", err)
	}
	return nil
}

func (r *RAGClient) CreateChunkFromText(text string, title string) ([]schema.Document, error) {

	docs, err := textsplitter.CreateDocuments(r.textSplitter, []string{text}, make([]map[string]any, 0))
	if err != nil {
		return nil, fmt.Errorf("create documents failed, err: %w", err)
	}

	results := make([]schema.Document, 0, len(docs))

	for chunkIndex, doc := range docs {
		doc.ID = uuid.New().String()
		doc.Metadata["chunk_index"] = chunkIndex
		doc.Metadata["chunk_title"] = title
		doc.Metadata["chunk_size"] = len(doc.Content)
		// Generate embedding for the document
		embedding, err := r.embeddingProvider.GetEmbedding(context.Background(), doc.Content)
		if err != nil {
			return nil, fmt.Errorf("create embedding failed, err: %w", err)
		}
		doc.Vector = embedding
		doc.CreatedAt = time.Now()
		results = append(results, doc)
	}

	if err := r.vectordbProvider.AddDoc(context.Background(), results); err != nil {
		return nil, fmt.Errorf("add documents failed, err: %w", err)
	}

	return results, nil
}

// SearchChunks searches for document chunks
func (r *RAGClient) SearchChunks(query string, topK int, threshold float64) ([]schema.SearchResult, error) {

	vector, err := r.embeddingProvider.GetEmbedding(context.Background(), query)
	if err != nil {
		return nil, fmt.Errorf("create embedding failed, err: %w", err)
	}
	options := &schema.SearchOptions{
		TopK:      topK,
		Threshold: threshold,
	}
	docs, err := r.vectordbProvider.SearchDocs(context.Background(), vector, options)
	if err != nil {
		return nil, fmt.Errorf("search chunks failed, err: %w", err)
	}
	return docs, nil
}

// Chat generates a response using LLM
func (r *RAGClient) Chat(query string) (string, error) {
	if r.llmProvider == nil {
		return "", fmt.Errorf("llm provider not initialized")
	}

	var contexts []string
	// Prefer enhanced pipeline when configured; fallback to baseline search
	if r.config.Pipeline != nil {
		cand, _ := r.orch.Run(context.Background(), query)
		if len(cand) == 0 {
			// fallback to baseline
			docs, err := r.SearchChunks(query, r.config.RAG.TopK, r.config.RAG.Threshold)
			if err != nil {
				return "", fmt.Errorf("search chunks failed, err: %w", err)
			}
			for _, doc := range docs {
				contexts = append(contexts, strings.ReplaceAll(doc.Document.Content, "\n", " "))
			}
		} else {
			for _, doc := range cand {
				contexts = append(contexts, strings.ReplaceAll(doc.Document.Content, "\n", " "))
			}
		}
	} else {
		docs, err := r.SearchChunks(query, r.config.RAG.TopK, r.config.RAG.Threshold)
		if err != nil {
			return "", fmt.Errorf("search chunks failed, err: %w", err)
		}
		for _, doc := range docs {
			contexts = append(contexts, strings.ReplaceAll(doc.Document.Content, "\n", " "))
		}
	}

	prompt := llm.BuildPrompt(query, contexts, "\n\n")
	resp, err := r.llmProvider.GenerateCompletion(context.Background(), prompt)
	if err != nil {
		return "", fmt.Errorf("generate completion failed, err: %w", err)
	}
	return resp, nil
}
