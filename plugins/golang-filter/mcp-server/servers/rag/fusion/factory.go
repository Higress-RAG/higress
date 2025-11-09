package fusion

import (
	"errors"
	"strings"
	"time"
)

// NewStrategy constructs a strategy by name. It returns the strategy and a sanitized param map.
func NewStrategy(name string, params map[string]any) (Strategy, map[string]any, error) {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if normalized == "" {
		normalized = "rrf"
	}
	if params == nil {
		params = map[string]any{}
	}

	switch normalized {
	case "rrf":
		k := lookupInt(params, "k")
		if k <= 0 {
			k = 60
		}
		return NewRRFStrategy(k), map[string]any{"k": k}, nil
	case "weighted":
		weights, _ := parseStringFloatMap(params["weights"])
		return NewWeightedStrategy(weights), map[string]any{"weights": weights}, nil
	case "linear":
		weights, _ := parseFloatSlice(params["weights"])
		return NewLinearCombinationStrategy(weights), map[string]any{"weights": weights}, nil
	case "distribution":
		baseName := "rrf"
		if v, ok := params["base"].(string); ok && v != "" {
			baseName = v
		}
		base, _, err := NewStrategy(baseName, params)
		if err != nil {
			return nil, nil, err
		}
		return NewDistributionBasedStrategy(base), params, nil
	case "learned":
		opts := LearnedOptions{
			WeightsURI: toString(params["weights_uri"]),
			Timeout:    time.Duration(lookupInt(params, "timeout_ms")) * time.Millisecond,
			CacheTTL:   time.Duration(lookupInt(params, "refresh_seconds")) * time.Second,
		}
		fallbackName := params["fallback"]
		fallbackStrategyName := "rrf"
		if s, ok := fallbackName.(string); ok && s != "" {
			fallbackStrategyName = s
		}
		fallback, fallbackParams, err := NewStrategy(fallbackStrategyName, params)
		if err != nil {
			return nil, nil, err
		}
		opts.Fallback = fallback
		if opts.CacheTTL <= 0 {
			opts.CacheTTL = time.Minute
		}
		if opts.Timeout <= 0 {
			opts.Timeout = 10 * time.Millisecond
		}
		strategy, err := NewLearnedStrategy(opts)
		if err != nil {
			return nil, nil, err
		}
		sanitized := map[string]any{
			"weights_uri":     opts.WeightsURI,
			"timeout_ms":      int(opts.Timeout / time.Millisecond),
			"refresh_seconds": int(opts.CacheTTL / time.Second),
			"fallback":        fallbackStrategyName,
		}
		for k, v := range fallbackParams {
			sanitized["fallback_"+k] = v
		}
		if percent := lookupInt(params, "traffic_percent"); percent > 0 {
			sanitized["traffic_percent"] = percent
		}
		return strategy, sanitized, nil
	default:
		return nil, nil, errors.New("unsupported fusion strategy: " + normalized)
	}
}

func toString(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case []byte:
		return string(val)
	default:
		return ""
	}
}
