package fusion

import (
	"context"
	"errors"
	"hash/fnv"
	"sync"
	"time"

	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/schema"
	"github.com/envoyproxy/envoy/contrib/golang/common/go/api"
)

// LearnedOptions configures the learned fusion strategy.
type LearnedOptions struct {
	WeightsURI      string
	CacheTTL        time.Duration
	Timeout         time.Duration
	Fallback        Strategy
	Loader          *WeightsLoader
	RefreshInterval time.Duration
}

// LearnedStrategy implements fusion using externally learned weights.
type LearnedStrategy struct {
	opts      LearnedOptions
	loader    *WeightsLoader
	metadata  map[string]any
	metaMutex sync.RWMutex
}

// NewLearnedStrategy constructs a learned fusion strategy.
func NewLearnedStrategy(opts LearnedOptions) (*LearnedStrategy, error) {
	if opts.Loader == nil {
		if opts.WeightsURI == "" {
			return nil, errors.New("learned strategy requires weights_uri")
		}
		loader, err := NewWeightsLoader(opts.WeightsURI, opts.CacheTTL)
		if err != nil {
			return nil, err
		}
		opts.Loader = loader
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Millisecond
	}
	if opts.RefreshInterval <= 0 {
		opts.RefreshInterval = time.Minute
	}
	if opts.Fallback == nil {
		opts.Fallback = NewRRFStrategy(60)
	}

	strategy := &LearnedStrategy{
		opts:     opts,
		loader:   opts.Loader,
		metadata: map[string]any{},
	}

	return strategy, nil
}

// Fuse merges inputs using learned weights, with graceful fallback.
func (s *LearnedStrategy) Fuse(ctx context.Context, inputs []RetrieverResult, params map[string]any) ([]schema.SearchResult, error) {
	if s.loader == nil {
		return s.opts.Fallback.Fuse(ctx, inputs, params)
	}

	if params == nil {
		params = map[string]any{}
	}

	if !s.shouldActivate(params, inputs) {
		api.LogInfof("fusion: learned strategy skipped by traffic control")
		return s.opts.Fallback.Fuse(ctx, inputs, params)
	}

	timeout := s.opts.Timeout
	if t := lookupInt(params, "timeout_ms"); t > 0 {
		timeout = time.Duration(t) * time.Millisecond
	}

	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	snapshot, err := s.loader.Get(ctx)
	if err != nil {
		api.LogWarnf("fusion: learned weights unavailable, fallback to %s: %v", s.opts.Fallback.Name(), err)
		return s.opts.Fallback.Fuse(ctx, inputs, params)
	}

	weighted := NewWeightedStrategy(snapshot.Weights)
	results, err := weighted.Fuse(ctx, inputs, params)
	if err != nil {
		api.LogWarnf("fusion: weighted fusion error, fallback to %s: %v", s.opts.Fallback.Name(), err)
		return s.opts.Fallback.Fuse(ctx, inputs, params)
	}

	if snapshot.Bias != 0 {
		for i := range results {
			results[i].Score += snapshot.Bias
		}
	}

	s.storeMetadata(snapshot)

	return results, nil
}

// Name implements Strategy.
func (s *LearnedStrategy) Name() string {
	return "learned"
}

// Metadata returns the last fusion metadata.
func (s *LearnedStrategy) Metadata() map[string]any {
	s.metaMutex.RLock()
	defer s.metaMutex.RUnlock()
	out := make(map[string]any, len(s.metadata))
	for k, v := range s.metadata {
		out[k] = v
	}
	return out
}

func (s *LearnedStrategy) storeMetadata(snapshot *WeightSnapshot) {
	s.metaMutex.Lock()
	defer s.metaMutex.Unlock()
	s.metadata = map[string]any{
		"weights_version": snapshot.Version,
		"weights_bias":    snapshot.Bias,
		"weights_uri":     s.opts.WeightsURI,
		"strategy":        "learned",
		"fetched_at":      snapshot.Fetched,
	}
}

func (s *LearnedStrategy) shouldActivate(params map[string]any, inputs []RetrieverResult) bool {
	percent := lookupInt(params, "traffic_percent")
	if percent <= 0 || percent >= 100 {
		return true
	}

	hashSeed := ""
	if seed, ok := params["query_id"].(string); ok && seed != "" {
		hashSeed = seed
	} else if seed, ok := params["query"].(string); ok && seed != "" {
		hashSeed = seed
	} else if len(inputs) > 0 {
		hashSeed = inputs[0].Query
	}
	if hashSeed == "" {
		return true
	}

	h := fnv.New32a()
	if _, err := h.Write([]byte(hashSeed)); err != nil {
		return true
	}
	value := h.Sum32() % 100
	return int(value) < percent
}

var _ Strategy = (*LearnedStrategy)(nil)
var _ MetadataProvider = (*LearnedStrategy)(nil)
