package orchestrator

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/config"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/crag"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/fusion"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/orchestrator/preclient"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/post"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/metrics"
	prepb "github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/proto/precontract/v1"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/retriever"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/schema"
	"github.com/envoyproxy/envoy/contrib/golang/common/go/api"
)

// Orchestrator wires the enhanced RAG pipeline stages.
type Orchestrator struct {
	Cfg          *config.Config
	Retrievers   []retriever.Retriever
	RetrieverMap map[string]retriever.Retriever
	Reranker     post.Reranker
	Evaluator    crag.Evaluator
	Pre          preclient.Client
}

// Run executes the pipeline for a given query and returns final candidates.
func (o *Orchestrator) Run(ctx context.Context, query string) ([]schema.SearchResult, error) {
	pc := o.Cfg.Pipeline
	if pc == nil {
		// No pipeline config; return empty to trigger fallback in caller.
		return nil, nil
	}

	// Pre stage: external or fallback heuristics
	intent := classifyQuery(query)
	profile := selectProfile(o.Cfg, intent.Intent)
    transformations := []string{}
    var preTrans []*prepb.QueryTransformation
	subQueries := []string{query}
    var perTimeout time.Duration = 300 * time.Millisecond
    if pc.EnablePre && o.Pre != nil && o.Cfg.Pipeline.Pre.Service.Endpoint != "" {
        ctxPre, cancel := context.WithTimeout(ctx, 900*time.Millisecond)
        resp, err := o.Pre.Generate(ctxPre, &prepb.PreprocessRequest{Query: query})
        cancel()
        if err == nil && resp != nil {
            // choose profile by suggested_profile first
            if len(resp.Intents) > 0 && resp.Intents[0].SuggestedProfile != "" {
                profile = profileByName(o.Cfg, resp.Intents[0].SuggestedProfile)
            }
            api.LogInfof("pre: profile=%s use_web=%v transforms=%d subqueries=%d", profile.Name, profile.UseWeb, len(transformations), len(subQueries))
            // apply requires_web hint
            if len(resp.Intents) > 0 {
                profile.UseWeb = resp.Intents[0].RequiresWeb
            }
            // collect transformations in priority order
            if len(resp.Transformations) > 0 {
                // keep the structs for conditional appending later
                for i := range resp.Transformations {
                    t := resp.Transformations[i]
                    if t != nil { preTrans = append(preTrans, t) }
                }
            }
            // decomposition -> subqueries if provided
            if resp.Decomposition != nil && len(resp.Decomposition.Tasks) > 0 {
                subs := make([]string, 0, len(resp.Decomposition.Tasks))
                for _, tk := range resp.Decomposition.Tasks {
                    if tk.QueryText != "" {
                        subs = append(subs, tk.QueryText)
                    }
                }
                if len(subs) > 0 {
                    subQueries = subs
                }
            }
            // constraints -> timeouts
            if resp.Constraints != nil {
                if resp.Constraints.PerRetrieverTimeoutMs > 0 {
                    perTimeout = time.Duration(resp.Constraints.PerRetrieverTimeoutMs) * time.Millisecond
                } else if resp.Constraints.LatencyBudgetMs > 0 {
                    perTimeout = time.Duration(resp.Constraints.LatencyBudgetMs/3) * time.Millisecond
                }
            }
        } else {
            api.LogWarnf("preprocessor call failed: %v", err)
        }
    } else if pc.EnablePre {
        // fallback lightweight transformers
        _, dense := rewriteVariants(query)
        transformations = append(transformations, dense)
        if intent.MultiDoc && o.Cfg.Pipeline.Pre.Decompose.Enable {
            subQueries = decompose(query)
        }
    }
    profile = o.normalizeProfile(profile)
    // Defer adding transformations until after gating (so low-score branch can prioritize HYDE/expansion)

	// Hybrid retrieval
	lists := make([][]schema.SearchResult, 0)
	activeRetrievers := resolveRetrievers(o, profile)
	// Two-phase gating: vector preflight to suppress/force web based on thresholds
	if v := findRetriever(o, "vector"); v != nil && (profile.VectorGate > 0 || profile.VectorLowGate > 0) {
		preCtx, cancel := context.WithTimeout(ctx, perTimeout)
		res, _ := v.Search(preCtx, query, minInt(5, profile.TopK))
		cancel()
		var top1 float64 = -1
		if len(res) > 0 { top1 = res[0].Score; metrics.ObserveVectorTop1(top1) }
		// suppress web on high vector score
		if profile.UseWeb && profile.VectorGate > 0 && top1 >= profile.VectorGate {
			filtered := make([]retriever.Retriever, 0, len(activeRetrievers))
			for _, r := range activeRetrievers {
				if r != nil && r.Type() == "web" { continue }
				filtered = append(filtered, r)
			}
			activeRetrievers = filtered
			metrics.IncGating("suppress_web")
			api.LogInfof("gating: vector top1 %.4f >= %.4f, suppress web", top1, profile.VectorGate)
		}
		// force web on low vector score
		if profile.VectorLowGate > 0 && top1 >= 0 && top1 < profile.VectorLowGate && profile.ForceWebOnLow {
			hasWeb := false
			for _, r := range activeRetrievers { if r != nil && r.Type() == "web" { hasWeb = true; break } }
			if !hasWeb {
				if web := findRetriever(o, "web"); web != nil {
					activeRetrievers = append(activeRetrievers, web)
					metrics.IncGating("force_web")
					api.LogInfof("gating: vector top1 %.4f < %.4f, force web", top1, profile.VectorLowGate)
				}
			} else {
				metrics.IncGating("neutral")
			}
		}
	}
    if profile.MaxFanout > 0 && len(activeRetrievers) > profile.MaxFanout {
        activeRetrievers = activeRetrievers[:profile.MaxFanout]
    }
    // Append Pre transformations: prioritize HYDE/EXPANSION when vector low-score
    if len(preTrans) > 0 {
        // low-score condition derived from gates
        var low bool
        // We don't have top1 accessible here; reuse metrics side-effects would be hacky.
        // Approximate by using config gates: if low gate set and web is enabled, bias to include HYDE/EXPANSION.
        if profile.VectorLowGate > 0 && profile.ForceWebOnLow {
            low = true
        }
        if low {
            pri := make([]string, 0)
            for _, t := range preTrans {
                if t != nil && (t.Type == prepb.TransformationType_TRANSFORMATION_TYPE_HYDE_SEED || t.Type == prepb.TransformationType_TRANSFORMATION_TYPE_QUERY_EXPANSION) && t.Text != "" {
                    pri = append(pri, t.Text)
                }
            }
            if len(pri) > 0 {
                // put priority transforms in front
                subQueries = append(pri, subQueries...)
            }
        }
        // Append all transformed queries for completeness
        for _, t := range preTrans { if t != nil && t.Text != "" { transformations = append(transformations, t.Text) } }
    }
    if len(transformations) > 0 {
        subQueries = append(subQueries, transformations...)
    }
    if len(activeRetrievers) == 0 && len(o.Retrievers) > 0 {
        activeRetrievers = append(activeRetrievers, o.Retrievers[0])
    }
    api.LogInfof("retrieval: profile=%s retrievers=%d per_timeout_ms=%d", profile.Name, len(activeRetrievers), perTimeout.Milliseconds())
	if pc.EnableHybrid {
        for _, sq := range expandQuery(profile, subQueries) {
            qctx, cancel := context.WithTimeout(ctx, perTimeout)
			var wg sync.WaitGroup
			resCh := make(chan []schema.SearchResult, len(activeRetrievers))
			for _, r := range activeRetrievers {
				rr := r
				wg.Add(1)
				go func(topK int) {
					defer wg.Done()
					// cap per-retriever topK when configured
					effK := topK
					if profile.PerRetrieverTopK > 0 && profile.PerRetrieverTopK < effK { effK = profile.PerRetrieverTopK }
					start := time.Now()
					res, err := rr.Search(qctx, sq, effK)
					if err == nil && len(res) > 0 {
						metrics.ObserveRetriever(rr.Type(), start, len(res))
						resCh <- res
					} else if err == nil {
						metrics.ObserveRetriever(rr.Type(), start, 0)
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
	metrics.ObserveFusion(len(lists))
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
		metrics.IncCRAGVerdict(verdictToLabel(verdict))
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

func profileByName(cfg *config.Config, name string) config.RetrievalProfile {
	if cfg == nil || cfg.Pipeline == nil {
		return config.RetrievalProfile{Name: "default"}
	}
	for i := range cfg.Pipeline.RetrievalProfiles {
		if strings.EqualFold(cfg.Pipeline.RetrievalProfiles[i].Name, name) {
			return cfg.Pipeline.RetrievalProfiles[i]
		}
	}
	return config.RetrievalProfile{Name: name}
}

func minInt(a, b int) int {
    if a < b { return a }
    return b
}

func verdictToLabel(v crag.Verdict) string {
    switch v {
    case crag.VerdictCorrect:
        return "correct"
    case crag.VerdictIncorrect:
        return "incorrect"
    case crag.VerdictAmbiguous:
        return "ambiguous"
    default:
        return "unknown"
    }
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
