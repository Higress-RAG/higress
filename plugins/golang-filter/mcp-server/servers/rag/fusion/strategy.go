package fusion

import (
	"sort"

	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/schema"
)

// Strategy defines the interface for fusion strategies
type Strategy interface {
	// Fuse merges multiple ranked lists into a single ranked list
	Fuse(lists [][]schema.SearchResult) []schema.SearchResult
	// Name returns the strategy name
	Name() string
}

// RRFStrategy implements Reciprocal Rank Fusion
type RRFStrategy struct {
	K int // RRF parameter (default: 60)
}

// NewRRFStrategy creates a new RRF fusion strategy
func NewRRFStrategy(k int) *RRFStrategy {
	if k <= 0 {
		k = 60
	}
	return &RRFStrategy{K: k}
}

// Fuse implements RRF fusion
func (s *RRFStrategy) Fuse(lists [][]schema.SearchResult) []schema.SearchResult {
	return RRFScore(lists, s.K)
}

// Name returns the strategy name
func (s *RRFStrategy) Name() string {
	return "rrf"
}

// WeightedStrategy implements weighted score fusion
type WeightedStrategy struct {
	Weights map[string]float64 // Weights per retriever type
}

// NewWeightedStrategy creates a new weighted fusion strategy
func NewWeightedStrategy(weights map[string]float64) *WeightedStrategy {
	if weights == nil {
		weights = make(map[string]float64)
	}
	return &WeightedStrategy{Weights: weights}
}

// Fuse implements weighted score fusion
func (s *WeightedStrategy) Fuse(lists [][]schema.SearchResult) []schema.SearchResult {
	if len(lists) == 0 {
		return []schema.SearchResult{}
	}

	// Accumulate weighted scores by document ID
	type agg struct {
		doc   schema.Document
		score float64
		count int
	}
	scores := map[string]*agg{}

	for listIdx, list := range lists {
		// Get weight for this list (default 1.0 if not specified)
		weight := 1.0
		if len(list) > 0 {
			// Try to get weight by retriever type if available in metadata
			if retrieverType, ok := list[0].Document.Metadata["retriever_type"].(string); ok {
				if w, exists := s.Weights[retrieverType]; exists {
					weight = w
				}
			}
		}
		// Fallback: use list index as identifier
		if weight == 1.0 && len(s.Weights) > 0 {
			if w, exists := s.Weights[string(rune(listIdx))]; exists {
				weight = w
			}
		}

		for _, item := range list {
			id := item.Document.ID
			if id == "" {
				continue
			}
			if _, ok := scores[id]; !ok {
				scores[id] = &agg{doc: item.Document, score: 0, count: 0}
			}
			scores[id].score += item.Score * weight
			scores[id].count++
		}
	}

	// Normalize scores by count (average)
	out := make([]schema.SearchResult, 0, len(scores))
	for _, v := range scores {
		avgScore := v.score / float64(v.count)
		out = append(out, schema.SearchResult{Document: v.doc, Score: avgScore})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// Name returns the strategy name
func (s *WeightedStrategy) Name() string {
	return "weighted"
}

// LinearCombinationStrategy implements linear combination of scores
type LinearCombinationStrategy struct {
	Weights []float64 // Weights for each list (in order)
}

// NewLinearCombinationStrategy creates a new linear combination strategy
func NewLinearCombinationStrategy(weights []float64) *LinearCombinationStrategy {
	if weights == nil || len(weights) == 0 {
		weights = []float64{1.0} // Default equal weight
	}
	return &LinearCombinationStrategy{Weights: weights}
}

// Fuse implements linear combination fusion
func (s *LinearCombinationStrategy) Fuse(lists [][]schema.SearchResult) []schema.SearchResult {
	if len(lists) == 0 {
		return []schema.SearchResult{}
	}

	// Normalize weights
	totalWeight := 0.0
	for _, w := range s.Weights {
		totalWeight += w
	}
	normalizedWeights := make([]float64, len(s.Weights))
	for i, w := range s.Weights {
		normalizedWeights[i] = w / totalWeight
	}

	// Accumulate weighted scores by document ID
	type agg struct {
		doc         schema.Document
		score       float64
		listPresent map[int]bool
	}
	scores := map[string]*agg{}

	for listIdx, list := range lists {
		weight := 1.0
		if listIdx < len(normalizedWeights) {
			weight = normalizedWeights[listIdx]
		}

		for _, item := range list {
			id := item.Document.ID
			if id == "" {
				continue
			}
			if _, ok := scores[id]; !ok {
				scores[id] = &agg{
					doc:         item.Document,
					score:       0,
					listPresent: make(map[int]bool),
				}
			}
			scores[id].score += item.Score * weight
			scores[id].listPresent[listIdx] = true
		}
	}

	// Convert to result list
	out := make([]schema.SearchResult, 0, len(scores))
	for _, v := range scores {
		out = append(out, schema.SearchResult{Document: v.doc, Score: v.score})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// Name returns the strategy name
func (s *LinearCombinationStrategy) Name() string {
	return "linear"
}

// DistributionBasedStrategy implements a score normalization before fusion
// Normalizes each list's scores to [0,1] range before combining
type DistributionBasedStrategy struct {
	BaseStrategy Strategy // Wrapped strategy to use after normalization
}

// NewDistributionBasedStrategy creates a new distribution-based strategy
func NewDistributionBasedStrategy(base Strategy) *DistributionBasedStrategy {
	if base == nil {
		base = NewRRFStrategy(60)
	}
	return &DistributionBasedStrategy{BaseStrategy: base}
}

// Fuse implements distribution-based fusion with normalization
func (s *DistributionBasedStrategy) Fuse(lists [][]schema.SearchResult) []schema.SearchResult {
	if len(lists) == 0 {
		return []schema.SearchResult{}
	}

	// Normalize each list's scores to [0, 1]
	normalizedLists := make([][]schema.SearchResult, len(lists))
	for i, list := range lists {
		if len(list) == 0 {
			normalizedLists[i] = list
			continue
		}

		// Find min and max scores
		minScore := list[0].Score
		maxScore := list[0].Score
		for _, item := range list {
			if item.Score < minScore {
				minScore = item.Score
			}
			if item.Score > maxScore {
				maxScore = item.Score
			}
		}

		// Normalize scores
		normalizedList := make([]schema.SearchResult, len(list))
		scoreRange := maxScore - minScore
		for j, item := range list {
			normalized := item
			if scoreRange > 0 {
				normalized.Score = (item.Score - minScore) / scoreRange
			} else {
				normalized.Score = 1.0 // All scores are the same
			}
			normalizedList[j] = normalized
		}
		normalizedLists[i] = normalizedList
	}

	// Apply base strategy on normalized lists
	return s.BaseStrategy.Fuse(normalizedLists)
}

// Name returns the strategy name
func (s *DistributionBasedStrategy) Name() string {
	return "distribution_based_" + s.BaseStrategy.Name()
}

// NewStrategy creates a fusion strategy by name
func NewStrategy(name string, params map[string]interface{}) Strategy {
	switch name {
	case "rrf":
		k := 60
		if kVal, ok := params["k"].(int); ok {
			k = kVal
		}
		return NewRRFStrategy(k)
	case "weighted":
		weights := make(map[string]float64)
		if w, ok := params["weights"].(map[string]float64); ok {
			weights = w
		}
		return NewWeightedStrategy(weights)
	case "linear":
		var weights []float64
		if w, ok := params["weights"].([]float64); ok {
			weights = w
		}
		return NewLinearCombinationStrategy(weights)
	case "distribution":
		// Distribution-based with default RRF
		var base Strategy = NewRRFStrategy(60)
		if baseName, ok := params["base"].(string); ok {
			base = NewStrategy(baseName, params)
		}
		return NewDistributionBasedStrategy(base)
	default:
		// Default to RRF
		return NewRRFStrategy(60)
	}
}

