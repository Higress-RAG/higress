package retriever

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "net/url"

    "github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/common/httpx"
    "github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/schema"
)

// WebSearchRetriever calls a web search API (e.g., Bing v7).
// Endpoint example: https://api.bing.microsoft.com/v7.0/search
type WebSearchRetriever struct {
    Provider string
    Endpoint string
    APIKey   string
    Client   *httpx.Client
}

func (r *WebSearchRetriever) Type() string { return "web" }

type bingResponse struct {
    WebPages struct {
        Value []struct {
            Name    string `json:"name"`
            URL     string `json:"url"`
            Snippet string `json:"snippet"`
        } `json:"value"`
    } `json:"webPages"`
}

func (r *WebSearchRetriever) Search(ctx context.Context, query string, topK int) ([]schema.SearchResult, error) {
    if r.Endpoint == "" || r.APIKey == "" { return []schema.SearchResult{}, nil }
    if topK <= 0 { topK = 10 }
    u, err := url.Parse(r.Endpoint)
    if err != nil { return nil, err }
    q := u.Query()
    q.Set("q", query)
    q.Set("count", fmt.Sprintf("%d", topK))
    u.RawQuery = q.Encode()
    req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
    if err != nil { return nil, err }
    // Bing API key header
    req.Header.Set("Ocp-Apim-Subscription-Key", r.APIKey)
    if r.Client == nil { return []schema.SearchResult{}, fmt.Errorf("web http client not configured") }
    resp, err := r.Client.Do(req)
    if err != nil { return nil, err }
    defer resp.Body.Close()
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        return nil, fmt.Errorf("web search http status %d", resp.StatusCode)
    }
    var br bingResponse
    if err := json.NewDecoder(resp.Body).Decode(&br); err != nil {
        return nil, err
    }
    out := make([]schema.SearchResult, 0, len(br.WebPages.Value))
    for _, v := range br.WebPages.Value {
        doc := schema.Document{ID: v.URL, Content: v.Snippet, Metadata: map[string]interface{}{"title": v.Name, "url": v.URL}}
        out = append(out, schema.SearchResult{Document: doc, Score: 0})
    }
    return out, nil
}
