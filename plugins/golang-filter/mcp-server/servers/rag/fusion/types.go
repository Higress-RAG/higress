package fusion

import (
	"context"

	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/schema"
)

// RetrieverResult groups the documents returned by a single retriever for a given query.
type RetrieverResult struct {
	// Query is the raw query string used for this retrieval call.
	Query string
	// Retriever is the logical retriever key (e.g. "vector", "bm25").
	Retriever string
	// Provider is an optional identifier for the concrete backend instance.
	Provider string
	// Results are the ranked documents produced by the retriever.
	Results []schema.SearchResult
	// Attributes carries optional per-retriever metadata (scores, stats, etc.).
	Attributes map[string]any
}

// Strategy defines pluggable fusion strategies.
type Strategy interface {
	// Fuse merges multiple retriever result lists into a single ranked list.
	Fuse(ctx context.Context, inputs []RetrieverResult, params map[string]any) ([]schema.SearchResult, error)
	// Name returns the strategy identifier.
	Name() string
}

// MetadataProvider is implemented by strategies that expose runtime metadata.
type MetadataProvider interface {
	Metadata() map[string]any
}
