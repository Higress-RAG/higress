package retriever

import (
    "context"

    "github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/schema"
)

// Retriever defines a unified search interface across different backends.
type Retriever interface {
    Type() string
    Search(ctx context.Context, query string, topK int) ([]schema.SearchResult, error)
}

// CandidateList is a utility alias for readability.
type CandidateList []schema.SearchResult
