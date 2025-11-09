package orchestrator

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/config"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/crag"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/fusion"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/post"
	pre_retrieve "github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/pre-retrieve"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/retriever"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/schema"
)

// Orchestrator wires the enhanced RAG pipeline stages.
type Orchestrator struct {
	Cfg                 *config.Config
	Retrievers          []retriever.Retriever
	Reranker            post.Reranker
	Evaluator           crag.Evaluator
	WebSearcher         *crag.WebSearcher
	QueryRewriter       *crag.QueryRewriter
	Refiner             *crag.KnowledgeRefiner
	LLMProvider         interface{}           // Can be used for query rewriting and knowledge refinement
	PreRetrieveProvider pre_retrieve.Provider // Pre-retrieve processor
}

// Run executes the pipeline for a given query and returns final candidates.
func (o *Orchestrator) Run(ctx context.Context, query string) ([]schema.SearchResult, error) {
	pc := o.Cfg.Pipeline
	if pc == nil {
		// No pipeline config; return empty to trigger fallback in caller.
		return nil, nil
	}

	// Pre-retrieve processing
	subQueries := []string{query}

	if pc.EnablePre && o.PreRetrieveProvider != nil {
		// Use the complete pre-retrieve provider (PreQRAG)
		sessionID := "" // TODO: Extract from context or request if available
		result, err := o.PreRetrieveProvider.Process(ctx, query, sessionID)
		if err != nil {
			// Log warning but continue with original query
			logWarnf("Pre-retrieve processing failed: %v, using original query", err)
		} else if result != nil {
			// Extract queries from the plan nodes
			if len(result.Plan.Nodes) > 0 {
				subQueries = make([]string, 0, len(result.Plan.Nodes))
				for _, node := range result.Plan.Nodes {
					// Use dense rewrite for vector retrieval
					// For BM25/sparse retrieval, could use node.SparseRewrite
					subQueries = append(subQueries, node.DenseRewrite)
				}

				// Update query to aligned version for logging/later use
				if result.AlignedQuery.Query != "" {
					query = result.AlignedQuery.Query
				}
			} else {
				// Fallback to aligned query if no plan nodes
				if result.AlignedQuery.Query != "" {
					query = result.AlignedQuery.Query
					subQueries = []string{query}
				}
			}
		}
	} else if pc.EnablePre && o.Cfg.Pipeline.Pre != nil {
		// Fallback to simple pre-processing if PreRetrieve not configured
		// This is deprecated but kept for backward compatibility
		if o.Cfg.Pipeline.Pre.Decompose.Enable {
			// Simple decomposition: just use original query
			subQueries = []string{query}
		}
	}

	// Hybrid retrieval
	lists := make([][]schema.SearchResult, 0)
	if pc.EnableHybrid {
		for _, sq := range subQueries {
			// Short timeout per sub-query; fan-out to retrievers in parallel
			qctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
			var wg sync.WaitGroup
			resCh := make(chan []schema.SearchResult, len(o.Retrievers))
			for _, r := range o.Retrievers {
				rr := r
				wg.Add(1)
				go func() {
					defer wg.Done()
					if res, _ := rr.Search(qctx, sq, o.Cfg.RAG.TopK); len(res) > 0 {
						resCh <- res
					}
				}()
			}
			wg.Wait()
			close(resCh)
			for res := range resCh {
				lists = append(lists, res)
			}
			cancel()
		}
	} else {
		// Minimal: use first retriever only
		if len(o.Retrievers) > 0 {
			res, _ := o.Retrievers[0].Search(ctx, query, o.Cfg.RAG.TopK)
			if len(res) > 0 {
				lists = append(lists, res)
			}
		}
	}

	// Fuse
	fused := fusion.RRFScore(lists, pc.RRFK)

	// Post-processing
	if pc.EnablePost && o.Reranker != nil && o.Cfg.Pipeline.Post != nil && o.Cfg.Pipeline.Post.Rerank.Enable {
		topN := o.Cfg.Pipeline.Post.Rerank.TopN
		rr, _ := o.Reranker.Rerank(ctx, query, fused, topN)
		fused = rr
	}

	// Optional context compression per document
	if pc.EnablePost && o.Cfg.Pipeline.Post != nil && o.Cfg.Pipeline.Post.Compress.Enable {
		ratio := o.Cfg.Pipeline.Post.Compress.TargetRatio
		for i := range fused {
			fused[i].Document.Content = post.CompressText(fused[i].Document.Content, ratio)
		}
	}

	// CRAG
	if pc.EnableCRAG && o.Evaluator != nil {
		// Concatenate top-k contexts for quick evaluation
		var b strings.Builder
		limit := len(fused)
		if limit > 5 {
			limit = 5
		}
		for i := 0; i < limit; i++ {
			b.WriteString(fused[i].Document.Content)
			b.WriteString("\n\n")
		}
		score, verdict, err := o.Evaluator.Evaluate(ctx, query, b.String())
		if err != nil {
			// FailMode: closed -> bubble error, open -> keep fused
			fm := "open"
			if o.Cfg.Pipeline.CRAG != nil && o.Cfg.Pipeline.CRAG.FailMode != "" {
				fm = o.Cfg.Pipeline.CRAG.FailMode
			}
			if fm == "closed" {
				return nil, err
			}
			return fused, nil
		}
		_ = score // score could be logged/returned later

		// Build ActionContext for CRAG actions
		actionCtx := &crag.ActionContext{
			Query:         query,
			Context:       ctx,
			WebSearcher:   o.WebSearcher,
			QueryRewriter: o.QueryRewriter,
			Refiner:       o.Refiner,
		}

		// Execute appropriate corrective action based on verdict
		switch verdict {
		case crag.VerdictCorrect:
			fused = crag.CorrectAction(actionCtx, fused)
		case crag.VerdictIncorrect:
			fused = crag.IncorrectAction(actionCtx)
		case crag.VerdictAmbiguous:
			fused = crag.AmbiguousAction(actionCtx, fused, nil)
		}
	}

	return fused, nil
}

// Helper logging functions
func logWarnf(format string, args ...interface{}) {
	defer func() {
		if r := recover(); r != nil {
			// Silently ignore logging errors
		}
	}()
	// Note: Using a simple print for now, can be replaced with proper logger
	// fmt.Printf("[WARN] "+format+"\n", args...)
	_ = format
	_ = args
}
