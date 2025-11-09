package crag

import (
	"context"

	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/schema"
)

// ActionContext holds the dependencies needed for executing corrective actions.
type ActionContext struct {
	Refiner       *KnowledgeRefiner
	WebSearcher   *WebSearcher
	QueryRewriter *QueryRewriter
	Query         string
	Context       context.Context
}

// CorrectAction handles high-relevance scenario: documents are relevant, use them directly.
// Optionally refine the content for better quality.
func CorrectAction(ctx *ActionContext, cands []schema.SearchResult) []schema.SearchResult {
	logInfof("CRAG CorrectAction: high relevance, using %d documents directly", len(cands))

	// Optionally refine documents for better quality
	if ctx != nil && ctx.Refiner != nil && ctx.Refiner.Provider != nil && ctx.Context != nil {
		refined := make([]schema.SearchResult, 0, len(cands))
		for _, result := range cands {
			refinedContent, err := ctx.Refiner.Refine(ctx.Context, result.Document.Content)
			if err == nil && refinedContent != "" {
				result.Document.Content = refinedContent
			}
			refined = append(refined, result)
		}
		return refined
	}

	return cands
}

// IncorrectAction handles low-relevance scenario: documents are not relevant, perform web search.
// Returns empty slice if web search is not available or configured.
func IncorrectAction(ctx *ActionContext) []schema.SearchResult {
	logInfof("CRAG IncorrectAction: low relevance, attempting web search")

	if ctx == nil || ctx.WebSearcher == nil || ctx.Query == "" || ctx.Context == nil {
		logWarnf("CRAG IncorrectAction: web search not configured, returning empty results")
		return []schema.SearchResult{}
	}

	// Rewrite query for better web search results
	searchQuery := ctx.Query
	if ctx.QueryRewriter != nil && ctx.QueryRewriter.Provider != nil {
		rewritten, err := ctx.QueryRewriter.Rewrite(ctx.Context, ctx.Query)
		if err == nil && rewritten != "" {
			searchQuery = rewritten
		}
	}

	// Perform web search
	webResults, err := ctx.WebSearcher.Search(ctx.Context, searchQuery, 3)
	if err != nil {
		logWarnf("CRAG IncorrectAction: web search failed: %v", err)
		return []schema.SearchResult{}
	}

	// Optionally refine web search results
	if ctx.Refiner != nil && ctx.Refiner.Provider != nil {
		refined := make([]schema.SearchResult, 0, len(webResults))
		for _, result := range webResults {
			refinedContent, err := ctx.Refiner.Refine(ctx.Context, result.Document.Content)
			if err == nil && refinedContent != "" {
				result.Document.Content = refinedContent
			}
			refined = append(refined, result)
		}
		return refined
	}

	logInfof("CRAG IncorrectAction: returning %d web search results", len(webResults))
	return webResults
}

// AmbiguousAction handles medium-relevance scenario: combine internal docs with external web search.
// If external results are provided, use them; otherwise perform web search.
func AmbiguousAction(ctx *ActionContext, internal []schema.SearchResult, external []schema.SearchResult) []schema.SearchResult {
	logInfof("CRAG AmbiguousAction: medium relevance, combining internal (%d) and external sources", len(internal))

	// If no external results provided and we have web search capability, fetch them
	if len(external) == 0 && ctx != nil && ctx.WebSearcher != nil && ctx.Query != "" && ctx.Context != nil {
		// Rewrite query for better web search results
		searchQuery := ctx.Query
		if ctx.QueryRewriter != nil && ctx.QueryRewriter.Provider != nil {
			rewritten, err := ctx.QueryRewriter.Rewrite(ctx.Context, ctx.Query)
			if err == nil && rewritten != "" {
				searchQuery = rewritten
			}
		}

		// Perform web search
		webResults, err := ctx.WebSearcher.Search(ctx.Context, searchQuery, 3)
		if err == nil {
			external = webResults
		} else {
			logWarnf("CRAG AmbiguousAction: web search failed: %v", err)
		}
	}

	// If still no external results, just use internal (refined if possible)
	if len(external) == 0 {
		if ctx != nil && ctx.Refiner != nil && ctx.Refiner.Provider != nil && ctx.Context != nil {
			return CorrectAction(ctx, internal)
		}
		return internal
	}

	// Combine internal and external results
	combined := make([]schema.SearchResult, 0, len(internal)+len(external))

	// Refine internal results if refiner available
	if ctx != nil && ctx.Refiner != nil && ctx.Refiner.Provider != nil && ctx.Context != nil {
		for _, result := range internal {
			refinedContent, err := ctx.Refiner.Refine(ctx.Context, result.Document.Content)
			if err == nil && refinedContent != "" {
				result.Document.Content = refinedContent
				// Mark as refined
				if result.Document.Metadata == nil {
					result.Document.Metadata = make(map[string]interface{})
				}
				result.Document.Metadata["refined"] = true
			}
			combined = append(combined, result)
		}

		// Also refine external results
		for _, result := range external {
			refinedContent, err := ctx.Refiner.Refine(ctx.Context, result.Document.Content)
			if err == nil && refinedContent != "" {
				result.Document.Content = refinedContent
				if result.Document.Metadata == nil {
					result.Document.Metadata = make(map[string]interface{})
				}
				result.Document.Metadata["refined"] = true
			}
			combined = append(combined, result)
		}
	} else {
		// No refiner, just combine as is
		combined = append(combined, internal...)
		combined = append(combined, external...)
	}

	logInfof("CRAG AmbiguousAction: returning %d combined results", len(combined))
	return combined
}
