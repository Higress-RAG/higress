package post

import (
	"context"
	"fmt"
	"strings"

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

// ================================================================================
// Compressor Factory
// ================================================================================

// NewCompressor creates a Compressor based on method and configuration
func NewCompressor(method string, targetRatio float64, llmProvider llm.Provider) Compressor {
	switch strings.ToLower(method) {
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
