package crag

import (
    "github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/schema"
)

// CorrectAction refines documents (placeholder: passthrough).
func CorrectAction(cands []schema.SearchResult) []schema.SearchResult { return cands }

// IncorrectAction discards internal results and leaves an empty set (web search could fill later).
func IncorrectAction() []schema.SearchResult { return []schema.SearchResult{} }

// AmbiguousAction blends internal refined docs with (optional) external ones (placeholder: passthrough).
func AmbiguousAction(internal []schema.SearchResult, external []schema.SearchResult) []schema.SearchResult {
    if len(external) == 0 { return internal }
    out := make([]schema.SearchResult, 0, len(internal)+len(external))
    out = append(out, internal...)
    out = append(out, external...)
    return out
}

