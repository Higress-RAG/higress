package fusion

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/schema"
)

// RRFStrategy implements Reciprocal Rank Fusion.
type RRFStrategy struct {
	K int
}

// NewRRFStrategy creates a new RRF fusion strategy.
func NewRRFStrategy(k int) *RRFStrategy {
	if k <= 0 {
		k = 60
	}
	return &RRFStrategy{K: k}
}

// Fuse merges retriever results using reciprocal rank fusion.
func (s *RRFStrategy) Fuse(ctx context.Context, inputs []RetrieverResult, params map[string]any) ([]schema.SearchResult, error) {
	k := s.K
	if v := lookupInt(params, "k"); v > 0 {
		k = v
	}

	lists := make([][]schema.SearchResult, 0, len(inputs))
	for _, in := range inputs {
		if len(in.Results) == 0 {
			continue
		}
		lists = append(lists, in.Results)
	}

	return RRFScore(lists, k), nil
}

// Name implements Strategy.
func (s *RRFStrategy) Name() string { return "rrf" }

// WeightedStrategy implements weighted score fusion.
type WeightedStrategy struct {
	Weights map[string]float64 // weight keyed by retriever identifier
}

// NewWeightedStrategy creates a new weighted fusion strategy.
func NewWeightedStrategy(weights map[string]float64) *WeightedStrategy {
	if weights == nil {
		weights = make(map[string]float64)
	}
	return &WeightedStrategy{Weights: weights}
}

// Fuse merges retriever results using configured weights.
func (s *WeightedStrategy) Fuse(ctx context.Context, inputs []RetrieverResult, params map[string]any) ([]schema.SearchResult, error) {
	if len(inputs) == 0 {
		return []schema.SearchResult{}, nil
	}

	weights := copyStringFloatMap(s.Weights)
	if paramWeights, ok := parseStringFloatMap(params["weights"]); ok {
		for k, v := range paramWeights {
			weights[k] = v
		}
	}

	type agg struct {
		doc   schema.Document
		score float64
		count int
	}
	scores := make(map[string]*agg, len(inputs)*8)

	for idx, in := range inputs {
		if len(in.Results) == 0 {
			continue
		}

		weight := 1.0
		if w, ok := weights[in.Retriever]; ok {
			weight = w
		} else if key := compoundKey(in.Retriever, in.Provider); key != "" {
			if w, ok := weights[key]; ok {
				weight = w
			}
		} else if w, ok := weights[indexKey(idx)]; ok {
			weight = w
		}

		for _, item := range in.Results {
			doc := item.Document
			if doc.Metadata == nil {
				doc.Metadata = make(map[string]interface{})
			}
			doc.Metadata["retriever_type"] = in.Retriever
			if in.Provider != "" {
				doc.Metadata["retriever_provider"] = in.Provider
			}

			id := doc.ID
			if id == "" {
				// Guard against empty document IDs.
				continue
			}

			entry, ok := scores[id]
			if !ok {
				entry = &agg{doc: doc}
				scores[id] = entry
			}
			entry.score += item.Score * weight
			entry.count++
		}
	}

	out := make([]schema.SearchResult, 0, len(scores))
	for _, v := range scores {
		score := v.score
		if v.count > 0 {
			score = score / float64(v.count)
		}
		out = append(out, schema.SearchResult{
			Document: v.doc,
			Score:    score,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out, nil
}

// Name implements Strategy.
func (s *WeightedStrategy) Name() string { return "weighted" }

// LinearCombinationStrategy merges scores with a fixed weight vector.
type LinearCombinationStrategy struct {
	Weights []float64
}

// NewLinearCombinationStrategy creates a new linear strategy.
func NewLinearCombinationStrategy(weights []float64) *LinearCombinationStrategy {
	if len(weights) == 0 {
		weights = []float64{1.0}
	}
	return &LinearCombinationStrategy{Weights: weights}
}

// Fuse merges inputs using linear combination (by input index).
func (s *LinearCombinationStrategy) Fuse(ctx context.Context, inputs []RetrieverResult, params map[string]any) ([]schema.SearchResult, error) {
	if len(inputs) == 0 {
		return []schema.SearchResult{}, nil
	}

	weights := copyFloatSlice(s.Weights)
	if override, ok := parseFloatSlice(params["weights"]); ok && len(override) > 0 {
		weights = override
	}

	total := 0.0
	for _, w := range weights {
		total += w
	}
	if total == 0 {
		total = 1
	}

	type agg struct {
		doc   schema.Document
		score float64
	}
	scores := make(map[string]*agg, len(inputs)*8)

	for idx, in := range inputs {
		if len(in.Results) == 0 {
			continue
		}
		weight := 1.0
		if idx < len(weights) {
			weight = weights[idx]
		}
		weight = weight / total

		for _, item := range in.Results {
			doc := item.Document
			if doc.Metadata == nil {
				doc.Metadata = make(map[string]interface{})
			}
			doc.Metadata["retriever_type"] = in.Retriever
			id := doc.ID
			if id == "" {
				continue
			}
			entry, ok := scores[id]
			if !ok {
				entry = &agg{doc: doc}
				scores[id] = entry
			}
			entry.score += item.Score * weight
		}
	}

	out := make([]schema.SearchResult, 0, len(scores))
	for _, v := range scores {
		out = append(out, schema.SearchResult{Document: v.doc, Score: v.score})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out, nil
}

// Name implements Strategy.
func (s *LinearCombinationStrategy) Name() string { return "linear" }

// DistributionBasedStrategy normalizes individual result lists before delegating to a base strategy.
type DistributionBasedStrategy struct {
	Base Strategy
}

// NewDistributionBasedStrategy wraps another strategy with score normalization.
func NewDistributionBasedStrategy(base Strategy) *DistributionBasedStrategy {
	if base == nil {
		base = NewRRFStrategy(60)
	}
	return &DistributionBasedStrategy{Base: base}
}

// Fuse normalizes scores per input then forwards to the base strategy.
func (s *DistributionBasedStrategy) Fuse(ctx context.Context, inputs []RetrieverResult, params map[string]any) ([]schema.SearchResult, error) {
	if len(inputs) == 0 {
		return []schema.SearchResult{}, nil
	}

	normalized := make([]RetrieverResult, 0, len(inputs))
	for _, in := range inputs {
		if len(in.Results) == 0 {
			continue
		}
		minScore := in.Results[0].Score
		maxScore := in.Results[0].Score
		for _, item := range in.Results {
			if item.Score < minScore {
				minScore = item.Score
			}
			if item.Score > maxScore {
				maxScore = item.Score
			}
		}
		rng := maxScore - minScore
		norm := make([]schema.SearchResult, len(in.Results))
		for idx, item := range in.Results {
			copyItem := item
			if rng > 0 {
				copyItem.Score = (item.Score - minScore) / rng
			} else {
				copyItem.Score = 1.0
			}
			norm[idx] = copyItem
		}
		normalized = append(normalized, RetrieverResult{
			Query:      in.Query,
			Retriever:  in.Retriever,
			Provider:   in.Provider,
			Results:    norm,
			Attributes: in.Attributes,
		})
	}

	base := s.Base
	if base == nil {
		base = NewRRFStrategy(60)
	}
	return base.Fuse(ctx, normalized, params)
}

// Name implements Strategy.
func (s *DistributionBasedStrategy) Name() string {
	if s.Base == nil {
		return "distribution_rrf"
	}
	return "distribution_" + s.Base.Name()
}

// Helper functions -----------------------------------------------------------

func lookupInt(params map[string]any, key string) int {
	if params == nil {
		return 0
	}
	switch v := params[key].(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	case string:
		if v == "" {
			return 0
		}
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 0
}

func parseStringFloatMap(v interface{}) (map[string]float64, bool) {
	if v == nil {
		return nil, false
	}
	result := make(map[string]float64)
	switch typed := v.(type) {
	case map[string]float64:
		for k, val := range typed {
			result[k] = val
		}
	case map[string]interface{}:
		for k, val := range typed {
			switch num := val.(type) {
			case float64:
				result[k] = num
			case float32:
				result[k] = float64(num)
			case int:
				result[k] = float64(num)
			case int64:
				result[k] = float64(num)
			case string:
				if parsed, err := strconv.ParseFloat(num, 64); err == nil {
					result[k] = parsed
				}
			}
		}
	default:
		return nil, false
	}
	return result, true
}

func parseFloatSlice(v interface{}) ([]float64, bool) {
	if v == nil {
		return nil, false
	}
	switch typed := v.(type) {
	case []float64:
		out := make([]float64, len(typed))
		copy(out, typed)
		return out, true
	case []interface{}:
		out := make([]float64, 0, len(typed))
		for _, item := range typed {
			switch num := item.(type) {
			case float64:
				out = append(out, num)
			case float32:
				out = append(out, float64(num))
			case int:
				out = append(out, float64(num))
			case int64:
				out = append(out, float64(num))
			case string:
				if parsed, err := strconv.ParseFloat(num, 64); err == nil {
					out = append(out, parsed)
				}
			}
		}
		return out, true
	default:
		return nil, false
	}
}

func copyStringFloatMap(src map[string]float64) map[string]float64 {
	if len(src) == 0 {
		return make(map[string]float64)
	}
	dst := make(map[string]float64, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func copyFloatSlice(src []float64) []float64 {
	if len(src) == 0 {
		return nil
	}
	dst := make([]float64, len(src))
	copy(dst, src)
	return dst
}

func compoundKey(parts ...string) string {
	buf := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			buf = append(buf, part)
		}
	}
	if len(buf) == 0 {
		return ""
	}
	return strings.Join(buf, ":")
}

func indexKey(idx int) string {
	return fmt.Sprintf("list_%d", idx)
}
