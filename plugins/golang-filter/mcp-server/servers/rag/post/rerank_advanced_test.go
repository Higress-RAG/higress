package post

import (
	"context"
	"testing"

	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/schema"
)

// MockLLMProvider for testing
type MockLLMProvider struct {
	responses []string
	callCount int
}

func (m *MockLLMProvider) GenerateCompletion(ctx context.Context, prompt string) (string, error) {
	if m.callCount >= len(m.responses) {
		return "5", nil // Default response
	}
	response := m.responses[m.callCount]
	m.callCount++
	return response, nil
}

func (m *MockLLMProvider) GetProviderType() string {
	return "mock"
}

func TestLLMReranker_Rerank(t *testing.T) {
	mockProvider := &MockLLMProvider{
		responses: []string{"9", "5", "7"},
	}

	reranker := &LLMReranker{
		Provider: mockProvider,
	}

	input := []schema.SearchResult{
		{Document: schema.Document{ID: "1", Content: "First document"}, Score: 0.5},
		{Document: schema.Document{ID: "2", Content: "Second document"}, Score: 0.7},
		{Document: schema.Document{ID: "3", Content: "Third document"}, Score: 0.6},
	}

	result, err := reranker.Rerank(context.Background(), "test query", input, 3)
	if err != nil {
		t.Fatalf("Rerank failed: %v", err)
	}

	if len(result) != 3 {
		t.Fatalf("Expected 3 results, got %d", len(result))
	}

	// Check order: should be sorted by LLM scores (9, 7, 5)
	if result[0].Document.ID != "1" || result[0].Score != 9 {
		t.Errorf("Expected first result to be doc 1 with score 9, got %s with score %f", result[0].Document.ID, result[0].Score)
	}
	if result[1].Document.ID != "3" || result[1].Score != 7 {
		t.Errorf("Expected second result to be doc 3 with score 7, got %s with score %f", result[1].Document.ID, result[1].Score)
	}
	if result[2].Document.ID != "2" || result[2].Score != 5 {
		t.Errorf("Expected third result to be doc 2 with score 5, got %s with score %f", result[2].Document.ID, result[2].Score)
	}
}

func TestLLMReranker_TopN(t *testing.T) {
	mockProvider := &MockLLMProvider{
		responses: []string{"9", "5", "7"},
	}

	reranker := &LLMReranker{
		Provider: mockProvider,
	}

	input := []schema.SearchResult{
		{Document: schema.Document{ID: "1", Content: "First"}, Score: 0.5},
		{Document: schema.Document{ID: "2", Content: "Second"}, Score: 0.7},
		{Document: schema.Document{ID: "3", Content: "Third"}, Score: 0.6},
	}

	result, err := reranker.Rerank(context.Background(), "test query", input, 2)
	if err != nil {
		t.Fatalf("Rerank failed: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("Expected 2 results (topN), got %d", len(result))
	}
}

func TestKeywordReranker_Rerank(t *testing.T) {
	reranker := &KeywordReranker{
		MinKeywordLength: 3,
		BaseScoreWeight:  0.5,
	}

	input := []schema.SearchResult{
		{Document: schema.Document{ID: "1", Content: "This document talks about kubernetes and containers"}, Score: 0.5},
		{Document: schema.Document{ID: "2", Content: "Python programming language"}, Score: 0.7},
		{Document: schema.Document{ID: "3", Content: "kubernetes deployment and orchestration"}, Score: 0.6},
	}

	result, err := reranker.Rerank(context.Background(), "kubernetes deployment", input, 3)
	if err != nil {
		t.Fatalf("Rerank failed: %v", err)
	}

	if len(result) != 3 {
		t.Fatalf("Expected 3 results, got %d", len(result))
	}

	// Document 3 should rank highest (has both "kubernetes" and "deployment")
	// Document 1 should be second (has "kubernetes")
	// Document 2 should be last (no matching keywords)
	if result[0].Document.ID != "3" {
		t.Errorf("Expected doc 3 to rank first, got %s", result[0].Document.ID)
	}
}

func TestKeywordReranker_PositionBonus(t *testing.T) {
	reranker := &KeywordReranker{
		MinKeywordLength: 3,
		BaseScoreWeight:  0.1,
	}

	input := []schema.SearchResult{
		{Document: schema.Document{ID: "1", Content: "Some text here... kubernetes is mentioned later"}, Score: 0.5},
		{Document: schema.Document{ID: "2", Content: "kubernetes is mentioned first in this document"}, Score: 0.5},
	}

	result, err := reranker.Rerank(context.Background(), "kubernetes", input, 2)
	if err != nil {
		t.Fatalf("Rerank failed: %v", err)
	}

	// Document 2 should rank higher due to position bonus
	if result[0].Document.ID != "2" {
		t.Errorf("Expected doc 2 to rank first (position bonus), got %s", result[0].Document.ID)
	}
}

func TestModelReranker_Fallback(t *testing.T) {
	// Test fallback behavior when endpoint is not configured
	reranker := &ModelReranker{
		Endpoint: "",
		Model:    "bge-reranker-large",
	}

	input := []schema.SearchResult{
		{Document: schema.Document{ID: "1", Content: "First"}, Score: 0.5},
		{Document: schema.Document{ID: "2", Content: "Second"}, Score: 0.7},
	}

	result, err := reranker.Rerank(context.Background(), "test query", input, 2)
	if err != nil {
		t.Fatalf("Rerank failed: %v", err)
	}

	// Should return all 2 results in original order when endpoint is empty
	if len(result) != 2 {
		t.Fatalf("Expected 2 results, got %d", len(result))
	}

	// The fallback should just return the input as-is
	if result[0].Document.ID != "1" && result[1].Document.ID != "2" {
		t.Errorf("Expected original order to be preserved")
	}
}
