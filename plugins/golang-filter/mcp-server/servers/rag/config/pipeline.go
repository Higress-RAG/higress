package config

// PipelineConfig defines the optional enhanced RAG pipeline configuration.
// All fields are optional and default to disabled for safety in gateway hot paths.
type PipelineConfig struct {
    EnablePre    bool           `json:"enable_pre,omitempty" yaml:"enable_pre,omitempty"`
    EnableHybrid bool           `json:"enable_hybrid,omitempty" yaml:"enable_hybrid,omitempty"`
    EnablePost   bool           `json:"enable_post,omitempty" yaml:"enable_post,omitempty"`
    EnableCRAG   bool           `json:"enable_crag,omitempty" yaml:"enable_crag,omitempty"`

    // RRF fusion parameter for hybrid retrieval; typical default 60
    RRFK int `json:"rrf_k,omitempty" yaml:"rrf_k,omitempty"`

    // Pre stage configuration
    Pre *PreConfig `json:"pre,omitempty" yaml:"pre,omitempty"`
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
}

// RetrieverConfig registers one retrieval backend instance.
// Type examples: "vector", "bm25", "web".
type RetrieverConfig struct {
    Type     string            `json:"type" yaml:"type"`
    Provider string            `json:"provider,omitempty" yaml:"provider,omitempty"`
    // Arbitrary key/values for the provider implementation, e.g., endpoints/index/collection.
    Params map[string]string `json:"params,omitempty" yaml:"params,omitempty"`
}

// RetrievalProfile describes a strategy for a specific intent or query class.
type RetrievalProfile struct {
    Name       string   `json:"name" yaml:"name"`
    Intent     string   `json:"intent,omitempty" yaml:"intent,omitempty"`
    Retrievers []string `json:"retrievers,omitempty" yaml:"retrievers,omitempty"`
    TopK       int      `json:"top_k,omitempty" yaml:"top_k,omitempty"`
    Threshold  float64  `json:"threshold,omitempty" yaml:"threshold,omitempty"`
    UseWeb     bool     `json:"use_web,omitempty" yaml:"use_web,omitempty"`
}

type PostConfig struct {
    Rerank struct {
        Enable   bool   `json:"enable,omitempty" yaml:"enable,omitempty"`
        Provider string `json:"provider,omitempty" yaml:"provider,omitempty"`
        Endpoint string `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`
        TopN     int    `json:"top_n,omitempty" yaml:"top_n,omitempty"`
    } `json:"rerank" yaml:"rerank"`
    Compress struct {
        Enable      bool    `json:"enable,omitempty" yaml:"enable,omitempty"`
        Method      string  `json:"method,omitempty" yaml:"method,omitempty"`
        TargetRatio float64 `json:"target_ratio,omitempty" yaml:"target_ratio,omitempty"`
    } `json:"compress" yaml:"compress"`
}

type CRAGConfig struct {
    Evaluator struct {
        Provider string  `json:"provider,omitempty" yaml:"provider,omitempty"`
        Endpoint string  `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`
        Correct  float64 `json:"correct,omitempty" yaml:"correct,omitempty"`
        Incorrect float64 `json:"incorrect,omitempty" yaml:"incorrect,omitempty"`
    } `json:"evaluator" yaml:"evaluator"`
    // Strict mode: if true, external evaluator is required and no heuristic fallback is allowed.
    Strict  bool `json:"strict,omitempty" yaml:"strict,omitempty"`
    // FailMode controls behavior when evaluator fails: "open" (default) keeps fused results, "closed" returns error.
    FailMode string `json:"fail_mode,omitempty" yaml:"fail_mode,omitempty"`
    MaxIters int `json:"max_iters,omitempty" yaml:"max_iters,omitempty"`
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

// DefaultPipeline returns a safe default pipeline configuration.
func DefaultPipeline() *PipelineConfig {
    return &PipelineConfig{
        EnablePre:    false,
        EnableHybrid: false,
        EnablePost:   false,
        EnableCRAG:   false,
        RRFK:         60,
        Post: &PostConfig{},
        CRAG: &CRAGConfig{},
    }
}
