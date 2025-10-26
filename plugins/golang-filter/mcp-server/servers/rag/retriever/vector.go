package retriever

import (
    "context"

    "github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/embedding"
    "github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/schema"
    "github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/vectordb"
)

// VectorRetriever implements Retriever using embedding+vector store backend.
type VectorRetriever struct {
    Embed   embedding.Provider
    Store   vectordb.VectorStoreProvider
    TopK    int
    // Threshold may be used by underlying vector search options.
    Threshold float64
}

func (r *VectorRetriever) Type() string { return "vector" }

func (r *VectorRetriever) Search(ctx context.Context, query string, topK int) ([]schema.SearchResult, error) {
    if topK <= 0 {
        if r.TopK > 0 {
            topK = r.TopK
        } else {
            topK = 10
        }
    }
    v, err := r.Embed.GetEmbedding(ctx, query)
    if err != nil {
        return nil, err
    }
    opts := &schema.SearchOptions{TopK: topK, Threshold: r.Threshold}
    return r.Store.SearchDocs(ctx, v, opts)
}
