package retrieval

import (
	"context"
	"sort"
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
	SetFusionStrategy(strategy fusion.Strategy, params map[string]any)
}

// defaultProvider is the default implementation
type defaultProvider struct {
	retrievers     []retriever.Retriever
	retrieverMap   map[string]retriever.Retriever
	rrfK           int
	fusionStrategy fusion.Strategy
	fusionParams   map[string]any
	hyde           *HYDEClient
}

// NewProvider creates a new retrieval provider
func NewProvider(retrievers []retriever.Retriever, retrieverMap map[string]retriever.Retriever, rrfK int) Provider {
	return &defaultProvider{
		retrievers:     retrievers,
		retrieverMap:   retrieverMap,
		rrfK:           rrfK,
		fusionStrategy: fusion.NewRRFStrategy(rrfK), // Default to RRF
		fusionParams: map[string]any{
			"k": rrfK,
		},
		hyde: NewHYDEClient(),
	}
}

// SetFusionStrategy sets the fusion strategy
func (p *defaultProvider) SetFusionStrategy(strategy fusion.Strategy, params map[string]any) {
	if strategy != nil {
		p.fusionStrategy = strategy
	}
	if params != nil {
		p.fusionParams = params
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

	// Retrieval path (cascade or parallel)
	var (
		inputs  []fusion.RetrieverResult
		results []schema.SearchResult
		ok      bool
	)
	if profile.Cascade.Enable {
		inputs, results, ok = p.runCascade(ctx, queries, profile, m)
	}
	if !ok {
		inputs, results = p.parallelRetrieve(ctx, queries, activeRetrievers, profile, m)
	}

	// Fusion
	fused := p.fuse(ctx, inputs, results, queries, profile, m)

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

// runCascade executes cascade retrieval if configured. Returns ok=false on fallback requirement.
func (p *defaultProvider) runCascade(
	ctx context.Context,
	queries []string,
	profile config.RetrievalProfile,
	m *metrics.RetrievalMetrics,
) ([]fusion.RetrieverResult, []schema.SearchResult, bool) {
	if len(queries) == 0 {
		return nil, nil, false
	}

	stage1Cfg := profile.Cascade.Stage1
	if stage1Cfg.Retriever == "" {
		api.LogWarn("retrieval: cascade enabled but stage1 retriever missing")
		return nil, nil, false
	}
	stage1 := p.findRetriever(stage1Cfg.Retriever)
	if stage1 == nil {
		api.LogWarnf("retrieval: cascade stage1 retriever %q not found", stage1Cfg.Retriever)
		return nil, nil, false
	}

	stage1TopK := stage1Cfg.TopK
	if stage1TopK <= 0 {
		stage1TopK = profile.TopK
	}
	if stage1TopK <= 0 {
		stage1TopK = 10
	}
	if budget, ok := p.variantTopK(profile, stage1); ok && budget > 0 {
		stage1TopK = budget
	}

	latencyBudget := profile.Cascade.LatencyBudgetMs
	if latencyBudget <= 0 && profile.LatencyBudgetMs > 0 {
		latencyBudget = profile.LatencyBudgetMs
	}
	budgetDuration := time.Duration(latencyBudget) * time.Millisecond
	begin := time.Now()

	seedQueries := make([]string, 0, 1)
	seedQueries = append(seedQueries, queries[0])
	if seeds := p.generateHYDESeeds(ctx, profile, queries[0]); len(seeds) > 0 {
		if m != nil {
			m.AddRetrievalPhase("hyde")
		}
		maxSeeds := profile.HYDE.MaxSeeds
		if maxSeeds <= 0 {
			maxSeeds = len(seeds)
		}
		if maxSeeds < len(seeds) {
			seeds = seeds[:maxSeeds]
		}
		seedQueries = append(seedQueries, seeds...)
	}

	if m != nil {
		m.AddRetrievalPhase("cascade_stage1")
	}

	stage1Map := make(map[string]schema.SearchResult)
	for _, q := range seedQueries {
		docs, latency, err := p.executeSearch(ctx, stage1, q, stage1TopK)
		if err != nil {
			api.LogWarnf("retrieval: cascade stage1 %s query %q failed: %v", stage1.Type(), q, err)
			continue
		}
		if m != nil {
			m.AddRetrieverStats(buildRetrieverStats(stage1, docs, latency))
		}
		for _, doc := range docs {
			id := doc.Document.ID
			if id == "" {
				continue
			}
			if doc.Document.Metadata == nil {
				doc.Document.Metadata = make(map[string]any)
			}
			doc.Document.Metadata["retriever_type"] = stage1.Type()
			doc.Document.Metadata["cascade_stage"] = "stage1"
			if existing, ok := stage1Map[id]; !ok || doc.Score > existing.Score {
				stage1Map[id] = doc
			}
		}
	}

	if len(stage1Map) == 0 {
		api.LogWarn("retrieval: cascade stage1 returned no documents")
		return nil, nil, false
	}

	stage1Results := mapToSortedSlice(stage1Map)

	elapsed := time.Since(begin)
	if budgetDuration > 0 && elapsed >= budgetDuration {
		api.LogWarnf("retrieval: cascade budget %.2fms exhausted after stage1", budgetDuration.Seconds()*1000)
		input := fusion.RetrieverResult{
			Query:      queries[0],
			Retriever:  stage1.Type(),
			Results:    stage1Results,
			Attributes: map[string]any{"cascade_stage": "stage1"},
		}
		return []fusion.RetrieverResult{input}, stage1Results, true
	}

	stage2Cfg := profile.Cascade.Stage2
	stage2 := p.findRetriever(stage2Cfg.Retriever)
	stage2Results := []schema.SearchResult{}
	if stage2 != nil {
		if m != nil {
			m.AddRetrievalPhase("cascade_stage2")
		}
		stage2TopK := stage2Cfg.TopK
		if stage2TopK <= 0 {
			stage2TopK = profile.TopK
		}
		if stage2TopK <= 0 {
			stage2TopK = len(stage1Results)
			if stage2TopK == 0 {
				stage2TopK = 10
			}
		}
		if budget, ok := p.variantTopK(profile, stage2); ok && budget > 0 {
			stage2TopK = budget
		}

		docs, latency, err := p.executeSearch(ctx, stage2, queries[0], stage2TopK)
		if err != nil {
			api.LogWarnf("retrieval: cascade stage2 %s failed: %v", stage2.Type(), err)
		} else {
			if m != nil {
				m.AddRetrieverStats(buildRetrieverStats(stage2, docs, latency))
			}
			mode := strings.ToLower(strings.TrimSpace(stage2Cfg.Mode))
			if mode == "" {
				mode = "rescore"
			}
			stage2Results = filterCascadeResults(docs, stage1Map, mode, stage2.Type())
		}
	}

	inputs := []fusion.RetrieverResult{
		{
			Query:      queries[0],
			Retriever:  stage1.Type(),
			Results:    stage1Results,
			Attributes: map[string]any{"cascade_stage": "stage1"},
		},
	}
	if stage2 != nil && len(stage2Results) > 0 {
		inputs = append(inputs, fusion.RetrieverResult{
			Query:      queries[0],
			Retriever:  stage2.Type(),
			Results:    stage2Results,
			Attributes: map[string]any{"cascade_stage": "stage2", "mode": strings.ToLower(stage2Cfg.Mode)},
		})
	}

	all := make([]schema.SearchResult, 0, len(stage1Results)+len(stage2Results))
	all = append(all, stage1Results...)
	all = append(all, stage2Results...)
	return inputs, all, true
}

// parallelRetrieve performs parallel retrieval across all queries and retrievers
func (p *defaultProvider) parallelRetrieve(
	ctx context.Context,
	queries []string,
	retrievers []retriever.Retriever,
	profile config.RetrievalProfile,
	m *metrics.RetrievalMetrics,
) ([]fusion.RetrieverResult, []schema.SearchResult) {
	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		allDocs []schema.SearchResult
		grouped = make(map[string]fusion.RetrieverResult)
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

				topK := perRetrieverK
				if budget, ok := p.variantTopK(profile, r); ok && budget > 0 {
					topK = budget
				}
				if topK <= 0 {
					topK = profile.TopK
					if topK <= 0 {
						topK = 10
					}
				}

				start := time.Now()
				docs, err := r.Search(ctx, query, topK)
				latency := time.Since(start).Milliseconds()

				if err != nil {
					api.LogWarnf("retrieval: %s search failed for query %q: %v", r.Type(), query, err)
					return
				}

				// Ensure metadata carries retriever hints for downstream fusion.
				for i := range docs {
					if docs[i].Document.Metadata == nil {
						docs[i].Document.Metadata = make(map[string]interface{})
					}
					docs[i].Document.Metadata["retriever_type"] = r.Type()
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
				key := r.Type()
				entry := grouped[key]
				if entry.Retriever == "" {
					entry.Retriever = r.Type()
					entry.Query = query
					entry.Attributes = map[string]any{}
				}
				entry.Results = append(entry.Results, docs...)
				grouped[key] = entry
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

	inputs := make([]fusion.RetrieverResult, 0, len(grouped))
	for _, item := range grouped {
		inputs = append(inputs, item)
	}

	return inputs, allDocs
}

// fuse merges results using configured fusion strategy
func (p *defaultProvider) fuse(
	ctx context.Context,
	inputs []fusion.RetrieverResult,
	raw []schema.SearchResult,
	queries []string,
	profile config.RetrievalProfile,
	m *metrics.RetrievalMetrics,
) []schema.SearchResult {
	if len(raw) == 0 {
		return raw
	}

	start := time.Now()

	params := make(map[string]any, len(p.fusionParams)+4)
	for k, v := range p.fusionParams {
		params[k] = v
	}
	params["profile_top_k"] = profile.TopK
	if len(queries) > 0 {
		params["query"] = queries[0]
		if _, exists := params["query_id"]; !exists {
			params["query_id"] = queries[0]
		}
	}

	strategy := p.fusionStrategy
	if strategy == nil {
		strategy = fusion.NewRRFStrategy(p.rrfK)
	}

	fused, err := strategy.Fuse(ctx, inputs, params)
	if err != nil {
		api.LogWarnf("retrieval: fusion strategy %s failed (%v), fallback to RRF", strategy.Name(), err)
		strategy = fusion.NewRRFStrategy(p.rrfK)
		fused, _ = strategy.Fuse(ctx, inputs, params)
	}
	latencyMs := time.Since(start).Milliseconds()

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
		weightsVersion := ""
		if provider, ok := strategy.(fusion.MetadataProvider); ok {
			meta := provider.Metadata()
			if version, ok := meta["weights_version"].(string); ok {
				weightsVersion = version
			}
		}
		m.RecordFusion(strategy.Name(), len(fused), 0, latencyMs, weightsVersion)
	}

	return fused
}

func (p *defaultProvider) executeSearch(ctx context.Context, r retriever.Retriever, query string, topK int) ([]schema.SearchResult, int64, error) {
	start := time.Now()
	docs, err := r.Search(ctx, query, topK)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return nil, latency, err
	}

	for i := range docs {
		if docs[i].Document.Metadata == nil {
			docs[i].Document.Metadata = make(map[string]any)
		}
		docs[i].Document.Metadata["retriever_type"] = r.Type()
	}
	return docs, latency, nil
}

func buildRetrieverStats(r retriever.Retriever, docs []schema.SearchResult, latency int64) metrics.RetrieverStats {
	var avgScore, topScore float64
	if len(docs) > 0 {
		topScore = docs[0].Score
		sum := 0.0
		for _, d := range docs {
			sum += d.Score
		}
		avgScore = sum / float64(len(docs))
	}
	return metrics.RetrieverStats{
		Type:        r.Type(),
		LatencyMs:   latency,
		ResultCount: len(docs),
		AvgScore:    avgScore,
		TopScore:    topScore,
	}
}

func mapToSortedSlice(m map[string]schema.SearchResult) []schema.SearchResult {
	out := make([]schema.SearchResult, 0, len(m))
	for _, doc := range m {
		out = append(out, doc)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Score > out[j].Score
	})
	return out
}

func filterCascadeResults(
	docs []schema.SearchResult,
	stage1 map[string]schema.SearchResult,
	mode string,
	retrieverType string,
) []schema.SearchResult {
	mode = strings.ToLower(mode)
	switch mode {
	case "refine":
		for i := range docs {
			if docs[i].Document.Metadata == nil {
				docs[i].Document.Metadata = make(map[string]any)
			}
			docs[i].Document.Metadata["retriever_type"] = retrieverType
			docs[i].Document.Metadata["cascade_stage"] = "stage2"
		}
		return docs
	default: // rescore by default
		filtered := make([]schema.SearchResult, 0, len(docs))
		for _, doc := range docs {
			id := doc.Document.ID
			if id == "" {
				continue
			}
			if _, ok := stage1[id]; !ok {
				continue
			}
			if doc.Document.Metadata == nil {
				doc.Document.Metadata = make(map[string]any)
			}
			doc.Document.Metadata["retriever_type"] = retrieverType
			doc.Document.Metadata["cascade_stage"] = "stage2"
			filtered = append(filtered, doc)
		}
		return filtered
	}
}

func (p *defaultProvider) generateHYDESeeds(ctx context.Context, profile config.RetrievalProfile, query string) []string {
	if p.hyde == nil || strings.TrimSpace(query) == "" {
		return nil
	}
	seeds, err := p.hyde.GenerateSeeds(ctx, profile.HYDE, query)
	if err != nil {
		api.LogWarnf("retrieval: hyde generation failed: %v", err)
		return nil
	}
	return seeds
}

func (p *defaultProvider) variantTopK(profile config.RetrievalProfile, r retriever.Retriever) (int, bool) {
	if len(profile.VariantBudgets) == 0 {
		return 0, false
	}
	key := variantKeyForRetriever(r)
	if key == "" {
		return 0, false
	}
	value, ok := profile.VariantBudgets[key]
	return value, ok
}

func variantKeyForRetriever(r retriever.Retriever) string {
	switch strings.ToLower(r.Type()) {
	case "vector":
		return "dense"
	case "bm25", "path":
		// Both BM25 and Path retrievers are sparse retrieval methods
		return "sparse"
	case "web":
		return "web"
	default:
		return strings.ToLower(strings.TrimSpace(r.Type()))
	}
}
