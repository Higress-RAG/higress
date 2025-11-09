package crag

import (
	"context"
	"fmt"
	"strings"

	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/llm"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/schema"
)

// QueryRewriter rewrites queries to be more suitable for web search engines.
type QueryRewriter struct {
	Provider llm.Provider
}

const rewriteSystemPrompt = `You are an expert at creating effective search queries.
Rewrite the given query to make it more suitable for a web search engine.
Focus on keywords and facts, remove unnecessary words, and make it concise.`

// Rewrite takes an original query and rewrites it for better search results.
func (r *QueryRewriter) Rewrite(ctx context.Context, originalQuery string) (string, error) {
	if r.Provider == nil {
		logWarnf("QueryRewriter: no LLM provider, returning original query")
		return originalQuery, nil
	}

	userPrompt := fmt.Sprintf("Original query: %s\n\nRewritten query:", originalQuery)
	fullPrompt := fmt.Sprintf("%s\n\n%s", rewriteSystemPrompt, userPrompt)

	response, err := r.Provider.GenerateCompletion(ctx, fullPrompt)
	if err != nil {
		logWarnf("QueryRewriter: failed to rewrite query: %v, using original", err)
		return originalQuery, err
	}

	rewritten := strings.TrimSpace(response)
	if rewritten == "" {
		return originalQuery, nil
	}

	logInfof("QueryRewriter: '%s' -> '%s'", originalQuery, rewritten)
	return rewritten, nil
}

// KnowledgeRefiner extracts and refines key information from text.
type KnowledgeRefiner struct {
	Provider llm.Provider
}

const refineSystemPrompt = `Extract the key information from the following text as a set of clear, concise bullet points.
Focus on the most relevant facts and important details.
Format your response as a bulleted list with each point on a new line starting with "• ".`

// Refine extracts key information from input text and returns refined bullet points.
func (kr *KnowledgeRefiner) Refine(ctx context.Context, text string) (string, error) {
	if kr.Provider == nil {
		logWarnf("KnowledgeRefiner: no LLM provider, returning original text")
		return text, nil
	}

	userPrompt := fmt.Sprintf("Text to refine:\n\n%s", text)
	fullPrompt := fmt.Sprintf("%s\n\n%s", refineSystemPrompt, userPrompt)

	response, err := kr.Provider.GenerateCompletion(ctx, fullPrompt)
	if err != nil {
		logWarnf("KnowledgeRefiner: failed to refine knowledge: %v, using original", err)
		return text, err
	}

	refined := strings.TrimSpace(response)
	if refined == "" {
		return text, nil
	}

	logInfof("KnowledgeRefiner: refined %d chars to %d chars", len(text), len(refined))
	return refined, nil
}

// CombineKnowledge combines internal (retrieved) and external (web search) knowledge.
func CombineKnowledge(internalResults []schema.SearchResult, externalResults []schema.SearchResult, refiner *KnowledgeRefiner, ctx context.Context) ([]schema.SearchResult, error) {
	combined := make([]schema.SearchResult, 0, len(internalResults)+len(externalResults))

	// Refine internal results if refiner is available
	if refiner != nil && refiner.Provider != nil {
		for _, result := range internalResults {
			refined, err := refiner.Refine(ctx, result.Document.Content)
			if err == nil && refined != "" {
				result.Document.Content = refined
			}
			combined = append(combined, result)
		}
	} else {
		combined = append(combined, internalResults...)
	}

	// Add external results
	combined = append(combined, externalResults...)

	return combined, nil
}

// ExtractContent extracts text content from search results for evaluation or refinement.
func ExtractContent(results []schema.SearchResult, limit int) string {
	if limit <= 0 || limit > len(results) {
		limit = len(results)
	}

	var b strings.Builder
	for i := 0; i < limit; i++ {
		b.WriteString(results[i].Document.Content)
		b.WriteString("\n\n")
	}
	return b.String()
}
