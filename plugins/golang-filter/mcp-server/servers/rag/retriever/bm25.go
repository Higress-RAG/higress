package retriever

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "net/url"
    "path"

    "github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/common/httpx"
    "github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/schema"
)

// BM25Retriever queries an Elasticsearch-like backend using a simple multi_match.
// Endpoint example: http://es:9200
// Index example: rag_bm25
type BM25Retriever struct {
    Endpoint string
    Index    string
    Client   *httpx.Client
    MaxTopK  int
}

func (r *BM25Retriever) Type() string { return "bm25" }

type esSearchRequest struct {
    Size  int                    `json:"size"`
    Query map[string]interface{} `json:"query"`
}

type esHit struct {
    ID     string                 `json:"_id"`
    Score  float64                `json:"_score"`
    Source map[string]interface{} `json:"_source"`
}
type esHits struct {
    Hits []esHit `json:"hits"`
}
type esSearchResponse struct {
    Hits esHits `json:"hits"`
}

func (r *BM25Retriever) Search(ctx context.Context, query string, topK int) ([]schema.SearchResult, error) {
    if r.Endpoint == "" || r.Index == "" {
        return []schema.SearchResult{}, nil
    }
    if topK <= 0 { topK = 10 }
    if r.MaxTopK > 0 && r.MaxTopK < topK { topK = r.MaxTopK }
    q := esSearchRequest{
        Size: topK,
        Query: map[string]interface{}{
            "multi_match": map[string]interface{}{
                "query":  query,
                "fields": []string{"content^2", "title", "metadata.*"},
            },
        },
    }
    bs, _ := json.Marshal(q)
    // Build URL: {endpoint}/{index}/_search
    u, err := url.Parse(r.Endpoint)
    if err != nil { return nil, err }
    u.Path = path.Join(u.Path, r.Index, "_search")
    req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(bs))
    if err != nil { return nil, err }
    req.Header.Set("Content-Type", "application/json")
    if r.Client == nil {
        return nil, fmt.Errorf("bm25 http client not configured")
    }
    resp, err := r.Client.Do(req)
    if err != nil { return nil, err }
    defer resp.Body.Close()
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        return nil, fmt.Errorf("bm25 http status %d", resp.StatusCode)
    }
    var esr esSearchResponse
    if err := json.NewDecoder(resp.Body).Decode(&esr); err != nil {
        return nil, err
    }
    out := make([]schema.SearchResult, 0, len(esr.Hits.Hits))
    for _, h := range esr.Hits.Hits {
        content := ""
        if v, ok := h.Source["content"].(string); ok { content = v }
        // fallback: if no content, try title or any other field
        if content == "" {
            if v, ok := h.Source["title"].(string); ok { content = v }
        }
        doc := schema.Document{ID: h.ID, Content: content, Metadata: h.Source}
        out = append(out, schema.SearchResult{Document: doc, Score: h.Score})
    }
    return out, nil
}

// ClientHTTP unwraps httpx.Client to stdlib http.Client via Do
func (r *BM25Retriever) ClientHTTP() *http.Client {
    // adapter for httpx.Client.Do
    return &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) { return r.Client.Do(req) })}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
