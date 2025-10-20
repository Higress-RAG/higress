package crag

import "context"

// Verdict indicates evaluator decision for corrective actions.
type Verdict int

const (
    VerdictCorrect Verdict = iota
    VerdictAmbiguous
    VerdictIncorrect
)

// Evaluator scores (query, context) relevance in [0,1] and yields a verdict.
type Evaluator interface {
    Evaluate(ctx context.Context, query string, contextText string) (score float64, verdict Verdict, err error)
}
