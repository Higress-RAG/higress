package fusion

import (
	"context"
	"sort"
	"strconv"

	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/schema"
)

// SimpleFusionStrategy implements a simple fusion method similar to EasyRAG's HybridRetriever.fusion.
// It merges results by document ID, keeping the highest score for each document,
// and optionally applies a topK limit.
type SimpleFusionStrategy struct {
	TopK int // If > 0, limits the number of results after fusion
}

// NewSimpleFusionStrategy creates a new simple fusion strategy.
func NewSimpleFusionStrategy(topK int) *SimpleFusionStrategy {
	return &SimpleFusionStrategy{TopK: topK}
}

// Fuse merges retriever results by keeping the highest score for each document ID.
// This is similar to EasyRAG's HybridRetriever.fusion() method.
func (s *SimpleFusionStrategy) Fuse(ctx context.Context, inputs []RetrieverResult, params map[string]any) ([]schema.SearchResult, error) {
	if len(inputs) == 0 {
		return []schema.SearchResult{}, nil
	}

	// Extract topK from params if provided
	topK := s.TopK
	if v := simpleLookupInt(params, "topk"); v > 0 {
		topK = v
	}
	if v := simpleLookupInt(params, "top_k"); v > 0 {
		topK = v
	}

	// Merge results by document ID, keeping the highest score
	scores := make(map[string]schema.SearchResult)
	for _, in := range inputs {
		if len(in.Results) == 0 {
			continue
		}
		for _, item := range in.Results {
			id := item.Document.ID
			if id == "" {
				// Skip documents without ID
				continue
			}

			// Ensure metadata carries retriever information
			if item.Document.Metadata == nil {
				item.Document.Metadata = make(map[string]interface{})
			}
			item.Document.Metadata["retriever_type"] = in.Retriever
			if in.Provider != "" {
				item.Document.Metadata["retriever_provider"] = in.Provider
			}

			existing, ok := scores[id]
			if !ok {
				// First occurrence of this document
				scores[id] = item
			} else {
				// Keep the document with the highest score
				if item.Score > existing.Score {
					scores[id] = item
				}
			}
		}
	}

	// Convert map to slice
	out := make([]schema.SearchResult, 0, len(scores))
	for _, result := range scores {
		out = append(out, result)
	}

	// Sort by score descending
	sort.Slice(out, func(i, j int) bool {
		return out[i].Score > out[j].Score
	})

	// Apply topK limit if specified
	if topK > 0 && len(out) > topK {
		out = out[:topK]
	}

	return out, nil
}

// Name implements Strategy.
func (s *SimpleFusionStrategy) Name() string { return "simple" }

// Fusion is a convenience function similar to EasyRAG's HybridRetriever.fusion().
// It merges multiple result lists by keeping the highest score for each document.
func Fusion(lists [][]schema.SearchResult) []schema.SearchResult {
	if len(lists) == 0 {
		return []schema.SearchResult{}
	}

	scores := make(map[string]schema.SearchResult)
	for _, list := range lists {
		for _, item := range list {
			id := item.Document.ID
			if id == "" {
				continue
			}

			existing, ok := scores[id]
			if !ok {
				scores[id] = item
			} else {
				if item.Score > existing.Score {
					scores[id] = item
				}
			}
		}
	}

	out := make([]schema.SearchResult, 0, len(scores))
	for _, result := range scores {
		out = append(out, result)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Score > out[j].Score
	})

	return out
}

// simpleLookupInt is a helper function to extract int from params.
func simpleLookupInt(params map[string]any, key string) int {
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
