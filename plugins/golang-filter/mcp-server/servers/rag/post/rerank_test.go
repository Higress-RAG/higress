package post

import (
    "encoding/json"
    "context"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/schema"
)

func TestHTTPReranker_Rerank(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        var req struct{
            Query string `json:"query"`
            Candidates []struct{ID, Text string} `json:"candidates"`
            TopN int `json:"top_n"`
        }
        _ = json.NewDecoder(r.Body).Decode(&req)
        // return reversed order with incremental scores
        type item struct{ ID string `json:"id"`; Score float64 `json:"score"` }
        out := struct{ Ranking []item `json:"ranking"` }{}
        for i := len(req.Candidates)-1; i>=0; i-- { out.Ranking = append(out.Ranking, item{ID: req.Candidates[i].ID, Score: float64(i+1)}) }
        _ = json.NewEncoder(w).Encode(out)
    }))
    defer srv.Close()

    rr := &HTTPReranker{Endpoint: srv.URL}
    in := []schema.SearchResult{{Document: schema.Document{ID: "a", Content: "x"}}, {Document: schema.Document{ID: "b", Content: "y"}}}
    out, err := rr.Rerank(context.Background(), "q", in, 0)
    if err != nil { t.Fatalf("rerank error: %v", err) }
    if len(out) != 2 || out[0].Document.ID != "b" { t.Fatalf("unexpected order: %+v", out) }
}
