package retriever

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/common/httpx"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/schema"
)

// PathRetriever queries an Elasticsearch-like backend using BM25 on path/knowledge path fields.
// It's similar to BM25Retriever but focuses on path-based retrieval for hierarchical document structures.
// Endpoint example: http://es:9200
// Index example: rag_bm25
type PathRetriever struct {
	Endpoint string
	Index    string
	Client   *httpx.Client
	MaxTopK  int
	// PathField specifies which metadata field to use for path retrieval
	// Common values: "file_path", "know_path", "path", "document_path"
	PathField string
}

func (r *PathRetriever) Type() string { return "path" }

type pathSearchRequest struct {
	Size  int                    `json:"size"`
	Query map[string]interface{} `json:"query"`
}

// Reuse esHit and esSearchResponse types from bm25.go for consistency
type pathHit struct {
	ID     string                 `json:"_id"`
	Score  float64                `json:"_score"`
	Source map[string]interface{} `json:"_source"`
}
type pathHits struct {
	Hits []pathHit `json:"hits"`
}
type pathSearchResponse struct {
	Hits pathHits `json:"hits"`
}

func (r *PathRetriever) Search(ctx context.Context, query string, topK int) ([]schema.SearchResult, error) {
	if r.Endpoint == "" || r.Index == "" {
		return []schema.SearchResult{}, nil
	}
	if topK <= 0 {
		topK = 10
	}
	if r.MaxTopK > 0 && r.MaxTopK < topK {
		topK = r.MaxTopK
	}

	// Determine path field to search
	pathField := r.PathField
	if pathField == "" {
		// Default to common path field names
		pathField = "know_path"
	}

	// Build query targeting path fields with higher weight
	q := pathSearchRequest{
		Size: topK,
		Query: map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []map[string]interface{}{
					{
						"match": map[string]interface{}{
							pathField: map[string]interface{}{
								"query": query,
								"boost": 2.0, // Higher weight for path field
							},
						},
					},
					{
						"match": map[string]interface{}{
							metadataField(pathField): map[string]interface{}{
								"query": query,
								"boost": 1.5,
							},
						},
					},
					// Fallback to content if path doesn't match
					{
						"match": map[string]interface{}{
							"content": map[string]interface{}{
								"query": query,
								"boost": 0.5,
							},
						},
					},
				},
				"minimum_should_match": 1,
			},
		},
	}

	bs, err := json.Marshal(q)
	if err != nil {
		return nil, fmt.Errorf("path retriever encode query: %w", err)
	}
	// Build URL: {endpoint}/{index}/_search
	u, err := url.Parse(r.Endpoint)
	if err != nil {
		return nil, err
	}
	u.Path = path.Join(u.Path, r.Index, "_search")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(bs))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if r.Client == nil {
		return nil, fmt.Errorf("path retriever http client not configured")
	}
	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("path retriever http status %d", resp.StatusCode)
	}

	var psr pathSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&psr); err != nil {
		return nil, err
	}

	out := make([]schema.SearchResult, 0, len(psr.Hits.Hits))
	for _, h := range psr.Hits.Hits {
		content := ""
		if v, ok := h.Source["content"].(string); ok {
			content = v
		}
		// fallback: if no content, try title or any other field
		if content == "" {
			if v, ok := h.Source["title"].(string); ok {
				content = v
			}
		}
		doc := schema.Document{ID: h.ID, Content: content, Metadata: h.Source}
		out = append(out, schema.SearchResult{Document: doc, Score: h.Score})
	}
	return out, nil
}

func metadataField(field string) string {
	field = strings.TrimSpace(field)
	if field == "" {
		return "metadata.know_path"
	}
	if strings.HasPrefix(field, "metadata.") {
		return field
	}
	return "metadata." + strings.TrimPrefix(field, "metadata.")
}

// ClientHTTP unwraps httpx.Client to stdlib http.Client via Do
func (r *PathRetriever) ClientHTTP() *http.Client {
	return &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return r.Client.Do(req)
	})}
}
