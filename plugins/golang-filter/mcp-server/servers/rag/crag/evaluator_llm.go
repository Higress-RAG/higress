package crag

import (
	"context"
	"fmt"
	"regexp"
	"strconv"

	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/llm"
)

// LLMEvaluator uses an LLM to evaluate (query, context) relevance with detailed prompts.
// It scores relevance on a scale of 0-1 and determines a verdict.
type LLMEvaluator struct {
	Provider    llm.Provider
	CorrectTh   float64 // threshold for "correct" verdict (default 0.7)
	IncorrectTh float64 // threshold for "incorrect" verdict (default 0.3)
}

// systemPrompt guides the LLM on how to evaluate relevance
const systemPrompt = `You are an expert at evaluating document relevance. 
Rate how relevant the given document is to the query on a scale from 0 to 1.
0 means completely irrelevant, 1 means perfectly relevant.
Provide ONLY the score as a float between 0 and 1.`

// Evaluate implements the Evaluator interface using LLM-based relevance scoring
func (e *LLMEvaluator) Evaluate(ctx context.Context, query string, contextText string) (float64, Verdict, error) {
	// Set default thresholds if not configured
	correctTh := e.CorrectTh
	if correctTh == 0 {
		correctTh = 0.7
	}
	incorrectTh := e.IncorrectTh
	if incorrectTh == 0 {
		incorrectTh = 0.3
	}

	// Build the prompt
	userPrompt := fmt.Sprintf("Query: %s\n\nDocument: %s", query, contextText)
	fullPrompt := fmt.Sprintf("%s\n\n%s", systemPrompt, userPrompt)

	// Call LLM
	response, err := e.Provider.GenerateCompletion(ctx, fullPrompt)
	if err != nil {
		logWarnf("LLMEvaluator: failed to call LLM: %v", err)
		return 0.5, VerdictAmbiguous, err
	}

	// Parse score from response using regex
	scoreRegex := regexp.MustCompile(`(\d+(\.\d+)?)`)
	match := scoreRegex.FindStringSubmatch(response)

	score := 0.5 // default middle value on parse failure
	if len(match) > 0 {
		parsed, err := strconv.ParseFloat(match[1], 64)
		if err == nil && parsed >= 0 && parsed <= 1 {
			score = parsed
		} else {
			logWarnf("LLMEvaluator: parsed score out of range or invalid: %f", parsed)
		}
	} else {
		logWarnf("LLMEvaluator: failed to parse score from response: %s", response)
	}

	// Determine verdict based on thresholds
	var verdict Verdict
	if score >= correctTh {
		verdict = VerdictCorrect
	} else if score < incorrectTh {
		verdict = VerdictIncorrect
	} else {
		verdict = VerdictAmbiguous
	}

	logInfof("LLMEvaluator: score=%.2f, verdict=%v", score, verdict)
	return score, verdict, nil
}
