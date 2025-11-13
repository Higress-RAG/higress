package config

// PipelineConfig defines the optional enhanced RAG pipeline configuration.
// All fields are optional and default to disabled for safety in gateway hot paths.
type PipelineConfig struct {
	EnablePre    bool `json:"enable_pre,omitempty" yaml:"enable_pre,omitempty"`
	EnableHybrid bool `json:"enable_hybrid,omitempty" yaml:"enable_hybrid,omitempty"`
	EnablePost   bool `json:"enable_post,omitempty" yaml:"enable_post,omitempty"`
	EnableCRAG   bool `json:"enable_crag,omitempty" yaml:"enable_crag,omitempty"`

	// RRF fusion parameter for hybrid retrieval; typical default 60
	RRFK int `json:"rrf_k,omitempty" yaml:"rrf_k,omitempty"`

	// Fusion strategy configuration
	Fusion *FusionConfig `json:"fusion,omitempty" yaml:"fusion,omitempty"`
	// Query router configuration
	Router *RouterConfig `json:"router,omitempty" yaml:"router,omitempty"`

	// Pre stage configuration (deprecated, use PreRetrieve for full features)
	Pre *PreConfig `json:"pre,omitempty" yaml:"pre,omitempty"`
	// Advanced Pre-Retrieve configuration (complete PreQRAG implementation)
	PreRetrieve *PreRetrieveConfig `json:"pre_retrieve,omitempty" yaml:"pre_retrieve,omitempty"`
	// Retrieval backends
	Retrievers []RetrieverConfig `json:"retrievers,omitempty" yaml:"retrievers,omitempty"`
	// Retrieval profiles define strategy per intent.
	RetrievalProfiles []RetrievalProfile `json:"retrieval_profiles,omitempty" yaml:"retrieval_profiles,omitempty"`
	DefaultProfile    string             `json:"default_profile,omitempty" yaml:"default_profile,omitempty"`
	// Post stage configuration
	Post *PostConfig `json:"post,omitempty" yaml:"post,omitempty"`
	// CRAG configuration
	CRAG *CRAGConfig `json:"crag,omitempty" yaml:"crag,omitempty"`
	// Session store configuration (optional). If nil or store=inmemory, use in-memory store.
	Session *SessionConfig `json:"session,omitempty" yaml:"session,omitempty"`
	// HTTP global defaults for outbound calls (retrievers, reranker, evaluator, web search).
	HTTP *HTTPClientConfig `json:"http,omitempty" yaml:"http,omitempty"`
	// Feedback config for adaptive retrieval.
	Feedback *FeedbackConfig `json:"feedback,omitempty" yaml:"feedback,omitempty"`
	// Cache controls L1 caching of retrieval results.
	Cache *CacheConfig `json:"cache,omitempty" yaml:"cache,omitempty"`
}

type PreConfig struct {
	Classifier struct {
		EnableRules bool `json:"enable_rules,omitempty" yaml:"enable_rules,omitempty"`
		EnableModel bool `json:"enable_model,omitempty" yaml:"enable_model,omitempty"`
	} `json:"classifier" yaml:"classifier"`
	Rewrite struct {
		Enable   bool     `json:"enable,omitempty" yaml:"enable,omitempty"`
		Variants []string `json:"variants,omitempty" yaml:"variants,omitempty"`
	} `json:"rewrite" yaml:"rewrite"`
	Decompose struct {
		Enable bool `json:"enable,omitempty" yaml:"enable,omitempty"`
	} `json:"decompose" yaml:"decompose"`
	// Service defines an external preprocessor endpoint to produce
	// structured outputs (intents/entities/transformations/decomposition).
	Service struct {
		// Provider: "grpc" or "http" (json over http)
		Provider string `json:"provider,omitempty" yaml:"provider,omitempty"`
		// Endpoint: host:port for grpc, or full URL for http
		Endpoint string `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`
	} `json:"service" yaml:"service"`
}

// RetrieverConfig registers one retrieval backend instance.
// Type examples: "vector", "bm25", "web".
type RetrieverConfig struct {
	Type     string `json:"type" yaml:"type"`
	Provider string `json:"provider,omitempty" yaml:"provider,omitempty"`
	// Arbitrary key/values for the provider implementation, e.g., endpoints/index/collection.
	Params map[string]string `json:"params,omitempty" yaml:"params,omitempty"`
}

// RetrievalProfile describes a strategy for a specific intent or query class.
type RetrievalProfile struct {
	Name            string   `json:"name" yaml:"name"`
	Intent          string   `json:"intent,omitempty" yaml:"intent,omitempty"`
	Retrievers      []string `json:"retrievers,omitempty" yaml:"retrievers,omitempty"`
	TopK            int      `json:"top_k,omitempty" yaml:"top_k,omitempty"`
	Threshold       float64  `json:"threshold,omitempty" yaml:"threshold,omitempty"`
	UseWeb          bool     `json:"use_web,omitempty" yaml:"use_web,omitempty"`
	LatencyBudgetMs int      `json:"latency_budget_ms,omitempty" yaml:"latency_budget_ms,omitempty"`
	// MaxFanout caps concurrent retriever fan-out for this profile (0 => no cap)
	MaxFanout int `json:"max_fanout,omitempty" yaml:"max_fanout,omitempty"`
	// VectorGate: if vector Top1 score >= this threshold, skip web retriever
	VectorGate float64 `json:"vector_gate,omitempty" yaml:"vector_gate,omitempty"`
	// VectorLowGate: if vector Top1 score < this threshold, force-enable web retriever (if available)
	VectorLowGate float64 `json:"vector_low_gate,omitempty" yaml:"vector_low_gate,omitempty"`
	// ForceWebOnLow: when true and vector Top1 < VectorLowGate, ensure web retriever is used
	ForceWebOnLow bool `json:"force_web_on_low,omitempty" yaml:"force_web_on_low,omitempty"`
	// PerRetrieverTopK: cap TopK per retriever; 0 => use TopK
	PerRetrieverTopK int            `json:"per_retriever_top_k,omitempty" yaml:"per_retriever_top_k,omitempty"`
	Cascade          CascadeConfig  `json:"cascade,omitempty" yaml:"cascade,omitempty"`
	HYDE             HYDEConfig     `json:"hyde,omitempty" yaml:"hyde,omitempty"`
	VariantBudgets   map[string]int `json:"variant_budgets,omitempty" yaml:"variant_budgets,omitempty"`
}

type CascadeConfig struct {
	Enable          bool               `json:"enable,omitempty" yaml:"enable,omitempty"`
	LatencyBudgetMs int                `json:"latency_budget_ms,omitempty" yaml:"latency_budget_ms,omitempty"`
	Stage1          CascadeStageConfig `json:"stage1,omitempty" yaml:"stage1,omitempty"`
	Stage2          CascadeStageConfig `json:"stage2,omitempty" yaml:"stage2,omitempty"`
}

type CascadeStageConfig struct {
	Retriever string `json:"retriever,omitempty" yaml:"retriever,omitempty"`
	TopK      int    `json:"top_k,omitempty" yaml:"top_k,omitempty"`
	Mode      string `json:"mode,omitempty" yaml:"mode,omitempty"`
}

type HYDEConfig struct {
	Enable    bool   `json:"enable,omitempty" yaml:"enable,omitempty"`
	Provider  string `json:"provider,omitempty" yaml:"provider,omitempty"`
	Endpoint  string `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`
	MaxSeeds  int    `json:"max_seeds,omitempty" yaml:"max_seeds,omitempty"`
	TimeoutMs int    `json:"timeout_ms,omitempty" yaml:"timeout_ms,omitempty"`
}

type FeedbackConfig struct {
	Window      int                 `json:"window,omitempty" yaml:"window,omitempty"`
	Thresholds  FeedbackThresholds  `json:"thresholds,omitempty" yaml:"thresholds,omitempty"`
	Adjustments FeedbackAdjustments `json:"adjustments,omitempty" yaml:"adjustments,omitempty"`
	CooldownSec int                 `json:"cooldown_seconds,omitempty" yaml:"cooldown_seconds,omitempty"`
}

type FeedbackThresholds struct {
	Incorrect int `json:"incorrect,omitempty" yaml:"incorrect,omitempty"`
	Ambiguous int `json:"ambiguous,omitempty" yaml:"ambiguous,omitempty"`
	Confident int `json:"confident,omitempty" yaml:"confident,omitempty"`
}

type FeedbackAdjustments struct {
	TopKStep            int  `json:"topk_step,omitempty" yaml:"topk_step,omitempty"`
	TopKMax             int  `json:"topk_max,omitempty" yaml:"topk_max,omitempty"`
	EnableForceWebOnLow bool `json:"enable_force_web_on_low,omitempty" yaml:"enable_force_web_on_low,omitempty"`
}

type CacheConfig struct {
	L1 *CacheLayerConfig `json:"l1,omitempty" yaml:"l1,omitempty"`
}

type CacheLayerConfig struct {
	Enable     bool   `json:"enable,omitempty" yaml:"enable,omitempty"`
	MaxEntries int    `json:"max_entries,omitempty" yaml:"max_entries,omitempty"`
	TTLSeconds int    `json:"ttl_seconds,omitempty" yaml:"ttl_seconds,omitempty"`
	Store      string `json:"store,omitempty" yaml:"store,omitempty"`
	Mode       string `json:"mode,omitempty" yaml:"mode,omitempty"`
}

type PostConfig struct {
	Rerank struct {
		Enable   bool   `json:"enable,omitempty" yaml:"enable,omitempty"`
		Provider string `json:"provider,omitempty" yaml:"provider,omitempty"` // "http", "llm", "keyword", "model"
		Endpoint string `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`
		TopN     int    `json:"top_n,omitempty" yaml:"top_n,omitempty"`
		Model    string `json:"model,omitempty" yaml:"model,omitempty"`     // For model-based reranker
		APIKey   string `json:"api_key,omitempty" yaml:"api_key,omitempty"` // For model-based reranker
	} `json:"rerank" yaml:"rerank"`
	Compress struct {
		Enable      bool              `json:"enable,omitempty" yaml:"enable,omitempty"`
		Method      string            `json:"method,omitempty" yaml:"method,omitempty"`
		TargetRatio float64           `json:"target_ratio,omitempty" yaml:"target_ratio,omitempty"`
		Endpoint    string            `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`
		Headers     map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
	} `json:"compress" yaml:"compress"`
}

type CRAGConfig struct {
	Evaluator struct {
		Provider  string  `json:"provider,omitempty" yaml:"provider,omitempty"`
		Endpoint  string  `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`
		Correct   float64 `json:"correct,omitempty" yaml:"correct,omitempty"`
		Incorrect float64 `json:"incorrect,omitempty" yaml:"incorrect,omitempty"`
	} `json:"evaluator" yaml:"evaluator"`
	// Strict mode: if true, external evaluator is required and no heuristic fallback is allowed.
	Strict bool `json:"strict,omitempty" yaml:"strict,omitempty"`
	// FailMode controls behavior when evaluator fails: "open" (default) keeps fused results, "closed" returns error.
	FailMode string `json:"fail_mode,omitempty" yaml:"fail_mode,omitempty"`
	MaxIters int    `json:"max_iters,omitempty" yaml:"max_iters,omitempty"`
}

// SessionConfig controls session persistence.
// Store: "inmemory" (default) or "redis".
// Redis: map with keys {address,username,password,db,secret}
type SessionConfig struct {
	Store      string                 `json:"store,omitempty" yaml:"store,omitempty"`
	TTLSeconds int                    `json:"ttl_seconds,omitempty" yaml:"ttl_seconds,omitempty"`
	Redis      map[string]interface{} `json:"redis,omitempty" yaml:"redis,omitempty"`
}

// HTTPClientConfig defines common options for outbound HTTP calls.
type HTTPClientConfig struct {
	TimeoutMs              int      `json:"timeout_ms,omitempty" yaml:"timeout_ms,omitempty"`
	Retry                  int      `json:"retry,omitempty" yaml:"retry,omitempty"`
	BackoffMinMs           int      `json:"backoff_min_ms,omitempty" yaml:"backoff_min_ms,omitempty"`
	BackoffMaxMs           int      `json:"backoff_max_ms,omitempty" yaml:"backoff_max_ms,omitempty"`
	HostAllowlist          []string `json:"host_allowlist,omitempty" yaml:"host_allowlist,omitempty"`
	MaxConsecutiveFailures int      `json:"max_consecutive_failures,omitempty" yaml:"max_consecutive_failures,omitempty"`
	CircuitOpenSeconds     int      `json:"circuit_open_seconds,omitempty" yaml:"circuit_open_seconds,omitempty"`
}

// FusionConfig defines the fusion strategy configuration
type FusionConfig struct {
	// Strategy: "rrf" (default), "weighted", "linear", "distribution"
	Strategy string `json:"strategy,omitempty" yaml:"strategy,omitempty"`
	// Params: strategy-specific parameters (e.g., weights, k value)
	Params map[string]interface{} `json:"params,omitempty" yaml:"params,omitempty"`
	// EnableLearned toggles the learned fusion strategy rollout.
	EnableLearned bool `json:"enable_learned,omitempty" yaml:"enable_learned,omitempty"`
	// Fallback defines the fallback strategy name for learned fusion.
	Fallback string `json:"fallback,omitempty" yaml:"fallback,omitempty"`
	// WeightsURI is the location of the learned weights document.
	WeightsURI string `json:"weights_uri,omitempty" yaml:"weights_uri,omitempty"`
	// TimeoutMs caps learned fusion weight loading latency.
	TimeoutMs int `json:"timeout_ms,omitempty" yaml:"timeout_ms,omitempty"`
	// TrafficPercent controls the traffic percentage for learned fusion rollout.
	TrafficPercent int `json:"traffic_percent,omitempty" yaml:"traffic_percent,omitempty"`
	// RefreshSeconds overrides the default weight cache TTL.
	RefreshSeconds int `json:"refresh_seconds,omitempty" yaml:"refresh_seconds,omitempty"`
}

// RouterConfig defines the query routing configuration
type RouterConfig struct {
	// Provider: "rule" (default), "http", "hybrid"
	Provider string `json:"provider,omitempty" yaml:"provider,omitempty"`
	// Endpoint: HTTP endpoint for external routing service
	Endpoint string `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`
	// Enable: whether to enable query routing
	Enable bool `json:"enable,omitempty" yaml:"enable,omitempty"`
	// Rules define intent/variant routing overrides.
	Rules []RouterRule `json:"rules,omitempty" yaml:"rules,omitempty"`
}

type RouterRule struct {
	Intent  string         `json:"intent,omitempty" yaml:"intent,omitempty"`
	Profile string         `json:"profile,omitempty" yaml:"profile,omitempty"`
	Enable  []string       `json:"enable,omitempty" yaml:"enable,omitempty"`
	Budgets map[string]int `json:"budgets,omitempty" yaml:"budgets,omitempty"`
}

// DefaultPipeline returns a safe default pipeline configuration.
func DefaultPipeline() *PipelineConfig {
	return &PipelineConfig{
		EnablePre:    false,
		EnableHybrid: false,
		EnablePost:   false,
		EnableCRAG:   false,
		RRFK:         60,
		Fusion:       &FusionConfig{Strategy: "rrf", Params: map[string]interface{}{"k": 60}},
		Post:         &PostConfig{},
		CRAG:         &CRAGConfig{},
	}
}
