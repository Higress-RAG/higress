package gating

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/config"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/metrics"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/retriever"
	"github.com/envoyproxy/envoy/contrib/golang/common/go/api"
)

// Provider handles gating decisions
type Provider interface {
	Evaluate(ctx context.Context, query string, profile config.RetrievalProfile, m *metrics.RetrievalMetrics) Decision
	ApplyDecision(decision Decision, profile config.RetrievalProfile) config.RetrievalProfile
}

// defaultProvider is the default implementation
type defaultProvider struct {
	vectorRetriever retriever.Retriever
}

// NewProvider creates a new gating provider
func NewProvider(vectorRetriever retriever.Retriever) Provider {
	return &defaultProvider{
		vectorRetriever: vectorRetriever,
	}
}

// Decision represents a gating decision
type Decision struct {
	ShouldSuppressWeb bool
	ShouldForceWeb    bool
	TopScore          float64
	Reason            string
}

// Evaluate performs vector-based gating and returns decision
func (p *defaultProvider) Evaluate(ctx context.Context, query string, profile config.RetrievalProfile, m *metrics.RetrievalMetrics) Decision {
	if p.vectorRetriever == nil {
		return Decision{Reason: "no_vector_retriever"}
	}

	// Check if gating is configured
	if profile.VectorGate <= 0 && profile.VectorLowGate <= 0 {
		return Decision{Reason: "gating_disabled"}
	}

	// Perform vector preflight
	preflightStart := time.Now()
	preflightResults, err := p.vectorRetriever.Search(ctx, query, 5)
	preflightLatency := time.Since(preflightStart).Milliseconds()

	if err != nil || len(preflightResults) == 0 {
		api.LogWarnf("gating: vector preflight failed: %v", err)
		return Decision{Reason: "preflight_failed"}
	}

	topScore := preflightResults[0].Score

	// Record preflight metrics
	if m != nil {
		m.AddRetrieverStats(metrics.RetrieverStats{
			Type:        "vector_preflight",
			LatencyMs:   preflightLatency,
			ResultCount: len(preflightResults),
			TopScore:    topScore,
		})
	}

	api.LogInfof("gating: vector_preflight top_score=%.4f (gate=%.4f low_gate=%.4f)",
		topScore, profile.VectorGate, profile.VectorLowGate)

	// Make decision
	decision := Decision{TopScore: topScore}

	// High score: suppress web
	if profile.VectorGate > 0 && topScore >= profile.VectorGate {
		if profile.UseWeb || containsRetriever(profile.Retrievers, "web") {
			decision.ShouldSuppressWeb = true
			decision.Reason = fmt.Sprintf("suppress_web:score=%.4f>=gate=%.4f", topScore, profile.VectorGate)
		}
	}

	// Low score: force web
	if profile.VectorLowGate > 0 && topScore < profile.VectorLowGate {
		if profile.ForceWebOnLow {
			if !profile.UseWeb && !containsRetriever(profile.Retrievers, "web") {
				decision.ShouldForceWeb = true
				decision.Reason = fmt.Sprintf("force_web:score=%.4f<low_gate=%.4f", topScore, profile.VectorLowGate)
			}
		} else {
			decision.Reason = fmt.Sprintf("low_score:score=%.4f<low_gate=%.4f,no_force", topScore, profile.VectorLowGate)
		}
	}

	// Neutral
	if decision.Reason == "" {
		decision.Reason = fmt.Sprintf("neutral:score=%.4f", topScore)
	}

	if m != nil {
		m.AddGatingDecision(decision.Reason)
	}

	api.LogInfof("gating: %s", decision.Reason)
	return decision
}

// ApplyDecision applies gating decision to profile
func (p *defaultProvider) ApplyDecision(decision Decision, profile config.RetrievalProfile) config.RetrievalProfile {
	if decision.ShouldSuppressWeb {
		profile.UseWeb = false
		profile.Retrievers = filterRetrievers(profile.Retrievers, "web")
	}

	if decision.ShouldForceWeb {
		profile.UseWeb = true
		if !containsRetriever(profile.Retrievers, "web") {
			profile.Retrievers = append(profile.Retrievers, "web")
		}
	}

	return profile
}

// containsRetriever checks if retriever list contains a type
func containsRetriever(retrievers []string, typ string) bool {
	typLower := strings.ToLower(typ)
	for _, r := range retrievers {
		if strings.Contains(strings.ToLower(r), typLower) {
			return true
		}
	}
	return false
}

// filterRetrievers filters out specific retriever type
func filterRetrievers(retrievers []string, typ string) []string {
	typLower := strings.ToLower(typ)
	filtered := make([]string, 0, len(retrievers))
	for _, r := range retrievers {
		if !strings.Contains(strings.ToLower(r), typLower) {
			filtered = append(filtered, r)
		}
	}
	return filtered
}
