package post

import (
    "bytes"
    "context"
    "encoding/json"
    "net/http"
    "sort"

    "github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/common/httpx"
    "github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/schema"
)

// Reranker reorders candidates, typically using an external cross-encoder service.
type Reranker interface {
    Rerank(ctx context.Context, query string, in []schema.SearchResult, topN int) ([]schema.SearchResult, error)
}

// HTTPReranker posts a JSON payload to an external service for reranking.
// Expected request body:
// {"query":"...","candidates":[{"id":"","text":"..."}],"top_n":100}
// Expected response body:
// {"ranking":[{"id":"","score":0.9}]}
type HTTPReranker struct {
    Endpoint string
    Client   *httpx.Client
}

type rerankReq struct {
    Query      string            `json:"query"`
    Candidates []rerankCandidate `json:"candidates"`
    TopN       int               `json:"top_n,omitempty"`
}
type rerankCandidate struct {
    ID   string `json:"id"`
    Text string `json:"text"`
}
type rerankResp struct {
    Ranking []struct {
        ID    string  `json:"id"`
        Score float64 `json:"score"`
    } `json:"ranking"`
}

func (h *HTTPReranker) Rerank(ctx context.Context, query string, in []schema.SearchResult, topN int) ([]schema.SearchResult, error) {
    if h.Endpoint == "" {
        if topN > 0 && len(in) > topN { return append([]schema.SearchResult(nil), in[:topN]...), nil }
        return in, nil
    }
    req := rerankReq{Query: query, TopN: topN}
    idx := map[string]int{}
    req.Candidates = make([]rerankCandidate, 0, len(in))
    for i, c := range in {
        idx[c.Document.ID] = i
        req.Candidates = append(req.Candidates, rerankCandidate{ID: c.Document.ID, Text: c.Document.Content})
    }
    bs, _ := json.Marshal(req)
    httpReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, h.Endpoint, bytes.NewReader(bs))
    httpReq.Header.Set("Content-Type", "application/json")
    if h.Client == nil { return in, nil }
    resp, err := h.Client.Do(httpReq)
    if err != nil {
        // Passthrough on failure
        if topN > 0 && len(in) > topN { return append([]schema.SearchResult(nil), in[:topN]...), nil }
        return in, nil
    }
    defer resp.Body.Close()
    var rr rerankResp
    if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil || len(rr.Ranking) == 0 {
        if topN > 0 && len(in) > topN { return append([]schema.SearchResult(nil), in[:topN]...), nil }
        return in, nil
    }
    // Build a new ordered list based on ranking ids
    out := make([]schema.SearchResult, 0, len(rr.Ranking))
    for _, r := range rr.Ranking {
        if i, ok := idx[r.ID]; ok {
            c := in[i]
            c.Score = r.Score
            out = append(out, c)
        }
    }
    if topN > 0 && len(out) > topN { out = out[:topN] }
    // Stable sort by score desc
    sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
    return out, nil
}

func NewHTTPReranker(endpoint string) *HTTPReranker { return &HTTPReranker{Endpoint: endpoint} }
