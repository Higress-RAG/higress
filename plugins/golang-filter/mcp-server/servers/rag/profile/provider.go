package profile

import (
	"strings"

	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/config"
)

// Provider manages retrieval profile selection and normalization
type Provider interface {
	SelectByQuery(query string) config.RetrievalProfile
	SelectByIntent(intent string) config.RetrievalProfile
	SelectByName(name string) config.RetrievalProfile
	SelectDefault() config.RetrievalProfile
	Normalize(prof config.RetrievalProfile) config.RetrievalProfile
	ApplyConstraints(prof config.RetrievalProfile, latencyBudgetMs int32, urgencyLevel string) config.RetrievalProfile
	ApplyIntentRequirements(prof config.RetrievalProfile, requiresWeb bool, requiresMultiDoc bool) config.RetrievalProfile
}

// defaultProvider is the default implementation
type defaultProvider struct {
	profiles []config.RetrievalProfile
}

// NewProvider creates a new profile provider
func NewProvider(pipelineConfig *config.PipelineConfig) Provider {
	if pipelineConfig == nil {
		return &defaultProvider{profiles: []config.RetrievalProfile{}}
	}
	return &defaultProvider{
		profiles: pipelineConfig.RetrievalProfiles,
	}
}

// SelectByQuery selects a profile based on query characteristics
func (p *defaultProvider) SelectByQuery(query string) config.RetrievalProfile {
	// Future: implement query analysis and profile matching
	return p.SelectDefault()
}

// SelectByIntent selects a profile based on intent classification
func (p *defaultProvider) SelectByIntent(intent string) config.RetrievalProfile {
	if intent == "" {
		return config.RetrievalProfile{}
	}

	intentLower := strings.ToLower(strings.TrimSpace(intent))

	// Match by Intent field
	for _, prof := range p.profiles {
		if strings.EqualFold(prof.Intent, intentLower) {
			return p.Normalize(prof)
		}
	}

	return config.RetrievalProfile{}
}

// SelectByName selects a profile by name
func (p *defaultProvider) SelectByName(name string) config.RetrievalProfile {
	for _, prof := range p.profiles {
		if prof.Name == name {
			return p.Normalize(prof)
		}
	}
	return config.RetrievalProfile{}
}

// SelectDefault returns the first profile or a default one
func (p *defaultProvider) SelectDefault() config.RetrievalProfile {
	if len(p.profiles) > 0 {
		return p.Normalize(p.profiles[0])
	}

	// Fallback to baseline
	return p.Normalize(config.RetrievalProfile{
		Name:       "baseline",
		Retrievers: []string{"vector"},
		TopK:       10,
		Threshold:  0.5,
	})
}

// Normalize fills in default values for a profile
func (p *defaultProvider) Normalize(prof config.RetrievalProfile) config.RetrievalProfile {
	if prof.TopK == 0 {
		prof.TopK = 10
	}
	if prof.Threshold == 0 {
		prof.Threshold = 0.5
	}
	if len(prof.Retrievers) == 0 {
		prof.Retrievers = []string{"vector"}
	}
	if prof.PerRetrieverTopK == 0 {
		prof.PerRetrieverTopK = prof.TopK
	}
	return prof
}

// ApplyConstraints applies execution constraints to a profile
func (p *defaultProvider) ApplyConstraints(prof config.RetrievalProfile, latencyBudgetMs int32, urgencyLevel string) config.RetrievalProfile {
	// Latency budget adjustments
	if latencyBudgetMs > 0 {
		if latencyBudgetMs < 100 {
			// Low budget: vector only, limit TopK
			prof.Retrievers = []string{"vector"}
			prof.MaxFanout = 1
			if prof.TopK > 5 {
				prof.TopK = 5
			}
		} else if latencyBudgetMs < 300 {
			// Medium budget: vector + bm25, no web
			prof.Retrievers = []string{"vector", "bm25"}
			prof.MaxFanout = 2
			prof.UseWeb = false
		}
	}

	// Urgency adjustments
	urgencyUpper := strings.ToUpper(strings.TrimSpace(urgencyLevel))
	if strings.Contains(urgencyUpper, "CRITICAL") || strings.Contains(urgencyUpper, "ELEVATED") {
		if prof.TopK > 10 {
			prof.TopK = 10
		}
		prof.UseWeb = false
	}

	return prof
}

// ApplyIntentRequirements applies intent-specific requirements to profile
func (p *defaultProvider) ApplyIntentRequirements(prof config.RetrievalProfile, requiresWeb bool, requiresMultiDoc bool) config.RetrievalProfile {
	prof.UseWeb = requiresWeb

	if requiresMultiDoc && prof.TopK < 15 {
		prof.TopK = 15
	}

	return prof
}
