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
	NeedWeb        bool                     `json:"need_web"`
	NeedVector     bool                     `json:"need_vector"`
	NeedBM25       bool                     `json:"need_bm25"`
	QueryType      string                   `json:"query_type"`     // factoid/comparison/temporal/open-ended
	Confidence     float64                  `json:"confidence"`     // Confidence score [0, 1]
	Reason         string                   `json:"reason"`         // Human-readable reason
	SuggestedTopK  int                      `json:"suggested_topk"` // Suggested TopK for this query
	ProfileName    string                   `json:"profile_name,omitempty"`
	VariantBudgets map[string]VariantBudget `json:"variant_budgets,omitempty"`
}

// VariantBudget defines per-variant routing budgets.
type VariantBudget struct {
	Enable bool `json:"enable"`
	TopK   int  `json:"top_k"`
}

// Router determines which retrievers to use for a given query
type Router interface {
	Route(ctx context.Context, query string) (*RoutingDecision, error)
}

// HTTPRouter routes queries using an external HTTP service
type HTTPRouter struct {
	Endpoint string
	Client   *httpx.Client
	rules    []config.RouterRule
}

// NewHTTPRouter creates a new HTTP-based router
func NewHTTPRouter(endpoint string, routerCfg *config.RouterConfig, httpCfg *config.HTTPClientConfig) *HTTPRouter {
	var rules []config.RouterRule
	if routerCfg != nil {
		rules = routerCfg.Rules
	}
	return &HTTPRouter{
		Endpoint: endpoint,
		Client:   httpx.NewFromConfig(httpCfg),
		rules:    rules,
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
	rb := NewRuleBasedRouter(r.rules)
	decision, _ := rb.Route(context.Background(), query)
	return decision
}

// RuleBasedRouter implements simple rule-based routing
type RuleBasedRouter struct {
	rules []config.RouterRule
}

// NewRuleBasedRouter creates a new rule-based router
func NewRuleBasedRouter(rules []config.RouterRule) *RuleBasedRouter {
	return &RuleBasedRouter{rules: rules}
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

	r.applyRules(decision)

	api.LogInfof("router: rule-based decision - web=%v vector=%v bm25=%v type=%s reason=%s",
		decision.NeedWeb, decision.NeedVector, decision.NeedBM25, decision.QueryType, decision.Reason)
	return decision, nil
}

func (r *RuleBasedRouter) applyRules(decision *RoutingDecision) {
	if decision == nil || len(r.rules) == 0 {
		return
	}
	for _, rule := range r.rules {
		if rule.Intent != "" && !strings.EqualFold(rule.Intent, decision.QueryType) {
			continue
		}
		if rule.Profile != "" {
			decision.ProfileName = rule.Profile
		} else if rule.Intent != "" {
			decision.ProfileName = rule.Intent
		}
		if budgets := buildVariantBudgets(rule); len(budgets) > 0 {
			decision.VariantBudgets = budgets
			for variant, budget := range budgets {
				if !budget.Enable {
					continue
				}
				switch variant {
				case "dense":
					decision.NeedVector = true
				case "sparse":
					decision.NeedBM25 = true
				case "web":
					decision.NeedWeb = true
				case "hyde":
					// HYDE signals downstream seed generation; no direct retriever toggle.
				}
			}
		}
		return
	}
}

func buildVariantBudgets(rule config.RouterRule) map[string]VariantBudget {
	enabled := make(map[string]bool)
	for _, variant := range rule.Enable {
		key := normalizeVariant(variant)
		if key != "" {
			enabled[key] = true
		}
	}
	if len(enabled) == 0 && len(rule.Budgets) == 0 {
		return nil
	}

	budgets := make(map[string]VariantBudget)
	for variant, topk := range rule.Budgets {
		key := normalizeVariant(variant)
		if key == "" {
			continue
		}
		budget := VariantBudget{
			Enable: enabled[key] || len(enabled) == 0,
			TopK:   topk,
		}
		budgets[key] = budget
	}

	for variant, on := range enabled {
		if !on {
			continue
		}
		if _, exists := budgets[variant]; !exists {
			budgets[variant] = VariantBudget{Enable: true}
		} else {
			b := budgets[variant]
			b.Enable = true
			budgets[variant] = b
		}
	}

	if len(budgets) == 0 {
		return nil
	}
	return budgets
}

func normalizeVariant(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}

func containsRetriever(retrievers []string, typ string) bool {
	typLower := strings.ToLower(strings.TrimSpace(typ))
	for _, r := range retrievers {
		if strings.Contains(strings.ToLower(r), typLower) {
			return true
		}
	}
	return false
}

// HybridRouter combines HTTP router with rule-based fallback
type HybridRouter struct {
	Primary  Router
	Fallback Router
}

// NewHybridRouter creates a hybrid router
func NewHybridRouter(primary, fallback Router) *HybridRouter {
	if fallback == nil {
		fallback = NewRuleBasedRouter(nil)
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

	if len(decision.VariantBudgets) > 0 {
		if profile.VariantBudgets == nil {
			profile.VariantBudgets = make(map[string]int, len(decision.VariantBudgets))
		} else {
			for k := range profile.VariantBudgets {
				delete(profile.VariantBudgets, k)
			}
		}
		for variant, budget := range decision.VariantBudgets {
			variantKey := normalizeVariant(variant)
			if !budget.Enable {
				continue
			}
			if budget.TopK > 0 {
				profile.VariantBudgets[variantKey] = budget.TopK
			} else {
				profile.VariantBudgets[variantKey] = 0
			}
			switch variantKey {
			case "dense":
				if !containsRetriever(profile.Retrievers, "vector") {
					profile.Retrievers = append(profile.Retrievers, "vector")
				}
				if topk := budget.TopK; topk > 0 && (profile.PerRetrieverTopK == 0 || topk < profile.PerRetrieverTopK) {
					profile.PerRetrieverTopK = topk
				}
			case "sparse":
				if !containsRetriever(profile.Retrievers, "bm25") {
					profile.Retrievers = append(profile.Retrievers, "bm25")
				}
			case "hyde":
				profile.HYDE.Enable = true
				if budget.TopK > 0 {
					profile.HYDE.MaxSeeds = budget.TopK
				}
			case "web":
				profile.UseWeb = true
				if !containsRetriever(profile.Retrievers, "web") {
					profile.Retrievers = append(profile.Retrievers, "web")
				}
			}
		}
	}

	return profile
}

// NewRouter creates a router based on configuration
func NewRouter(cfg *config.RouterConfig, httpCfg *config.HTTPClientConfig) Router {
	if cfg == nil {
		return NewRuleBasedRouter(nil)
	}

	switch cfg.Provider {
	case "http":
		if cfg.Endpoint != "" {
			return NewHTTPRouter(cfg.Endpoint, cfg, httpCfg)
		}
		return NewRuleBasedRouter(cfg.Rules)
	case "rule":
		return NewRuleBasedRouter(cfg.Rules)
	case "hybrid":
		var primary Router
		if cfg.Endpoint != "" {
			primary = NewHTTPRouter(cfg.Endpoint, cfg, httpCfg)
		}
		return NewHybridRouter(primary, NewRuleBasedRouter(cfg.Rules))
	default:
		return NewRuleBasedRouter(cfg.Rules)
	}
}
