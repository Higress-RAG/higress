package post

import (
	"context"
	"strings"
	"testing"

	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/schema"
)

// MockLLMProvider for testing
type MockCompressorLLMProvider struct {
	response string
	err      error
}

func (m *MockCompressorLLMProvider) GenerateCompletion(ctx context.Context, prompt string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.response, nil
}

func (m *MockCompressorLLMProvider) GetProviderType() string {
	return "mock"
}

// ================================================================================
// Truncate Compressor Tests
// ================================================================================

func TestTruncateCompressor_Compress(t *testing.T) {
	compressor := &TruncateCompressor{TargetRatio: 0.5}

	text := "This is a test document with ten words in it"
	compressed, ratio, err := compressor.Compress(context.Background(), text, "test query")

	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	if len(compressed) >= len(text) {
		t.Errorf("Expected compressed text to be shorter")
	}

	if ratio <= 0 {
		t.Errorf("Expected positive compression ratio, got %f", ratio)
	}

	// Should keep first half of tokens
	if !strings.HasPrefix(compressed, "This is a test") {
		t.Errorf("Expected to keep beginning of text, got: %s", compressed)
	}
}

func TestTruncateCompressor_BatchCompress(t *testing.T) {
	compressor := &TruncateCompressor{TargetRatio: 0.6}

	input := []schema.SearchResult{
		{Document: schema.Document{ID: "1", Content: "First document with some content here"}},
		{Document: schema.Document{ID: "2", Content: "Second document with more content"}},
	}

	result, err := compressor.BatchCompress(context.Background(), input, "test query")

	if err != nil {
		t.Fatalf("BatchCompress failed: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("Expected 2 results, got %d", len(result))
	}

	for i, r := range result {
		if len(r.Document.Content) >= len(input[i].Document.Content) {
			t.Errorf("Document %d was not compressed", i)
		}
	}
}

// ================================================================================
// Selective Compressor Tests
// ================================================================================

func TestSelectiveCompressor_Compress(t *testing.T) {
	mockProvider := &MockCompressorLLMProvider{
		response: "This is the relevant part. It answers the query directly.",
	}

	compressor := &SelectiveCompressor{Provider: mockProvider}

	text := "This is the relevant part. It answers the query directly. This is irrelevant noise. More irrelevant content here."
	compressed, ratio, err := compressor.Compress(context.Background(), text, "test query")

	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	if compressed == "" {
		t.Error("Expected non-empty compressed text")
	}

	if ratio <= 0 {
		t.Errorf("Expected positive compression ratio, got %f", ratio)
	}

	if compressed == text {
		t.Error("Expected text to be compressed")
	}
}

func TestSelectiveCompressor_EmptyResponse(t *testing.T) {
	mockProvider := &MockCompressorLLMProvider{
		response: "",
	}

	compressor := &SelectiveCompressor{Provider: mockProvider}

	text := "Original text"
	compressed, _, err := compressor.Compress(context.Background(), text, "test query")

	// Should fallback to original when compressed is empty
	if compressed != text {
		t.Errorf("Expected original text on empty compression, got: %s", compressed)
	}

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
}

// ================================================================================
// Summary Compressor Tests
// ================================================================================

func TestSummaryCompressor_Compress(t *testing.T) {
	mockProvider := &MockCompressorLLMProvider{
		response: "Summary: Kubernetes is a container orchestration platform.",
	}

	compressor := &SummaryCompressor{Provider: mockProvider}

	text := "Kubernetes is a container orchestration platform. It was originally designed by Google. It automates deployment, scaling, and management of containerized applications."
	compressed, _, err := compressor.Compress(context.Background(), text, "What is Kubernetes?")

	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	if compressed == "" {
		t.Error("Expected non-empty compressed text")
	}

	if !strings.Contains(compressed, "Kubernetes") {
		t.Error("Expected summary to contain key term")
	}
}

// ================================================================================
// Extraction Compressor Tests
// ================================================================================

func TestExtractionCompressor_Compress(t *testing.T) {
	mockProvider := &MockCompressorLLMProvider{
		response: "Kubernetes automates deployment, scaling, and management.",
	}

	compressor := &ExtractionCompressor{Provider: mockProvider}

	text := "Some background information. Kubernetes automates deployment, scaling, and management. More details here."
	compressed, _, err := compressor.Compress(context.Background(), text, "deployment automation")

	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	if compressed == "" {
		t.Error("Expected non-empty compressed text")
	}

	if len(compressed) >= len(text) {
		t.Error("Expected extracted text to be shorter")
	}
}

// ================================================================================
// Batch Compress Tests
// ================================================================================

func TestSelectiveCompressor_BatchCompress(t *testing.T) {
	mockProvider := &MockCompressorLLMProvider{
		response: "Relevant content.",
	}

	compressor := &SelectiveCompressor{Provider: mockProvider}

	input := []schema.SearchResult{
		{Document: schema.Document{ID: "1", Content: "Relevant content. Irrelevant noise."}},
		{Document: schema.Document{ID: "2", Content: "More relevant content here."}},
	}

	result, err := compressor.BatchCompress(context.Background(), input, "test query")

	if err != nil {
		t.Fatalf("BatchCompress failed: %v", err)
	}

	if len(result) == 0 {
		t.Fatal("Expected non-empty results")
	}

	// Both documents should be compressed
	for _, r := range result {
		if r.Document.Content == "" {
			t.Error("Expected non-empty document content")
		}
	}
}

func TestBatchCompress_AllEmpty(t *testing.T) {
	mockProvider := &MockCompressorLLMProvider{
		response: "",
	}

	compressor := &SelectiveCompressor{Provider: mockProvider}

	input := []schema.SearchResult{
		{Document: schema.Document{ID: "1", Content: "Some text"}},
	}

	result, err := compressor.BatchCompress(context.Background(), input, "test query")

	// Should fallback to originals when all compress to empty
	if err != nil {
		t.Fatalf("BatchCompress failed: %v", err)
	}

	if len(result) != len(input) {
		t.Errorf("Expected %d results (fallback to originals), got %d", len(input), len(result))
	}
}

// ================================================================================
// Factory Tests
// ================================================================================

func TestNewCompressor_Selective(t *testing.T) {
	mockProvider := &MockCompressorLLMProvider{response: "test"}

	compressor := NewCompressor("selective", 0.7, mockProvider)

	if _, ok := compressor.(*SelectiveCompressor); !ok {
		t.Error("Expected SelectiveCompressor")
	}
}

func TestNewCompressor_Summary(t *testing.T) {
	mockProvider := &MockCompressorLLMProvider{response: "test"}

	compressor := NewCompressor("summary", 0.7, mockProvider)

	if _, ok := compressor.(*SummaryCompressor); !ok {
		t.Error("Expected SummaryCompressor")
	}
}

func TestNewCompressor_Extraction(t *testing.T) {
	mockProvider := &MockCompressorLLMProvider{response: "test"}

	compressor := NewCompressor("extraction", 0.7, mockProvider)

	if _, ok := compressor.(*ExtractionCompressor); !ok {
		t.Error("Expected ExtractionCompressor")
	}
}

func TestNewCompressor_Truncate(t *testing.T) {
	compressor := NewCompressor("truncate", 0.7, nil)

	if _, ok := compressor.(*TruncateCompressor); !ok {
		t.Error("Expected TruncateCompressor")
	}
}

func TestNewCompressor_FallbackWithoutLLM(t *testing.T) {
	// When LLM is required but not provided, should fallback to truncate
	compressor := NewCompressor("selective", 0.7, nil)

	if _, ok := compressor.(*TruncateCompressor); !ok {
		t.Error("Expected TruncateCompressor as fallback")
	}
}

// ================================================================================
// Utility Tests
// ================================================================================

func TestCalculateCompressionRatio(t *testing.T) {
	tests := []struct {
		name       string
		original   string
		compressed string
		expected   float64
	}{
		{
			name:       "50% compression",
			original:   "Hello world",
			compressed: "Hello",
			expected:   54.54, // (11-5)/11 * 100
		},
		{
			name:       "No compression",
			original:   "Hello",
			compressed: "Hello",
			expected:   0,
		},
		{
			name:       "Empty original",
			original:   "",
			compressed: "",
			expected:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ratio := calculateCompressionRatio(tt.original, tt.compressed)

			// Allow small floating point differences
			if ratio < tt.expected-1 || ratio > tt.expected+1 {
				t.Errorf("Expected ratio ~%.2f, got %.2f", tt.expected, ratio)
			}
		})
	}
}

func TestCompressText_BackwardCompatibility(t *testing.T) {
	// Test that the original CompressText function still works
	text := "one two three four five six seven eight nine ten"
	compressed := CompressText(text, 0.5)

	tokens := strings.Fields(compressed)
	if len(tokens) != 5 {
		t.Errorf("Expected 5 tokens (50%%), got %d", len(tokens))
	}

	if compressed != "one two three four five" {
		t.Errorf("Unexpected compressed text: %s", compressed)
	}
}
