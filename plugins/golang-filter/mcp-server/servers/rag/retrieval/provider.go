package retrieval

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/config"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/fusion"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/metrics"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/retriever"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/schema"
	"github.com/envoyproxy/envoy/contrib/golang/common/go/api"
)

// Provider handles retrieval orchestration
type Provider interface {
	Retrieve(ctx context.Context, queries []string, profile config.RetrievalProfile, m *metrics.RetrievalMetrics) []schema.SearchResult
	SetFusionStrategy(strategy fusion.Strategy)
}

// defaultProvider is the default implementation
type defaultProvider struct {
	retrievers     []retriever.Retriever
	retrieverMap   map[string]retriever.Retriever
	rrfK           int
	fusionStrategy fusion.Strategy
}

// NewProvider creates a new retrieval provider
func NewProvider(retrievers []retriever.Retriever, retrieverMap map[string]retriever.Retriever, rrfK int) Provider {
	return &defaultProvider{
		retrievers:     retrievers,
		retrieverMap:   retrieverMap,
		rrfK:           rrfK,
		fusionStrategy: fusion.NewRRFStrategy(rrfK), // Default to RRF
	}
}

// SetFusionStrategy sets the fusion strategy
func (p *defaultProvider) SetFusionStrategy(strategy fusion.Strategy) {
	if strategy != nil {
		p.fusionStrategy = strategy
	}
}

// Retrieve performs hybrid retrieval across multiple retrievers
func (p *defaultProvider) Retrieve(ctx context.Context, queries []string, profile config.RetrievalProfile, m *metrics.RetrievalMetrics) []schema.SearchResult {
	if len(p.retrievers) == 0 {
		api.LogWarn("retrieval: no retrievers available")
		return []schema.SearchResult{}
	}

	// Select active retrievers based on profile
	activeRetrievers := p.selectRetrievers(profile)
	if len(activeRetrievers) == 0 {
		api.LogWarn("retrieval: no active retrievers for profile")
		return []schema.SearchResult{}
	}

	// Record retriever types
	if m != nil {
		retrieverTypes := make([]string, len(activeRetrievers))
		for i, r := range activeRetrievers {
			retrieverTypes[i] = r.Type()
		}
		m.RetrieversUsed = retrieverTypes
	}

	// Parallel retrieval
	results := p.parallelRetrieve(ctx, queries, activeRetrievers, profile, m)

	// Fusion
	fused := p.fuse(results, profile, m)

	api.LogInfof("retrieval: total_results=%d fused=%d", len(results), len(fused))
	return fused
}

// selectRetrievers selects active retrievers based on profile
func (p *defaultProvider) selectRetrievers(profile config.RetrievalProfile) []retriever.Retriever {
	if len(profile.Retrievers) == 0 {
		// No specific retrievers configured, use all
		return p.retrievers
	}

	selected := make([]retriever.Retriever, 0, len(profile.Retrievers))
	for _, key := range profile.Retrievers {
		if r := p.findRetriever(key); r != nil {
			selected = append(selected, r)
		}
	}

	return selected
}

// findRetriever finds a retriever by key (type or type:provider or name)
func (p *defaultProvider) findRetriever(key string) retriever.Retriever {
	keyLower := strings.ToLower(strings.TrimSpace(key))

	// Direct match
	if r, ok := p.retrieverMap[keyLower]; ok {
		return r
	}

	// Type match
	for _, r := range p.retrievers {
		if strings.ToLower(r.Type()) == keyLower {
			return r
		}
	}

	return nil
}

// parallelRetrieve performs parallel retrieval across all queries and retrievers
func (p *defaultProvider) parallelRetrieve(
	ctx context.Context,
	queries []string,
	retrievers []retriever.Retriever,
	profile config.RetrievalProfile,
	m *metrics.RetrievalMetrics,
) []schema.SearchResult {
	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		allDocs []schema.SearchResult
	)

	// Control fan-out if MaxFanout is set
	fanout := len(queries) * len(retrievers)
	if profile.MaxFanout > 0 && fanout > profile.MaxFanout {
		// Limit concurrent operations (simple approach: limit queries)
		maxQueries := profile.MaxFanout / len(retrievers)
		if maxQueries < 1 {
			maxQueries = 1
		}
		if len(queries) > maxQueries {
			queries = queries[:maxQueries]
			api.LogInfof("retrieval: limited queries to %d (max_fanout=%d)", maxQueries, profile.MaxFanout)
		}
	}

	// Per-retriever TopK
	perRetrieverK := profile.PerRetrieverTopK
	if perRetrieverK == 0 {
		perRetrieverK = profile.TopK
	}

	for _, q := range queries {
		for _, ret := range retrievers {
			wg.Add(1)
			go func(query string, r retriever.Retriever) {
				defer wg.Done()

				start := time.Now()
				docs, err := r.Search(ctx, query, perRetrieverK)
				latency := time.Since(start).Milliseconds()

				if err != nil {
					api.LogWarnf("retrieval: %s search failed for query %q: %v", r.Type(), query, err)
					return
				}

				// Record metrics
				if m != nil {
					var avgScore, topScore float64
					if len(docs) > 0 {
						topScore = docs[0].Score
						sum := 0.0
						for _, d := range docs {
							sum += d.Score
						}
						avgScore = sum / float64(len(docs))
					}

					mu.Lock()
					m.AddRetrieverStats(metrics.RetrieverStats{
						Type:        r.Type(),
						LatencyMs:   latency,
						ResultCount: len(docs),
						AvgScore:    avgScore,
						TopScore:    topScore,
					})
					mu.Unlock()
				}

				mu.Lock()
				allDocs = append(allDocs, docs...)
				mu.Unlock()

				api.LogInfof("retrieval: %s returned %d docs in %dms for query %q",
					r.Type(), len(docs), latency, query)
			}(q, ret)
		}
	}

	wg.Wait()

	if m != nil {
		m.TotalRetrieved = len(allDocs)
	}

	return allDocs
}

// fuse merges results using configured fusion strategy
func (p *defaultProvider) fuse(results []schema.SearchResult, profile config.RetrievalProfile, m *metrics.RetrievalMetrics) []schema.SearchResult {
	if len(results) == 0 {
		return results
	}

	start := time.Now()

	// Use configured fusion strategy
	// Group all results into a single list (simple approach for single query set)
	resultLists := [][]schema.SearchResult{results}
	fused := p.fusionStrategy.Fuse(resultLists)

	// Apply threshold
	if profile.Threshold > 0 {
		filtered := make([]schema.SearchResult, 0, len(fused))
		for _, doc := range fused {
			if doc.Score >= profile.Threshold {
				filtered = append(filtered, doc)
			}
		}
		fused = filtered
	}

	// Apply TopK
	if len(fused) > profile.TopK {
		fused = fused[:profile.TopK]
	}

	if m != nil {
		m.FusionMethod = p.fusionStrategy.Name()
		m.FusionResultCount = len(fused)
		m.FusionLatencyMs = time.Since(start).Milliseconds()
	}

	return fused
}
