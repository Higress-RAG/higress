package post

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/common/httpx"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/llm"
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
		if topN > 0 && len(in) > topN {
			return append([]schema.SearchResult(nil), in[:topN]...), nil
		}
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
	if h.Client == nil {
		h.Client = httpx.NewFromConfig(nil)
	}
	resp, err := h.Client.Do(httpReq)
	if err != nil {
		// Passthrough on failure
		if topN > 0 && len(in) > topN {
			return append([]schema.SearchResult(nil), in[:topN]...), nil
		}
		return in, nil
	}
	defer resp.Body.Close()
	var rr rerankResp
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil || len(rr.Ranking) == 0 {
		if topN > 0 && len(in) > topN {
			return append([]schema.SearchResult(nil), in[:topN]...), nil
		}
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
	if topN > 0 && len(out) > topN {
		out = out[:topN]
	}
	// Stable sort by score desc
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out, nil
}

func NewHTTPReranker(endpoint string) *HTTPReranker { return &HTTPReranker{Endpoint: endpoint} }

// ================================================================================
// LLM-based Reranker
// ================================================================================

// LLMReranker uses an LLM to score and rerank documents based on relevance.
type LLMReranker struct {
	Provider llm.Provider
	Model    string // optional: specific model to use for reranking
}

const llmRerankSystemPrompt = `You are an expert at evaluating document relevance for search queries.
Your task is to rate documents on a scale from 0 to 10 based on how well they answer the given query.

Guidelines:
- Score 0-2: Document is completely irrelevant
- Score 3-5: Document has some relevant information but doesn't directly answer the query
- Score 6-8: Document is relevant and partially answers the query
- Score 9-10: Document is highly relevant and directly answers the query

You MUST respond with ONLY a single integer score between 0 and 10. Do not include ANY other text.`

func (l *LLMReranker) Rerank(ctx context.Context, query string, in []schema.SearchResult, topN int) ([]schema.SearchResult, error) {
	if l.Provider == nil {
		// Fallback: return top N by original scores
		if topN > 0 && len(in) > topN {
			return append([]schema.SearchResult(nil), in[:topN]...), nil
		}
		return in, nil
	}

	logInfof("LLMReranker: reranking %d documents...", len(in))

	scored := make([]schema.SearchResult, 0, len(in))

	for i, result := range in {
		// Progress logging every 5 documents
		if i%5 == 0 {
			logInfof("LLMReranker: scoring document %d/%d...", i+1, len(in))
		}

		// Build user prompt
		userPrompt := fmt.Sprintf(`Query: %s
Document:
%s

Rate this document's relevance to the query on a scale from 0 to 10:`, query, result.Document.Content)

		// Create full prompt with system message
		fullPrompt := fmt.Sprintf("%s\n\n%s", llmRerankSystemPrompt, userPrompt)

		// Get LLM response
		response, err := l.Provider.GenerateCompletion(ctx, fullPrompt)
		if err != nil {
			logWarnf("LLMReranker: failed to score document %d: %v, using original score", i, err)
			// Use original score scaled to 0-10
			result.Score = result.Score * 10
			scored = append(scored, result)
			continue
		}

		// Parse score from response
		scoreText := strings.TrimSpace(response)
		scoreRegex := regexp.MustCompile(`\b(10|[0-9])\b`)
		match := scoreRegex.FindStringSubmatch(scoreText)

		var score float64
		if match != nil {
			parsed, err := strconv.ParseFloat(match[1], 64)
			if err == nil {
				score = parsed
			} else {
				logWarnf("LLMReranker: failed to parse score from '%s', using original score", scoreText)
				score = result.Score * 10
			}
		} else {
			logWarnf("LLMReranker: could not extract score from response: '%s', using original score", scoreText)
			score = result.Score * 10
		}

		result.Score = score
		scored = append(scored, result)
	}

	// Sort by relevance score descending
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	// Return top N
	if topN > 0 && len(scored) > topN {
		scored = scored[:topN]
	}

	logInfof("LLMReranker: reranked to top %d documents", len(scored))
	return scored, nil
}

// ================================================================================
// Keyword-based Reranker
// ================================================================================

// KeywordReranker performs reranking based on keyword matching and positioning.
type KeywordReranker struct {
	MinKeywordLength int     // Minimum length for a word to be considered a keyword (default: 3)
	BaseScoreWeight  float64 // Weight for original similarity score (default: 0.5)
}

func (k *KeywordReranker) Rerank(ctx context.Context, query string, in []schema.SearchResult, topN int) ([]schema.SearchResult, error) {
	// Set defaults
	minLen := k.MinKeywordLength
	if minLen == 0 {
		minLen = 3
	}
	baseWeight := k.BaseScoreWeight
	if baseWeight == 0 {
		baseWeight = 0.5
	}

	logInfof("KeywordReranker: reranking %d documents based on keywords...", len(in))

	// Extract keywords from query (words longer than minLen)
	keywords := make([]string, 0)
	for _, word := range strings.Fields(query) {
		if len(word) > minLen {
			keywords = append(keywords, strings.ToLower(word))
		}
	}

	logInfof("KeywordReranker: extracted %d keywords: %v", len(keywords), keywords)

	scored := make([]schema.SearchResult, 0, len(in))

	for _, result := range in {
		documentText := strings.ToLower(result.Document.Content)

		// Base score from original similarity
		baseScore := result.Score * baseWeight

		// Keyword matching score
		keywordScore := 0.0

		for _, keyword := range keywords {
			if strings.Contains(documentText, keyword) {
				// Base keyword match: +0.1
				keywordScore += 0.1

				// Position bonus: if keyword appears in first quarter, add extra
				firstPosition := strings.Index(documentText, keyword)
				if firstPosition >= 0 && firstPosition < len(documentText)/4 {
					keywordScore += 0.1
				}

				// Frequency bonus: count occurrences
				frequency := float64(strings.Count(documentText, keyword))
				keywordScore += min(0.05*frequency, 0.2) // Max 0.2 from frequency
			}
		}

		// Combine base score and keyword score
		finalScore := baseScore + keywordScore

		result.Score = finalScore
		scored = append(scored, result)
	}

	// Sort by final score descending
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	// Return top N
	if topN > 0 && len(scored) > topN {
		scored = scored[:topN]
	}

	logInfof("KeywordReranker: reranked to top %d documents", len(scored))
	return scored, nil
}

// ================================================================================
// Model-based Reranker (Cross-encoder)
// ================================================================================

// ModelReranker uses a dedicated reranking model (e.g., BGE-reranker, Cohere rerank).
// It calls an external service that provides cross-encoder based reranking.
type ModelReranker struct {
	Endpoint string
	Model    string // e.g., "bge-reranker-large", "rerank-multilingual-v2.0"
	APIKey   string
	Client   *httpx.Client
}

type modelRerankReq struct {
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	Model     string   `json:"model,omitempty"`
	TopN      int      `json:"top_n,omitempty"`
}

type modelRerankResp struct {
	Results []struct {
		Index          int     `json:"index"`
		RelevanceScore float64 `json:"relevance_score"`
		Document       string  `json:"document,omitempty"`
	} `json:"results"`
}

func (m *ModelReranker) Rerank(ctx context.Context, query string, in []schema.SearchResult, topN int) ([]schema.SearchResult, error) {
	if m.Endpoint == "" {
		// Fallback: return top N by original scores
		if topN > 0 && len(in) > topN {
			return append([]schema.SearchResult(nil), in[:topN]...), nil
		}
		return in, nil
	}

	logInfof("ModelReranker: reranking %d documents using model %s...", len(in), m.Model)

	// Prepare documents for reranking
	documents := make([]string, len(in))
	for i, result := range in {
		documents[i] = result.Document.Content
	}

	// Build request
	reqBody := modelRerankReq{
		Query:     query,
		Documents: documents,
		Model:     m.Model,
		TopN:      topN,
	}

	bs, _ := json.Marshal(reqBody)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, m.Endpoint, bytes.NewReader(bs))
	if err != nil {
		logWarnf("ModelReranker: failed to create request: %v", err)
		if topN > 0 && len(in) > topN {
			return append([]schema.SearchResult(nil), in[:topN]...), nil
		}
		return in, nil
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if m.APIKey != "" {
		httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", m.APIKey))
	}

	if m.Client == nil {
		m.Client = httpx.NewFromConfig(nil)
	}

	resp, err := m.Client.Do(httpReq)
	if err != nil {
		logWarnf("ModelReranker: request failed: %v, using original order", err)
		if topN > 0 && len(in) > topN {
			return append([]schema.SearchResult(nil), in[:topN]...), nil
		}
		return in, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		logWarnf("ModelReranker: server returned status %d, using original order", resp.StatusCode)
		if topN > 0 && len(in) > topN {
			return append([]schema.SearchResult(nil), in[:topN]...), nil
		}
		return in, nil
	}

	var rerankResp modelRerankResp
	if err := json.NewDecoder(resp.Body).Decode(&rerankResp); err != nil {
		logWarnf("ModelReranker: failed to decode response: %v", err)
		if topN > 0 && len(in) > topN {
			return append([]schema.SearchResult(nil), in[:topN]...), nil
		}
		return in, nil
	}

	if len(rerankResp.Results) == 0 {
		logWarnf("ModelReranker: empty results, using original order")
		if topN > 0 && len(in) > topN {
			return append([]schema.SearchResult(nil), in[:topN]...), nil
		}
		return in, nil
	}

	// Build reranked results
	out := make([]schema.SearchResult, 0, len(rerankResp.Results))
	for _, result := range rerankResp.Results {
		if result.Index >= 0 && result.Index < len(in) {
			doc := in[result.Index]
			doc.Score = result.RelevanceScore
			out = append(out, doc)
		}
	}

	// Sort by relevance score descending
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Score > out[j].Score
	})

	// Limit to top N
	if topN > 0 && len(out) > topN {
		out = out[:topN]
	}

	logInfof("ModelReranker: reranked to top %d documents", len(out))
	return out, nil
}

// ================================================================================
// Helper functions
// ================================================================================

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// Helper logging functions that only log when api is available
func logInfof(format string, args ...interface{}) {
	defer func() {
		if r := recover(); r != nil {
			// Silently ignore logging errors in tests
		}
	}()
	// Note: Using fmt.Printf for now, can be replaced with proper logger
	fmt.Printf("[INFO] "+format+"\n", args...)
}

func logWarnf(format string, args ...interface{}) {
	defer func() {
		if r := recover(); r != nil {
			// Silently ignore logging errors in tests
		}
	}()
	// Note: Using fmt.Printf for now, can be replaced with proper logger
	fmt.Printf("[WARN] "+format+"\n", args...)
}
