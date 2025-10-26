package orchestrator

import "strings"

// queryClass describes a simple classification outcome.
type queryClass struct {
    MultiDoc bool
}

// classifyQuery applies light rule-based classification.
func classifyQuery(q string) queryClass {
    qs := strings.ToLower(q)
    // Heuristic: conjunctions or multiple entities often imply multiple documents.
    md := strings.Contains(qs, " and ") || strings.Contains(qs, " vs ") || strings.Contains(qs, " compare ")
    return queryClass{MultiDoc: md}
}

// rewriteVariants returns sparse-optimized and dense-optimized variants (placeholder).
func rewriteVariants(q string) (sparse, dense string) {
    // Sparse: keep keywords only
    sparse = strings.Join(strings.Fields(q), " ")
    dense = q
    return
}

// decompose returns a simple 2-way decomposition if MultiDoc is likely (placeholder).
func decompose(q string) []string {
    // In real impl, call LLM to decompose. Here, duplicate the query as two sub-queries for demo.
    return []string{q, q}
}
