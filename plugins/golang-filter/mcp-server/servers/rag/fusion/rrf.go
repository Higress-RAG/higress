package fusion

import (
    "sort"

    "github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/schema"
)

// RRFScore computes Reciprocal Rank Fusion score across multiple ranked lists.
func RRFScore(lists [][]schema.SearchResult, k int) []schema.SearchResult {
    if k <= 0 { k = 60 }
    // Accumulate scores by document ID
    type agg struct{ doc schema.Document; score float64 }
    scores := map[string]*agg{}

    for _, list := range lists {
        for idx, item := range list {
            id := item.Document.ID
            if id == "" {
                // Fallback to content hash key if needed; here we skip empty IDs.
                continue
            }
            if _, ok := scores[id]; !ok {
                scores[id] = &agg{doc: item.Document, score: 0}
            }
            // RRF: 1 / (k + rank)
            rank := float64(idx+1)
            scores[id].score += 1.0 / (float64(k) + rank)
        }
    }

    out := make([]schema.SearchResult, 0, len(scores))
    for _, v := range scores {
        out = append(out, schema.SearchResult{Document: v.doc, Score: v.score})
    }
    sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
    return out
}
