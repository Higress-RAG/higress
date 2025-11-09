package crag

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/common/httpx"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/schema"
)

// WebSearcher performs web searches to retrieve external knowledge.
type WebSearcher struct {
	Provider string // e.g., "duckduckgo", "bing", "google"
	Endpoint string
	APIKey   string
	Client   *httpx.Client
}

// SearchResult represents a single web search result with title, URL, and snippet.
type WebSearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// Search performs a web search and returns results as schema.SearchResult slice.
func (w *WebSearcher) Search(ctx context.Context, query string, numResults int) ([]schema.SearchResult, error) {
	if numResults <= 0 {
		numResults = 3
	}

	var results []WebSearchResult
	var err error

	switch w.Provider {
	case "duckduckgo":
		results, err = w.searchDuckDuckGo(ctx, query, numResults)
	case "bing":
		results, err = w.searchBing(ctx, query, numResults)
	default:
		// Fallback to DuckDuckGo
		logWarnf("WebSearcher: unknown provider %s, using DuckDuckGo", w.Provider)
		results, err = w.searchDuckDuckGo(ctx, query, numResults)
	}

	if err != nil {
		return nil, fmt.Errorf("web search failed: %w", err)
	}

	// Convert to schema.SearchResult
	out := make([]schema.SearchResult, 0, len(results))
	for _, r := range results {
		doc := schema.Document{
			ID:      r.URL,
			Content: r.Snippet,
			Metadata: map[string]interface{}{
				"title":  r.Title,
				"url":    r.URL,
				"source": "web_search",
			},
		}
		out = append(out, schema.SearchResult{Document: doc, Score: 0})
	}

	return out, nil
}

// searchDuckDuckGo performs a DuckDuckGo search using their Instant Answer API
func (w *WebSearcher) searchDuckDuckGo(ctx context.Context, query string, numResults int) ([]WebSearchResult, error) {
	// DuckDuckGo Instant Answer API (unofficial)
	endpoint := "https://api.duckduckgo.com/"
	if w.Endpoint != "" {
		endpoint = w.Endpoint
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}

	q := u.Query()
	q.Set("q", query)
	q.Set("format", "json")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	if w.Client == nil {
		w.Client = httpx.NewFromConfig(nil)
	}

	resp, err := w.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("duckduckgo api returned status %d", resp.StatusCode)
	}

	var ddgResp struct {
		AbstractText   string `json:"AbstractText"`
		AbstractSource string `json:"AbstractSource"`
		AbstractURL    string `json:"AbstractURL"`
		RelatedTopics  []struct {
			Text     string `json:"Text"`
			FirstURL string `json:"FirstURL"`
		} `json:"RelatedTopics"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&ddgResp); err != nil {
		return nil, err
	}

	results := make([]WebSearchResult, 0, numResults)

	// Add abstract if available
	if ddgResp.AbstractText != "" {
		results = append(results, WebSearchResult{
			Title:   ddgResp.AbstractSource,
			URL:     ddgResp.AbstractURL,
			Snippet: ddgResp.AbstractText,
		})
	}

	// Add related topics
	for _, topic := range ddgResp.RelatedTopics {
		if len(results) >= numResults {
			break
		}
		if topic.Text != "" && topic.FirstURL != "" {
			// Extract title from text (usually before " - ")
			title := topic.Text
			if len(title) > 100 {
				title = title[:100]
			}
			results = append(results, WebSearchResult{
				Title:   title,
				URL:     topic.FirstURL,
				Snippet: topic.Text,
			})
		}
	}

	logInfof("WebSearcher: DuckDuckGo returned %d results for query: %s", len(results), query)
	return results, nil
}

// searchBing performs a Bing Web Search using Bing Search API v7
func (w *WebSearcher) searchBing(ctx context.Context, query string, numResults int) ([]WebSearchResult, error) {
	if w.Endpoint == "" {
		return nil, fmt.Errorf("bing search requires endpoint configuration")
	}
	if w.APIKey == "" {
		return nil, fmt.Errorf("bing search requires api key")
	}

	u, err := url.Parse(w.Endpoint)
	if err != nil {
		return nil, err
	}

	q := u.Query()
	q.Set("q", query)
	q.Set("count", fmt.Sprintf("%d", numResults))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Ocp-Apim-Subscription-Key", w.APIKey)

	if w.Client == nil {
		w.Client = httpx.NewFromConfig(nil)
	}

	resp, err := w.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("bing api returned status %d", resp.StatusCode)
	}

	var bingResp struct {
		WebPages struct {
			Value []struct {
				Name    string `json:"name"`
				URL     string `json:"url"`
				Snippet string `json:"snippet"`
			} `json:"value"`
		} `json:"webPages"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&bingResp); err != nil {
		return nil, err
	}

	results := make([]WebSearchResult, 0, len(bingResp.WebPages.Value))
	for _, v := range bingResp.WebPages.Value {
		results = append(results, WebSearchResult{
			Title:   v.Name,
			URL:     v.URL,
			Snippet: v.Snippet,
		})
	}

	logInfof("WebSearcher: Bing returned %d results for query: %s", len(results), query)
	return results, nil
}
