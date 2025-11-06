package router

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/common/httpx"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/config"
	"github.com/envoyproxy/envoy/contrib/golang/common/go/api"
)

// RoutingDecision represents the routing decision for a query
type RoutingDecision struct {
	NeedWeb       bool    `json:"need_web"`
	NeedVector    bool    `json:"need_vector"`
	NeedBM25      bool    `json:"need_bm25"`
	QueryType     string  `json:"query_type"`     // factoid/comparison/temporal/open-ended
	Confidence    float64 `json:"confidence"`     // Confidence score [0, 1]
	Reason        string  `json:"reason"`         // Human-readable reason
	SuggestedTopK int     `json:"suggested_topk"` // Suggested TopK for this query
}

// Router determines which retrievers to use for a given query
type Router interface {
	Route(ctx context.Context, query string) (*RoutingDecision, error)
}

// HTTPRouter routes queries using an external HTTP service
type HTTPRouter struct {
	Endpoint string
	Client   *httpx.Client
}

// NewHTTPRouter creates a new HTTP-based router
func NewHTTPRouter(endpoint string) *HTTPRouter {
	return &HTTPRouter{
		Endpoint: endpoint,
		Client:   httpx.NewFromConfig(nil),
	}
}

type routeRequest struct {
	Query string `json:"query"`
}

// Route calls external routing service
func (r *HTTPRouter) Route(ctx context.Context, query string) (*RoutingDecision, error) {
	req := routeRequest{Query: query}
	body, _ := json.Marshal(req)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, r.Endpoint, bytes.NewReader(body))
	if err != nil {
		api.LogWarnf("router: failed to create request: %v", err)
		return r.fallbackRuleBased(query), nil
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := r.Client.Do(httpReq)
	if err != nil {
		api.LogWarnf("router: HTTP request failed: %v", err)
		return r.fallbackRuleBased(query), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		api.LogWarnf("router: unexpected status code: %d", resp.StatusCode)
		return r.fallbackRuleBased(query), nil
	}

	var decision RoutingDecision
	if err := json.NewDecoder(resp.Body).Decode(&decision); err != nil {
		api.LogWarnf("router: failed to decode response: %v", err)
		return r.fallbackRuleBased(query), nil
	}

	api.LogInfof("router: decision from HTTP service - web=%v vector=%v bm25=%v type=%s confidence=%.2f",
		decision.NeedWeb, decision.NeedVector, decision.NeedBM25, decision.QueryType, decision.Confidence)
	return &decision, nil
}

// fallbackRuleBased provides rule-based routing as fallback
func (r *HTTPRouter) fallbackRuleBased(query string) *RoutingDecision {
	rb := &RuleBasedRouter{}
	decision, _ := rb.Route(context.Background(), query)
	return decision
}

// RuleBasedRouter implements simple rule-based routing
type RuleBasedRouter struct{}

// NewRuleBasedRouter creates a new rule-based router
func NewRuleBasedRouter() *RuleBasedRouter {
	return &RuleBasedRouter{}
}

// Route applies rule-based logic to determine routing
func (r *RuleBasedRouter) Route(ctx context.Context, query string) (*RoutingDecision, error) {
	decision := &RoutingDecision{
		NeedVector:    true, // Always use vector by default
		NeedBM25:      false,
		NeedWeb:       false,
		QueryType:     "factoid",
		Confidence:    0.6, // Medium confidence for rules
		SuggestedTopK: 10,
	}

	queryLower := strings.ToLower(query)
	queryLen := len(strings.Fields(query))

	// Temporal queries: need web search for current information
	temporalKeywords := []string{
		"latest", "newest", "recent", "current", "today", "now", "2024", "2025",
		"最新", "最近", "当前", "今天", "现在",
	}
	for _, kw := range temporalKeywords {
		if strings.Contains(queryLower, kw) {
			decision.NeedWeb = true
			decision.QueryType = "temporal"
			decision.Reason = "detected temporal keywords requiring up-to-date information"
			decision.SuggestedTopK = 15
			break
		}
	}

	// Comparison queries: benefit from BM25 keyword matching
	comparisonKeywords := []string{
		"compare", "difference", "versus", "vs", "better", "best",
		"比较", "区别", "对比", "哪个好",
	}
	for _, kw := range comparisonKeywords {
		if strings.Contains(queryLower, kw) {
			decision.NeedBM25 = true
			decision.QueryType = "comparison"
			decision.Reason = "detected comparison requiring keyword matching"
			decision.SuggestedTopK = 12
			break
		}
	}

	// Open-ended or exploratory queries: use multiple retrievers
	openKeywords := []string{
		"explain", "how", "why", "what is", "tell me about",
		"解释", "如何", "为什么", "什么是", "介绍",
	}
	for _, kw := range openKeywords {
		if strings.Contains(queryLower, kw) {
			decision.QueryType = "open-ended"
			decision.NeedBM25 = true
			decision.Reason = "open-ended query benefits from hybrid retrieval"
			decision.SuggestedTopK = 15
			break
		}
	}

	// Long queries (> 15 words): likely complex, use hybrid approach
	if queryLen > 15 {
		decision.NeedBM25 = true
		decision.QueryType = "complex"
		decision.Reason = "long query suggesting complex information need"
		decision.SuggestedTopK = 20
	}

	// Short queries (<= 3 words): likely factoid, vector is usually sufficient
	if queryLen <= 3 && decision.QueryType == "factoid" {
		decision.Reason = "short factoid query, vector retrieval sufficient"
		decision.SuggestedTopK = 5
	}

	// Question marks suggest information-seeking: enhance coverage
	if strings.Contains(query, "?") || strings.Contains(query, "？") {
		if !decision.NeedBM25 {
			decision.NeedBM25 = true
			decision.SuggestedTopK += 3
		}
	}

	api.LogInfof("router: rule-based decision - web=%v vector=%v bm25=%v type=%s reason=%s",
		decision.NeedWeb, decision.NeedVector, decision.NeedBM25, decision.QueryType, decision.Reason)
	return decision, nil
}

// HybridRouter combines HTTP router with rule-based fallback
type HybridRouter struct {
	Primary  Router
	Fallback Router
}

// NewHybridRouter creates a hybrid router
func NewHybridRouter(primary, fallback Router) *HybridRouter {
	if fallback == nil {
		fallback = NewRuleBasedRouter()
	}
	return &HybridRouter{
		Primary:  primary,
		Fallback: fallback,
	}
}

// Route tries primary router, falls back to secondary on failure
func (r *HybridRouter) Route(ctx context.Context, query string) (*RoutingDecision, error) {
	if r.Primary != nil {
		decision, err := r.Primary.Route(ctx, query)
		if err == nil && decision != nil {
			return decision, nil
		}
		api.LogWarnf("router: primary router failed, using fallback")
	}

	if r.Fallback != nil {
		return r.Fallback.Route(ctx, query)
	}

	// Ultimate fallback: enable everything
	return &RoutingDecision{
		NeedVector:    true,
		NeedBM25:      true,
		NeedWeb:       false,
		QueryType:     "unknown",
		Confidence:    0.5,
		Reason:        "all routers unavailable, using safe defaults",
		SuggestedTopK: 10,
	}, nil
}

// ApplyDecision applies routing decision to retrieval profile
func ApplyDecision(decision *RoutingDecision, profile config.RetrievalProfile) config.RetrievalProfile {
	if decision == nil {
		return profile
	}

	// Clear existing retrievers and rebuild based on decision
	profile.Retrievers = []string{}

	if decision.NeedVector {
		profile.Retrievers = append(profile.Retrievers, "vector")
	}
	if decision.NeedBM25 {
		profile.Retrievers = append(profile.Retrievers, "bm25")
	}
	if decision.NeedWeb {
		profile.Retrievers = append(profile.Retrievers, "web")
		profile.UseWeb = true
	} else {
		profile.UseWeb = false
	}

	// Apply suggested TopK if reasonable
	if decision.SuggestedTopK > 0 && decision.SuggestedTopK <= 100 {
		profile.TopK = decision.SuggestedTopK
	}

	return profile
}

// NewRouter creates a router based on configuration
func NewRouter(cfg *config.RouterConfig) Router {
	if cfg == nil {
		return NewRuleBasedRouter()
	}

	switch cfg.Provider {
	case "http":
		if cfg.Endpoint != "" {
			return NewHTTPRouter(cfg.Endpoint)
		}
		return NewRuleBasedRouter()
	case "rule":
		return NewRuleBasedRouter()
	case "hybrid":
		var primary Router
		if cfg.Endpoint != "" {
			primary = NewHTTPRouter(cfg.Endpoint)
		}
		return NewHybridRouter(primary, NewRuleBasedRouter())
	default:
		return NewRuleBasedRouter()
	}
}

