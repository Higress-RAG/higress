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
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/retriever"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/schema"
)

// Orchestrator wires the enhanced RAG pipeline stages.
type Orchestrator struct {
	Cfg          *config.Config
	Retrievers   []retriever.Retriever
	RetrieverMap map[string]retriever.Retriever
	Reranker     post.Reranker
	Evaluator    crag.Evaluator
}

// Run executes the pipeline for a given query and returns final candidates.
func (o *Orchestrator) Run(ctx context.Context, query string) ([]schema.SearchResult, error) {
	pc := o.Cfg.Pipeline
	if pc == nil {
		// No pipeline config; return empty to trigger fallback in caller.
		return nil, nil
	}

	intent := classifyQuery(query)
	profile := selectProfile(o.Cfg, intent.Intent)
	profile = o.normalizeProfile(profile)
	subQueries := []string{query}
	if pc.EnablePre {
		if o.Cfg.Pipeline.Pre != nil && o.Cfg.Pipeline.Pre.Rewrite.Enable {
			_, dense := rewriteVariants(query)
			query = dense // prefer dense as primary query for vector retrieval
		}
		if o.Cfg.Pipeline.Pre != nil && o.Cfg.Pipeline.Pre.Decompose.Enable && intent.MultiDoc {
			subQueries = decompose(query)
		}
	}

	// Hybrid retrieval
	lists := make([][]schema.SearchResult, 0)
	activeRetrievers := resolveRetrievers(o, profile)
	if len(activeRetrievers) == 0 && len(o.Retrievers) > 0 {
		activeRetrievers = append(activeRetrievers, o.Retrievers[0])
	}
	if pc.EnableHybrid {
		for _, sq := range expandQuery(profile, subQueries) {
			qctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
			var wg sync.WaitGroup
			resCh := make(chan []schema.SearchResult, len(activeRetrievers))
			for _, r := range activeRetrievers {
				rr := r
				wg.Add(1)
				go func(topK int) {
					defer wg.Done()
					if res, err := rr.Search(qctx, sq, topK); err == nil && len(res) > 0 {
						resCh <- res
					}
				}(profile.TopK)
			}
			wg.Wait()
			close(resCh)
			for res := range resCh {
				lists = append(lists, res)
			}
			cancel()
		}
	} else if len(activeRetrievers) > 0 {
		res, _ := activeRetrievers[0].Search(ctx, query, profile.TopK)
		if len(res) > 0 {
			lists = append(lists, res)
		}
	}

	// Fuse
	rrfk := pc.RRFK
	if rrfk <= 0 {
		rrfk = 60
	}
	fused := fusion.RRFScore(lists, rrfk)

	// Post-processing
	if len(fused) > 0 && pc.EnablePost && o.Reranker != nil && o.Cfg.Pipeline.Post != nil && o.Cfg.Pipeline.Post.Rerank.Enable {
		topN := o.Cfg.Pipeline.Post.Rerank.TopN
		if topN <= 0 {
			topN = profile.TopK
		}
		if rr, err := o.Reranker.Rerank(ctx, query, fused, topN); err == nil && len(rr) > 0 {
			fused = rr
		}
	}

	// Optional context compression per document
	if len(fused) > 0 && pc.EnablePost && o.Cfg.Pipeline.Post != nil && o.Cfg.Pipeline.Post.Compress.Enable {
		ratio := o.Cfg.Pipeline.Post.Compress.TargetRatio
		for i := range fused {
			fused[i].Document.Content = post.CompressText(fused[i].Document.Content, ratio)
		}
	}

	// CRAG
	if len(fused) > 0 && pc.EnableCRAG && o.Evaluator != nil {
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
		switch verdict {
		case crag.VerdictCorrect:
			fused = crag.CorrectAction(fused)
		case crag.VerdictIncorrect:
			fused = crag.IncorrectAction()
		case crag.VerdictAmbiguous:
			fused = crag.AmbiguousAction(fused, nil)
		}
	}

	return fused, nil
}

func (o *Orchestrator) normalizeProfile(profile config.RetrievalProfile) config.RetrievalProfile {
	if profile.TopK <= 0 {
		profile.TopK = defaultTopK(o.Cfg)
	}
	if profile.Threshold <= 0 && o != nil && o.Cfg != nil {
		profile.Threshold = o.Cfg.RAG.Threshold
	}
	return profile
}

func selectProfile(cfg *config.Config, intent string) config.RetrievalProfile {
	if cfg == nil || cfg.Pipeline == nil {
		return config.RetrievalProfile{Name: "default"}
	}
	pc := cfg.Pipeline
	intent = strings.ToLower(strings.TrimSpace(intent))

	// First, try to match by intent label.
	for i := range pc.RetrievalProfiles {
		prof := pc.RetrievalProfiles[i]
		if prof.Intent != "" && strings.EqualFold(prof.Intent, intent) {
			return prof
		}
	}

	// Then try to match default profile name.
	if pc.DefaultProfile != "" {
		for i := range pc.RetrievalProfiles {
			prof := pc.RetrievalProfiles[i]
			if strings.EqualFold(prof.Name, pc.DefaultProfile) {
				return prof
			}
		}
	}

	if len(pc.RetrievalProfiles) > 0 {
		return pc.RetrievalProfiles[0]
	}

	return config.RetrievalProfile{Name: "default"}
}

func resolveRetrievers(o *Orchestrator, profile config.RetrievalProfile) []retriever.Retriever {
	if o == nil {
		return nil
	}
	if len(profile.Retrievers) == 0 {
		result := make([]retriever.Retriever, 0, len(o.Retrievers))
		for _, r := range o.Retrievers {
			if r == nil {
				continue
			}
			if !profile.UseWeb && r.Type() == "web" {
				continue
			}
			result = appendUniqueRetriever(result, r)
		}
		if profile.UseWeb {
			if web := findRetriever(o, "web"); web != nil {
				result = appendUniqueRetriever(result, web)
			}
		}
		return result
	}

	seen := make(map[retriever.Retriever]struct{})
	result := make([]retriever.Retriever, 0, len(profile.Retrievers))
	for _, name := range profile.Retrievers {
		key := normalizeKey(name)
		if key == "" {
			continue
		}
		if r := findRetriever(o, key); r != nil {
			if _, ok := seen[r]; ok {
				continue
			}
			seen[r] = struct{}{}
			result = append(result, r)
		}
	}
	if profile.UseWeb {
		if web := findRetriever(o, "web"); web != nil {
			if _, ok := seen[web]; !ok {
				result = append(result, web)
			}
		}
	}
	if len(result) == 0 {
		return o.Retrievers
	}
	return result
}

func expandQuery(_ config.RetrievalProfile, subQueries []string) []string {
	seen := make(map[string]struct{}, len(subQueries))
	expanded := make([]string, 0, len(subQueries))
	for _, q := range subQueries {
		trimmed := strings.TrimSpace(q)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		expanded = append(expanded, trimmed)
	}
	if len(expanded) == 0 {
		expanded = []string{""}
	}
	return expanded
}

func findRetriever(o *Orchestrator, key string) retriever.Retriever {
	key = normalizeKey(key)
	if key == "" || o == nil {
		return nil
	}
	if o.RetrieverMap != nil {
		if r, ok := o.RetrieverMap[key]; ok {
			return r
		}
		if idx := strings.Index(key, ":"); idx > 0 {
			base := key[:idx]
			if r, ok := o.RetrieverMap[base]; ok {
				return r
			}
		}
	}
	for _, r := range o.Retrievers {
		if normalizeKey(r.Type()) == key {
			return r
		}
	}
	return nil
}

func appendUniqueRetriever(list []retriever.Retriever, r retriever.Retriever) []retriever.Retriever {
	for _, existing := range list {
		if existing == r {
			return list
		}
	}
	return append(list, r)
}

func normalizeKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func defaultTopK(cfg *config.Config) int {
	if cfg != nil && cfg.RAG.TopK > 0 {
		return cfg.RAG.TopK
	}
	return 10
}
