package crag

import (
	"context"
	"testing"
)

// MockLLMProvider is a mock implementation of llm.Provider for testing
type MockLLMProvider struct {
	response string
	err      error
}

func (m *MockLLMProvider) GenerateCompletion(ctx context.Context, prompt string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.response, nil
}

func (m *MockLLMProvider) GetProviderType() string {
	return "mock"
}

func TestLLMEvaluator_Evaluate(t *testing.T) {
	tests := []struct {
		name            string
		llmResponse     string
		llmError        error
		correctTh       float64
		incorrectTh     float64
		expectedScore   float64
		expectedVerdict Verdict
	}{
		{
			name:            "High relevance score",
			llmResponse:     "0.9",
			correctTh:       0.7,
			incorrectTh:     0.3,
			expectedScore:   0.9,
			expectedVerdict: VerdictCorrect,
		},
		{
			name:            "Low relevance score",
			llmResponse:     "0.2",
			correctTh:       0.7,
			incorrectTh:     0.3,
			expectedScore:   0.2,
			expectedVerdict: VerdictIncorrect,
		},
		{
			name:            "Medium relevance score",
			llmResponse:     "0.5",
			correctTh:       0.7,
			incorrectTh:     0.3,
			expectedScore:   0.5,
			expectedVerdict: VerdictAmbiguous,
		},
		{
			name:            "Score with text prefix",
			llmResponse:     "The relevance score is 0.85",
			correctTh:       0.7,
			incorrectTh:     0.3,
			expectedScore:   0.85,
			expectedVerdict: VerdictCorrect,
		},
		{
			name:            "Invalid score returns default",
			llmResponse:     "invalid",
			correctTh:       0.7,
			incorrectTh:     0.3,
			expectedScore:   0.5,
			expectedVerdict: VerdictAmbiguous,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockProvider := &MockLLMProvider{
				response: tt.llmResponse,
				err:      tt.llmError,
			}

			evaluator := &LLMEvaluator{
				Provider:    mockProvider,
				CorrectTh:   tt.correctTh,
				IncorrectTh: tt.incorrectTh,
			}

			score, verdict, err := evaluator.Evaluate(context.Background(), "test query", "test context")

			if err != nil && tt.llmError == nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if score != tt.expectedScore {
				t.Errorf("Expected score %f, got %f", tt.expectedScore, score)
			}

			if verdict != tt.expectedVerdict {
				t.Errorf("Expected verdict %v, got %v", tt.expectedVerdict, verdict)
			}
		})
	}
}

func TestLLMEvaluator_DefaultThresholds(t *testing.T) {
	mockProvider := &MockLLMProvider{
		response: "0.8",
	}

	evaluator := &LLMEvaluator{
		Provider: mockProvider,
		// No thresholds set - should use defaults
	}

	score, verdict, err := evaluator.Evaluate(context.Background(), "test", "test")

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	if score != 0.8 {
		t.Errorf("Expected score 0.8, got %f", score)
	}

	// With default threshold of 0.7, score of 0.8 should be Correct
	if verdict != VerdictCorrect {
		t.Errorf("Expected verdict Correct, got %v", verdict)
	}
}

