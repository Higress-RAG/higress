package post

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/common/httpx"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/common/logger"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/llm"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/schema"
)

// ================================================================================
// Compressor Interface
// ================================================================================

// Compressor defines the interface for context compression strategies.
type Compressor interface {
	// Compress compresses a single text chunk based on query relevance
	Compress(ctx context.Context, text string, query string) (compressed string, compressionRatio float64, err error)

	// BatchCompress compresses multiple search results
	BatchCompress(ctx context.Context, results []schema.SearchResult, query string) ([]schema.SearchResult, error)
}

// CompressionStats holds compression statistics
type CompressionStats struct {
	OriginalLength   int
	CompressedLength int
	CompressionRatio float64 // percentage (0-100)
}

// ================================================================================
// 1. Truncate Compressor (Original Simple Strategy)
// ================================================================================

// TruncateCompressor is a simple, query-agnostic compressor.
// It trims the text to a target ratio of its length, preserving the beginning.
type TruncateCompressor struct {
	TargetRatio float64 // Target compression ratio (0-1)
}

func (t *TruncateCompressor) Compress(ctx context.Context, text string, query string) (string, float64, error) {
	compressed := CompressText(text, t.TargetRatio)
	ratio := calculateCompressionRatio(text, compressed)
	return compressed, ratio, nil
}

func (t *TruncateCompressor) BatchCompress(ctx context.Context, results []schema.SearchResult, query string) ([]schema.SearchResult, error) {
	logger.Infof("TruncateCompressor: compressing %d documents...", len(results))

	compressed := make([]schema.SearchResult, len(results))
	for i, result := range results {
		compressedText, _, _ := t.Compress(ctx, result.Document.Content, query)
		result.Document.Content = compressedText
		compressed[i] = result
	}

	return compressed, nil
}

// CompressText is the original simple compressor function (kept for backward compatibility).
func CompressText(text string, targetRatio float64) string {
	if targetRatio <= 0 || targetRatio >= 1 {
		return text
	}
	// Simple token-ish split to reduce mid-text noise; keep first N tokens.
	tokens := strings.Fields(text)
	if len(tokens) == 0 {
		return text
	}
	keep := int(float64(len(tokens)) * targetRatio)
	if keep <= 0 {
		keep = 1
	}
	if keep >= len(tokens) {
		return text
	}
	return strings.Join(tokens[:keep], " ")
}

// ================================================================================
// 2. Selective Compressor (LLM-based relevance filtering)
// ================================================================================

// SelectiveCompressor extracts ONLY the sentences or paragraphs directly relevant to the query.
type SelectiveCompressor struct {
	Provider llm.Provider
	Model    string
}

const selectiveSystemPrompt = `You are an expert at information filtering. 
Your task is to analyze a document chunk and extract ONLY the sentences or paragraphs that are directly 
relevant to the user's query. Remove all irrelevant content.

Your output should:
1. ONLY include text that helps answer the query
2. Preserve the exact wording of relevant sentences (do not paraphrase)
3. Maintain the original order of the text
4. Include ALL relevant content, even if it seems redundant
5. EXCLUDE any text that isn't relevant to the query

Format your response as plain text with no additional comments.`

func (s *SelectiveCompressor) Compress(ctx context.Context, text string, query string) (string, float64, error) {
	if s.Provider == nil {
		return text, 0, nil
	}

	userPrompt := fmt.Sprintf(`Query: %s

Document Chunk:
%s

Extract only the content relevant to answering this query.`, query, text)

	fullPrompt := fmt.Sprintf("%s\n\n%s", selectiveSystemPrompt, userPrompt)

	compressed, err := s.Provider.GenerateCompletion(ctx, fullPrompt)
	if err != nil {
		logger.Warnf("SelectiveCompressor: failed to compress: %v, using original", err)
		return text, 0, err
	}

	compressed = strings.TrimSpace(compressed)
	if compressed == "" {
		logger.Warnf("SelectiveCompressor: compressed to empty, using original")
		return text, 0, nil
	}

	ratio := calculateCompressionRatio(text, compressed)
	return compressed, ratio, nil
}

func (s *SelectiveCompressor) BatchCompress(ctx context.Context, results []schema.SearchResult, query string) ([]schema.SearchResult, error) {
	logger.Infof("SelectiveCompressor: compressing %d documents...", len(results))

	totalOriginal := 0
	totalCompressed := 0
	compressed := make([]schema.SearchResult, 0, len(results))

	for i, result := range results {
		if i%5 == 0 {
			logger.Infof("SelectiveCompressor: compressing chunk %d/%d...", i+1, len(results))
		}

		compressedText, ratio, err := s.Compress(ctx, result.Document.Content, query)
		if err == nil && compressedText != "" {
			result.Document.Content = compressedText
			totalOriginal += len(result.Document.Content)
			totalCompressed += len(compressedText)
			compressed = append(compressed, result)
		} else if compressedText != "" {
			// Keep even if error, as long as we have content
			result.Document.Content = compressedText
			compressed = append(compressed, result)
		}
		_ = ratio
	}

	// Filter out empty results
	if len(compressed) == 0 {
		logger.Warnf("SelectiveCompressor: all chunks compressed to empty, using originals")
		return results, nil
	}

	overallRatio := 0.0
	if totalOriginal > 0 {
		overallRatio = float64(totalOriginal-totalCompressed) / float64(totalOriginal) * 100
	}
	logger.Infof("SelectiveCompressor: overall compression ratio: %.2f%%", overallRatio)

	return compressed, nil
}

// ================================================================================
// 3. Summary Compressor (LLM-based summarization)
// ================================================================================

// SummaryCompressor creates a concise summary focusing on query-relevant information.
type SummaryCompressor struct {
	Provider llm.Provider
	Model    string
}

const summarySystemPrompt = `You are an expert at summarization. 
Your task is to create a concise summary of the provided chunk that focuses ONLY on 
information relevant to the user's query.

Your output should:
1. Be brief but comprehensive regarding query-relevant information
2. Focus exclusively on information related to the query
3. Omit irrelevant details
4. Be written in a neutral, factual tone

Format your response as plain text with no additional comments.`

func (s *SummaryCompressor) Compress(ctx context.Context, text string, query string) (string, float64, error) {
	if s.Provider == nil {
		return text, 0, nil
	}

	userPrompt := fmt.Sprintf(`Query: %s

Document Chunk:
%s

Create a concise summary focusing only on information relevant to the query.`, query, text)

	fullPrompt := fmt.Sprintf("%s\n\n%s", summarySystemPrompt, userPrompt)

	compressed, err := s.Provider.GenerateCompletion(ctx, fullPrompt)
	if err != nil {
		logger.Warnf("SummaryCompressor: failed to compress: %v, using original", err)
		return text, 0, err
	}

	compressed = strings.TrimSpace(compressed)
	if compressed == "" {
		logger.Warnf("SummaryCompressor: compressed to empty, using original")
		return text, 0, nil
	}

	ratio := calculateCompressionRatio(text, compressed)
	return compressed, ratio, nil
}

func (s *SummaryCompressor) BatchCompress(ctx context.Context, results []schema.SearchResult, query string) ([]schema.SearchResult, error) {
	logger.Infof("SummaryCompressor: compressing %d documents...", len(results))

	totalOriginal := 0
	totalCompressed := 0
	compressed := make([]schema.SearchResult, 0, len(results))

	for i, result := range results {
		if i%5 == 0 {
			logger.Infof("SummaryCompressor: compressing chunk %d/%d...", i+1, len(results))
		}

		compressedText, ratio, err := s.Compress(ctx, result.Document.Content, query)
		if err == nil && compressedText != "" {
			result.Document.Content = compressedText
			totalOriginal += len(result.Document.Content)
			totalCompressed += len(compressedText)
			compressed = append(compressed, result)
		} else if compressedText != "" {
			result.Document.Content = compressedText
			compressed = append(compressed, result)
		}
		_ = ratio
	}

	if len(compressed) == 0 {
		logger.Warnf("SummaryCompressor: all chunks compressed to empty, using originals")
		return results, nil
	}

	overallRatio := 0.0
	if totalOriginal > 0 {
		overallRatio = float64(totalOriginal-totalCompressed) / float64(totalOriginal) * 100
	}
	logger.Infof("SummaryCompressor: overall compression ratio: %.2f%%", overallRatio)

	return compressed, nil
}

// ================================================================================
// 4. Extraction Compressor (Extract exact relevant sentences)
// ================================================================================

// ExtractionCompressor extracts ONLY exact sentences containing query-relevant information.
type ExtractionCompressor struct {
	Provider llm.Provider
	Model    string
}

const extractionSystemPrompt = `You are an expert at information extraction.
Your task is to extract ONLY the exact sentences from the document chunk that contain information relevant 
to answering the user's query.

Your output should:
1. Include ONLY direct quotes of relevant sentences from the original text
2. Preserve the original wording (do not modify the text)
3. Include ONLY sentences that directly relate to the query
4. Separate extracted sentences with newlines
5. Do not add any commentary or additional text

Format your response as plain text with no additional comments.`

func (e *ExtractionCompressor) Compress(ctx context.Context, text string, query string) (string, float64, error) {
	if e.Provider == nil {
		return text, 0, nil
	}

	userPrompt := fmt.Sprintf(`Query: %s

Document Chunk:
%s

Extract only the exact sentences that are relevant to answering this query.`, query, text)

	fullPrompt := fmt.Sprintf("%s\n\n%s", extractionSystemPrompt, userPrompt)

	compressed, err := e.Provider.GenerateCompletion(ctx, fullPrompt)
	if err != nil {
		logger.Warnf("ExtractionCompressor: failed to compress: %v, using original", err)
		return text, 0, err
	}

	compressed = strings.TrimSpace(compressed)
	if compressed == "" {
		logger.Warnf("ExtractionCompressor: compressed to empty, using original")
		return text, 0, nil
	}

	ratio := calculateCompressionRatio(text, compressed)
	return compressed, ratio, nil
}

func (e *ExtractionCompressor) BatchCompress(ctx context.Context, results []schema.SearchResult, query string) ([]schema.SearchResult, error) {
	logger.Infof("ExtractionCompressor: compressing %d documents...", len(results))

	totalOriginal := 0
	totalCompressed := 0
	compressed := make([]schema.SearchResult, 0, len(results))

	for i, result := range results {
		if i%5 == 0 {
			logger.Infof("ExtractionCompressor: compressing chunk %d/%d...", i+1, len(results))
		}

		compressedText, ratio, err := e.Compress(ctx, result.Document.Content, query)
		if err == nil && compressedText != "" {
			result.Document.Content = compressedText
			totalOriginal += len(result.Document.Content)
			totalCompressed += len(compressedText)
			compressed = append(compressed, result)
		} else if compressedText != "" {
			result.Document.Content = compressedText
			compressed = append(compressed, result)
		}
		_ = ratio
	}

	if len(compressed) == 0 {
		logger.Warnf("ExtractionCompressor: all chunks compressed to empty, using originals")
		return results, nil
	}

	overallRatio := 0.0
	if totalOriginal > 0 {
		overallRatio = float64(totalOriginal-totalCompressed) / float64(totalOriginal) * 100
	}
	logger.Infof("ExtractionCompressor: overall compression ratio: %.2f%%", overallRatio)

	return compressed, nil
}

// ================================================================================
// 5. HTTP Compressor (External microservice, e.g., LLMLingua)
// ================================================================================

// HTTPCompressor delegates compression to an external HTTP service.
type HTTPCompressor struct {
	Endpoint    string
	Client      *httpx.Client
	Headers     map[string]string
	TargetRatio float64
}

func (h *HTTPCompressor) Compress(ctx context.Context, text string, query string) (string, float64, error) {
	if h.Endpoint == "" || text == "" {
		return text, 0, nil
	}
	results := []schema.SearchResult{{
		Document: schema.Document{
			ID:      "compress-single",
			Content: text,
		},
	}}
	compressed, err := h.BatchCompress(ctx, results, query)
	if err != nil || len(compressed) == 0 {
		return text, 0, err
	}
	out := compressed[0].Document.Content
	if out == "" {
		return text, 0, err
	}
	return out, calculateCompressionRatio(text, out), nil
}

func (h *HTTPCompressor) BatchCompress(ctx context.Context, results []schema.SearchResult, query string) ([]schema.SearchResult, error) {
	if h.Endpoint == "" || len(results) == 0 {
		return results, nil
	}

	logger.Infof("HTTPCompressor: compressing %d documents via %s", len(results), h.Endpoint)

	req := httpCompressRequest{
		Query:       query,
		TargetRatio: h.TargetRatio,
		Documents:   make([]httpCompressDocument, 0, len(results)),
	}
	index := make(map[string]int, len(results))
	for i, result := range results {
		docID := result.Document.ID
		if docID == "" {
			docID = fmt.Sprintf("compress-%d", i)
		}
		index[docID] = i
		req.Documents = append(req.Documents, httpCompressDocument{
			ID:       docID,
			Text:     result.Document.Content,
			Metadata: result.Document.Metadata,
		})
	}

	resp, err := h.doRequest(ctx, &req)
	if err != nil {
		logger.Warnf("HTTPCompressor: request failed: %v", err)
		return results, err
	}
	if resp == nil || len(resp.Documents) == 0 {
		logger.Warnf("HTTPCompressor: empty response, using original results")
		return results, nil
	}

	out := make([]schema.SearchResult, 0, len(resp.Documents))
	for _, doc := range resp.Documents {
		idx, ok := index[doc.ID]
		if !ok {
			continue
		}
		item := results[idx]
		if doc.Text != "" {
			item.Document.Content = doc.Text
		}
		if doc.Metadata != nil {
			if item.Document.Metadata == nil {
				item.Document.Metadata = make(map[string]any, len(doc.Metadata))
			}
			for k, v := range doc.Metadata {
				item.Document.Metadata[k] = v
			}
		}
		if doc.Score != 0 {
			item.Score = doc.Score
		}
		out = append(out, item)
	}

	if len(out) == 0 {
		logger.Warnf("HTTPCompressor: no matching documents in response, using original results")
		return results, nil
	}
	return out, nil
}

func (h *HTTPCompressor) doRequest(ctx context.Context, payload *httpCompressRequest) (*httpCompressResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("http compressor marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("http compressor new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range h.Headers {
		req.Header.Set(k, v)
	}
	client := h.ensureClient()
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http compressor request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http compressor status %d", resp.StatusCode)
	}
	var result httpCompressResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("http compressor decode response: %w", err)
	}
	return &result, nil
}

func (h *HTTPCompressor) ensureClient() *httpx.Client {
	if h.Client == nil {
		h.Client = httpx.NewFromConfig(nil)
	}
	return h.Client
}

// ================================================================================
// Helper functions
// ================================================================================

// calculateCompressionRatio calculates the compression ratio as a percentage
func calculateCompressionRatio(original, compressed string) float64 {
	if len(original) == 0 {
		return 0
	}
	reduction := float64(len(original)-len(compressed)) / float64(len(original)) * 100
	if reduction < 0 {
		return 0
	}
	return reduction
}

type httpCompressRequest struct {
	Query       string                 `json:"query"`
	TargetRatio float64                `json:"target_ratio,omitempty"`
	Documents   []httpCompressDocument `json:"documents"`
}

type httpCompressDocument struct {
	ID       string         `json:"id"`
	Text     string         `json:"text"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type httpCompressResponse struct {
	Documents []httpCompressedDocument `json:"documents"`
}

type httpCompressedDocument struct {
	ID       string         `json:"id"`
	Text     string         `json:"text"`
	Metadata map[string]any `json:"metadata,omitempty"`
	Score    float64        `json:"score,omitempty"`
}

// ================================================================================
// Compressor Factory
// ================================================================================

// CompressorOption configures the HTTP/remote compressor factory.
type CompressorOption func(*compressorOptions)

type compressorOptions struct {
	endpoint string
	headers  map[string]string
	client   *httpx.Client
}

// WithHTTPEndpoint sets the remote compressor endpoint.
func WithHTTPEndpoint(endpoint string) CompressorOption {
	return func(opts *compressorOptions) {
		opts.endpoint = endpoint
	}
}

// WithHTTPHeaders sets static headers (e.g., Authorization) for the HTTP compressor.
func WithHTTPHeaders(headers map[string]string) CompressorOption {
	return func(opts *compressorOptions) {
		if len(headers) == 0 {
			return
		}
		if opts.headers == nil {
			opts.headers = make(map[string]string, len(headers))
		}
		for k, v := range headers {
			opts.headers[k] = v
		}
	}
}

// WithHTTPClient injects a custom httpx.Client.
func WithHTTPClient(client *httpx.Client) CompressorOption {
	return func(opts *compressorOptions) {
		opts.client = client
	}
}

// NewCompressor creates a Compressor based on method and configuration
func NewCompressor(method string, targetRatio float64, llmProvider llm.Provider, opts ...CompressorOption) Compressor {
	options := compressorOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	switch strings.ToLower(method) {
	case "http", "llmlingua", "llm-lingua":
		if options.endpoint == "" {
			logger.Warnf("HTTP compression requires endpoint, falling back to truncate")
			return &TruncateCompressor{TargetRatio: targetRatio}
		}
		return &HTTPCompressor{
			Endpoint:    options.endpoint,
			Client:      options.client,
			Headers:     options.headers,
			TargetRatio: targetRatio,
		}
	case "selective":
		if llmProvider == nil {
			logger.Warnf("Selective compression requires LLM provider, falling back to truncate")
			return &TruncateCompressor{TargetRatio: targetRatio}
		}
		return &SelectiveCompressor{Provider: llmProvider}

	case "summary":
		if llmProvider == nil {
			logger.Warnf("Summary compression requires LLM provider, falling back to truncate")
			return &TruncateCompressor{TargetRatio: targetRatio}
		}
		return &SummaryCompressor{Provider: llmProvider}

	case "extraction":
		if llmProvider == nil {
			logger.Warnf("Extraction compression requires LLM provider, falling back to truncate")
			return &TruncateCompressor{TargetRatio: targetRatio}
		}
		return &ExtractionCompressor{Provider: llmProvider}

	case "truncate", "":
		// Default to truncate
		return &TruncateCompressor{TargetRatio: targetRatio}

	default:
		logger.Warnf("Unknown compression method: %s, using truncate", method)
		return &TruncateCompressor{TargetRatio: targetRatio}
	}
}
