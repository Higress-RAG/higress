package rag

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/cache"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/common/httpx"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/config"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/crag"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/embedding"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/feedback"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/fusion"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/gating"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/llm"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/metrics"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/orchestrator"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/post"
	pre_retrieve "github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/pre-retrieve"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/profile"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/retrieval"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/retriever"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/router"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/schema"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/textsplitter"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/vectordb"
	"github.com/envoyproxy/envoy/contrib/golang/common/go/api"
	"github.com/google/uuid"
)

const (
	MAX_LIST_KNOWLEDGE_ROW_COUNT = 1000
	MAX_LIST_DOCUMENT_ROW_COUNT  = 1000
)

// RAGClient represents the RAG (Retrieval-Augmented Generation) client
type RAGClient struct {
	config             *config.Config
	vectordbProvider   vectordb.VectorStoreProvider
	embeddingProvider  embedding.Provider
	textSplitter       textsplitter.TextSplitter
	llmProvider        llm.Provider
	sessions           SessionStore
	profileProvider    profile.Provider
	retrievalProvider  retrieval.Provider
	gatingProvider     gating.Provider
	reranker           post.Reranker
	evaluator          crag.Evaluator
	feedbackManager    *feedback.Manager
	routerProvider     router.Router
	l1Cache            cache.Cache
	cacheMode          string
	indexVersion       string
	cacheFusionVersion string
	orch               *orchestrator.Orchestrator
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
	ragclient.indexVersion = ragclient.config.VectorDB.Collection

	// Build enhanced pipeline providers if configured
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
				if tk := rc.Params["top_k"]; tk != "" {
					if n, err := strconv.Atoi(tk); err == nil {
						bm.MaxTopK = n
					}
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
				if tk := rc.Params["top_k"]; tk != "" {
					if n, err := strconv.Atoi(tk); err == nil {
						web.MaxTopK = n
					}
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

		// Initialize providers
		ragclient.profileProvider = profile.NewProvider(ragclient.config.Pipeline)

		rrfK := ragclient.config.Pipeline.RRFK
		if rrfK <= 0 {
			rrfK = 60
		}
		ragclient.retrievalProvider = retrieval.NewProvider(retrievers, retrieverMap, rrfK)

		// Configure fusion strategy
		var (
			fusionStrategy fusion.Strategy = fusion.NewRRFStrategy(rrfK)
			fusionParams                   = map[string]any{"k": rrfK}
		)
		if ragclient.config.Pipeline.Fusion != nil {
			strategyName := ragclient.config.Pipeline.Fusion.Strategy
			if strategyName == "" {
				strategyName = "rrf"
			}
			if ragclient.config.Pipeline.Fusion.EnableLearned {
				strategyName = "learned"
			}

			params := make(map[string]any)
			for k, v := range ragclient.config.Pipeline.Fusion.Params {
				params[k] = v
			}
			if ragclient.config.Pipeline.Fusion.WeightsURI != "" {
				params["weights_uri"] = ragclient.config.Pipeline.Fusion.WeightsURI
			}
			if ragclient.config.Pipeline.Fusion.Fallback != "" {
				params["fallback"] = ragclient.config.Pipeline.Fusion.Fallback
			}
			if ragclient.config.Pipeline.Fusion.TimeoutMs > 0 {
				params["timeout_ms"] = ragclient.config.Pipeline.Fusion.TimeoutMs
			}
			if ragclient.config.Pipeline.Fusion.RefreshSeconds > 0 {
				params["refresh_seconds"] = ragclient.config.Pipeline.Fusion.RefreshSeconds
			}
			if ragclient.config.Pipeline.Fusion.TrafficPercent > 0 {
				params["traffic_percent"] = ragclient.config.Pipeline.Fusion.TrafficPercent
			}

			strategy, sanitized, err := fusion.NewStrategy(strategyName, params)
			if err != nil {
				api.LogWarnf("rag: fallback to RRF fusion due to strategy init error: %v", err)
			} else {
				fusionStrategy = strategy
				if sanitized != nil {
					fusionParams = sanitized
				}
			}
		}
		ragclient.retrievalProvider.SetFusionStrategy(fusionStrategy, fusionParams)

		if ragclient.config.Pipeline.Feedback != nil {
			ragclient.feedbackManager = feedback.NewManager(ragclient.config.Pipeline.Feedback)
		}

		ragclient.gatingProvider = gating.NewProvider(vectorRet)
		if ragclient.feedbackManager != nil {
			ragclient.gatingProvider.WithFeedback(ragclient.feedbackManager, ragclient.config.Pipeline.Feedback)
		}

		if ragclient.config.Pipeline.Router != nil && ragclient.config.Pipeline.Router.Enable {
			ragclient.routerProvider = router.NewRouter(ragclient.config.Pipeline.Router, ragclient.config.Pipeline.HTTP)
		}

		if ragclient.config.Pipeline.Cache != nil && ragclient.config.Pipeline.Cache.L1 != nil && ragclient.config.Pipeline.Cache.L1.Enable {
			l1 := ragclient.config.Pipeline.Cache.L1
			ttl := time.Duration(l1.TTLSeconds) * time.Second
			if ttl <= 0 {
				ttl = 2 * time.Minute
			}
			capacity := l1.MaxEntries
			if capacity <= 0 {
				capacity = 500
			}
			ragclient.l1Cache = cache.NewLRU(capacity, ttl)
			mode := strings.ToLower(strings.TrimSpace(l1.Mode))
			if mode == "" {
				mode = "post"
			}
			if mode != "post" {
				api.LogInfof("rag: L1 cache mode %q not fully supported, defaulting to post", mode)
				mode = "post"
			}
			ragclient.cacheMode = mode
		}

		// Initialize reranker with support for multiple providers
		var rr post.Reranker
		if ragclient.config.Pipeline.Post != nil && ragclient.config.Pipeline.Post.Rerank.Enable {
			rerankCfg := ragclient.config.Pipeline.Post.Rerank
			switch rerankCfg.Provider {
			case "llm":
				// Use LLM-based reranker
				if ragclient.llmProvider != nil {
					rr = &post.LLMReranker{
						Provider: ragclient.llmProvider,
						Model:    rerankCfg.Model,
					}
				}
			case "keyword":
				// Use keyword-based reranker
				rr = &post.KeywordReranker{
					MinKeywordLength: 3,
					BaseScoreWeight:  0.5,
				}
			case "model":
				// Use model-based reranker (BGE-reranker, Cohere rerank, etc.)
				rr = &post.ModelReranker{
					Endpoint: rerankCfg.Endpoint,
					Model:    rerankCfg.Model,
					APIKey:   rerankCfg.APIKey,
				}
			default:
				// Default to HTTP reranker for backward compatibility
				rr = post.NewHTTPReranker(rerankCfg.Endpoint)
			}
		}

		// Initialize CRAG components (for orchestrator)
		var ev crag.Evaluator
		var webSearcher *crag.WebSearcher
		var queryRewriter *crag.QueryRewriter
		var refiner *crag.KnowledgeRefiner

		if ragclient.config.Pipeline.CRAG != nil {
			cragCfg := ragclient.config.Pipeline.CRAG

			// Initialize evaluator (HTTP or LLM-based)
			if cragCfg.Evaluator.Provider == "http" && cragCfg.Evaluator.Endpoint != "" {
				httpEval := &crag.HTTPEvaluator{
					Endpoint:    cragCfg.Evaluator.Endpoint,
					CorrectTh:   cragCfg.Evaluator.Correct,
					IncorrectTh: cragCfg.Evaluator.Incorrect,
				}
				ragclient.evaluator = httpEval
				ev = httpEval
			} else if cragCfg.Evaluator.Provider == "llm" && ragclient.llmProvider != nil {
				// Use LLM-based evaluator
				llmEval := &crag.LLMEvaluator{
					Provider:    ragclient.llmProvider,
					CorrectTh:   cragCfg.Evaluator.Correct,
					IncorrectTh: cragCfg.Evaluator.Incorrect,
				}
				ragclient.evaluator = llmEval
				ev = llmEval
			}

			// Initialize web searcher from CRAG config or retriever config
			for _, rc := range ragclient.config.Pipeline.Retrievers {
				if rc.Type == "web" {
					webSearcher = &crag.WebSearcher{
						Provider: rc.Provider,
						Endpoint: rc.Params["endpoint"],
						APIKey:   rc.Params["api_key"],
					}
					break
				}
			}

			// Initialize query rewriter if LLM available
			if ragclient.llmProvider != nil {
				queryRewriter = &crag.QueryRewriter{
					Provider: ragclient.llmProvider,
				}
				refiner = &crag.KnowledgeRefiner{
					Provider: ragclient.llmProvider,
				}
			}
		}

		// Initialize Compressor if enabled
		var compressor post.Compressor
		if ragclient.config.Pipeline.Post != nil && ragclient.config.Pipeline.Post.Compress.Enable {
			compressCfg := ragclient.config.Pipeline.Post.Compress
			method := compressCfg.Method
			if method == "" {
				method = "truncate" // Default method
			}
			targetRatio := compressCfg.TargetRatio
			if targetRatio == 0 {
				targetRatio = 0.7 // Default ratio
			}
			compressor = post.NewCompressor(method, targetRatio, ragclient.llmProvider)
		}

		// Initialize Pre-Retrieve Provider if enabled
		var preRetrieveProvider pre_retrieve.Provider
		if ragclient.config.Pipeline.EnablePre && ragclient.config.Pipeline.PreRetrieve != nil {
			preRetCfg := ragclient.config.Pipeline.PreRetrieve
			// Set LLM config if available
			if ragclient.llmProvider != nil {
				preRetCfg.LLM = ragclient.config.LLM
			}

			provider, err := pre_retrieve.NewPreRetrieveProvider(preRetCfg)
			if err != nil {
				// Log warning but don't fail - pre-retrieve is optional
				fmt.Printf("[WARN] Failed to initialize pre-retrieve provider: %v\n", err)
			} else {
				preRetrieveProvider = provider
			}
		}

		ragclient.orch = &orchestrator.Orchestrator{
			Cfg:                 ragclient.config,
			Retrievers:          retrievers,
			Reranker:            rr,
			Compressor:          compressor,
			Evaluator:           ev,
			WebSearcher:         webSearcher,
			QueryRewriter:       queryRewriter,
			Refiner:             refiner,
			LLMProvider:         ragclient.llmProvider,
			PreRetrieveProvider: preRetrieveProvider,
		}
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
	if r.config.Pipeline != nil && r.retrievalProvider != nil {
		// Use provider-based pipeline
		results := r.runEnhancedPipeline(context.Background(), query)
		if len(results) == 0 {
			// fallback to baseline
			docs, err := r.SearchChunks(query, r.config.RAG.TopK, r.config.RAG.Threshold)
			if err != nil {
				return "", fmt.Errorf("search chunks failed, err: %w", err)
			}
			for _, doc := range docs {
				contexts = append(contexts, strings.ReplaceAll(doc.Document.Content, "\n", " "))
			}
		} else {
			for _, doc := range results {
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

// runEnhancedPipeline executes the enhanced RAG pipeline using providers
func (r *RAGClient) runEnhancedPipeline(ctx context.Context, query string) []schema.SearchResult {
	var metricsRecord *metrics.RetrievalMetrics
	if r.config.Pipeline != nil {
		metricsRecord = metrics.NewRetrievalMetrics()
		metricsRecord.QueryID = uuid.NewString()
		metricsRecord.Query = query
		metricsRecord.Timestamp = time.Now()
	}

	// Select base profile
	prof := r.profileProvider.SelectDefault()
	profileSource := "default"
	if r.config.Pipeline.DefaultProfile != "" {
		if p := r.profileProvider.SelectByName(r.config.Pipeline.DefaultProfile); p.Name != "" {
			prof = p
			profileSource = "default_profile"
		}
	}
	prof = r.profileProvider.Normalize(prof)

	// Router decision
	if r.routerProvider != nil {
		if metricsRecord != nil {
			metricsRecord.RouterEnabled = true
			if r.config.Pipeline.Router != nil {
				metricsRecord.RouterProvider = r.config.Pipeline.Router.Provider
			}
		}
		if decision, err := r.routerProvider.Route(ctx, query); err != nil {
			if metricsRecord != nil {
				metricsRecord.RouterError = err.Error()
			}
		} else if decision != nil {
			if metricsRecord != nil {
				metricsRecord.RouterProfile = decision.ProfileName
				resetMap(metricsRecord.RouterVariants)
				for k, v := range decision.VariantBudgets {
					metricsRecord.RouterVariants[k] = v.TopK
				}
			}
			profileSource = "router"
			if decision.ProfileName != "" {
				if p := r.profileProvider.SelectByName(decision.ProfileName); p.Name != "" {
					prof = p
					profileSource = "router_profile"
				}
			}
			prof = router.ApplyDecision(decision, prof)
			prof = r.profileProvider.Normalize(prof)
		}
	}

	// Gating decision
	if r.gatingProvider != nil && (prof.VectorGate > 0 || prof.VectorLowGate > 0) {
		decision := r.gatingProvider.Evaluate(ctx, query, prof, metricsRecord)
		prof = r.gatingProvider.ApplyDecision(decision, prof)
		prof = r.profileProvider.Normalize(prof)
	}

	if metricsRecord != nil {
		metricsRecord.RecordProfileSelection(prof.Name, profileSource)
		if len(prof.VariantBudgets) > 0 && len(metricsRecord.RouterVariants) == 0 {
			resetMap(metricsRecord.RouterVariants)
			for k, v := range prof.VariantBudgets {
				metricsRecord.RouterVariants[k] = v
			}
		}
	}

	cacheKey := ""
	if r.l1Cache != nil && r.cacheMode == "post" {
		cacheKey = r.buildCacheKey(query, prof)
		if cached, ok := r.l1Cache.Get(cacheKey); ok {
			if docs, ok := cached.([]schema.SearchResult); ok {
				api.LogInfof("rag: L1 cache hit for profile=%s", prof.Name)
				if metricsRecord != nil {
					metricsRecord.Success = true
					metricsRecord.LogJSON()
				}
				return cloneResults(docs)
			}
		}
	}

	// Retrieval
	queries := []string{query}
	results := r.retrievalProvider.Retrieve(ctx, queries, prof, metricsRecord)

	if metricsRecord != nil {
		metricsRecord.TotalRetrieved = len(results)
		if version := metricsRecord.FusionWeightsVersion; version != "" && r.cacheFusionVersion != version {
			if r.l1Cache != nil && r.cacheFusionVersion != "" {
				r.l1Cache.Purge()
			}
			r.cacheFusionVersion = version
		}
	}

	// Reranking
	if len(results) > 0 && r.config.Pipeline.EnablePost && r.config.Pipeline.Post != nil &&
		r.config.Pipeline.Post.Rerank.Enable && r.reranker != nil {
		topN := r.config.Pipeline.Post.Rerank.TopN
		if topN <= 0 || topN > len(results) {
			topN = len(results)
		}
		if reranked, err := r.reranker.Rerank(ctx, query, results, topN); err == nil && len(reranked) > 0 {
			results = reranked
		}
		if metricsRecord != nil {
			metricsRecord.RerankEnabled = true
			metricsRecord.RerankResultCount = len(results)
		}
	}

	// Compression
	if len(results) > 0 && r.config.Pipeline.EnablePost && r.config.Pipeline.Post != nil &&
		r.config.Pipeline.Post.Compress.Enable {
		ratio := r.config.Pipeline.Post.Compress.TargetRatio
		for i := range results {
			results[i].Document.Content = post.CompressText(results[i].Document.Content, ratio)
		}
		if metricsRecord != nil {
			metricsRecord.CompressEnabled = true
		}
	}

	// CRAG evaluation
	if len(results) > 0 && r.config.Pipeline.EnableCRAG && r.evaluator != nil {
		var builder strings.Builder
		limit := len(results)
		if limit > 5 {
			limit = 5
		}
		for i := 0; i < limit; i++ {
			builder.WriteString(results[i].Document.Content)
			builder.WriteString("\n\n")
		}
		_, verdict, err := r.evaluator.Evaluate(ctx, query, builder.String())
		if err == nil {
			if r.feedbackManager != nil {
				r.feedbackManager.Record(prof.Name, verdict, 0)
			}
			// Build ActionContext for CRAG actions
			actionCtx := &crag.ActionContext{
				Query:   query,
				Context: ctx,
				// WebSearcher, QueryRewriter, and Refiner would be available via orchestrator
				// For direct RAGClient usage, they are optional
			}
			switch verdict {
			case crag.VerdictCorrect:
				results = crag.CorrectAction(actionCtx, results)
			case crag.VerdictIncorrect:
				results = crag.IncorrectAction(actionCtx)
			case crag.VerdictAmbiguous:
				results = crag.AmbiguousAction(actionCtx, results, nil)
			}
			if metricsRecord != nil {
				metricsRecord.CRAGEnabled = true
				metricsRecord.CRAGVerdict = verdict.String()
			}
		}
	}

	if r.l1Cache != nil && r.cacheMode == "post" && cacheKey != "" && len(results) > 0 {
		r.l1Cache.Set(cacheKey, cloneResults(results), 0)
	}

	if metricsRecord != nil {
		metricsRecord.Success = len(results) > 0
		metricsRecord.LogJSON()
	}

	return results
}

func (r *RAGClient) buildCacheKey(query string, profile config.RetrievalProfile) string {
	normalized := strings.ToLower(strings.TrimSpace(query))
	base := fmt.Sprintf("%s|%s|%s|%d|%d|%s|%s", normalized, profile.Name, r.indexVersion, profile.TopK, r.rerankTopN(), budgetsSignature(profile.VariantBudgets), r.cacheFusionVersion)
	hash := sha1.Sum([]byte(base))
	return hex.EncodeToString(hash[:])
}

func (r *RAGClient) rerankTopN() int {
	if r.config.Pipeline != nil && r.config.Pipeline.Post != nil {
		if r.config.Pipeline.Post.Rerank.TopN > 0 {
			return r.config.Pipeline.Post.Rerank.TopN
		}
	}
	return 0
}

func cloneResults(results []schema.SearchResult) []schema.SearchResult {
	if len(results) == 0 {
		return nil
	}
	out := make([]schema.SearchResult, len(results))
	for i, res := range results {
		out[i].Score = res.Score
		out[i].Document = cloneDocument(res.Document)
	}
	return out
}

func cloneDocument(doc schema.Document) schema.Document {
	cloned := doc
	if doc.Metadata != nil {
		cloned.Metadata = make(map[string]interface{}, len(doc.Metadata))
		for k, v := range doc.Metadata {
			cloned.Metadata[k] = v
		}
	}
	if doc.Vector != nil {
		cloned.Vector = cloneVector(doc.Vector)
	}
	return cloned
}

func cloneVector(vec []float32) []float32 {
	if len(vec) == 0 {
		return nil
	}
	out := make([]float32, len(vec))
	copy(out, vec)
	return out
}

func resetMap(m map[string]int) {
	if m == nil {
		return
	}
	for k := range m {
		delete(m, k)
	}
}

func budgetsSignature(budgets map[string]int) string {
	if len(budgets) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(budgets))
	for k := range budgets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var builder strings.Builder
	for _, k := range keys {
		builder.WriteString(k)
		builder.WriteByte('=')
		builder.WriteString(strconv.Itoa(budgets[k]))
		builder.WriteByte(';')
	}
	return builder.String()
}
