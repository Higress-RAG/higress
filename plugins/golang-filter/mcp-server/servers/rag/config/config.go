package config

import "fmt"

// Config represents the main configuration structure for the MCP server
type Config struct {
	RAG       RAGConfig       `json:"rag" yaml:"rag"`
	LLM       LLMConfig       `json:"llm" yaml:"llm"`
	Embedding EmbeddingConfig `json:"embedding" yaml:"embedding"`
	VectorDB  VectorDBConfig  `json:"vectordb" yaml:"vectordb"`
	// Pipeline holds optional enhanced RAG pipeline settings. If nil, fallback to baseline RAG.
	Pipeline *PipelineConfig `json:"pipeline,omitempty" yaml:"pipeline,omitempty"`
}

// RAGConfig contains basic configuration for the RAG system
type RAGConfig struct {
	Splitter  SplitterConfig `json:"splitter" yaml:"splitter"`
	Threshold float64        `json:"threshold,omitempty" yaml:"threshold,omitempty"`
	TopK      int            `json:"top_k,omitempty" yaml:"top_k,omitempty"`
}

// SplitterConfig defines document splitter configuration
type SplitterConfig struct {
	Provider     string `json:"provider" yaml:"provider"` // Available options: recursive, character, token
	ChunkSize    int    `json:"chunk_size,omitempty" yaml:"chunk_size,omitempty"`
	ChunkOverlap int    `json:"chunk_overlap,omitempty" yaml:"chunk_overlap,omitempty"`
}

// LLMConfig defines configuration for Large Language Models
type LLMConfig struct {
	Provider    string  `json:"provider" yaml:"provider"` // Available options: openai, dashscope, qwen
	APIKey      string  `json:"api_key,omitempty" yaml:"api_key"`
	BaseURL     string  `json:"base_url,omitempty" yaml:"base_url,omitempty"`
	Model       string  `json:"model" yaml:"model"`
	Temperature float64 `json:"temperature,omitempty" yaml:"temperature,omitempty"`
	MaxTokens   int     `json:"max_tokens,omitempty" yaml:"max_tokens,omitempty"`
}

// EmbeddingConfig defines configuration for embedding models
type EmbeddingConfig struct {
	Provider   string `json:"provider" yaml:"provider"` // Available options: openai, dashscope
	APIKey     string `json:"api_key,omitempty" yaml:"api_key,omitempty"`
	BaseURL    string `json:"base_url,omitempty" yaml:"base_url,omitempty"`
	Model      string `json:"model,omitempty" yaml:"model,omitempty"`
	Dimensions int    `json:"dimensions,omitempty" yaml:"dimension,omitempty"`
}

// VectorDBConfig defines configuration for vector databases
type VectorDBConfig struct {
	Provider   string        `json:"provider" yaml:"provider"` // Available options: milvus, qdrant, chroma
	Host       string        `json:"host,omitempty" yaml:"host,omitempty"`
	Port       int           `json:"port,omitempty" yaml:"port,omitempty"`
	Database   string        `json:"database,omitempty" yaml:"database,omitempty"`
	Collection string        `json:"collection,omitempty" yaml:"collection,omitempty"`
	Username   string        `json:"username,omitempty" yaml:"username,omitempty"`
	Password   string        `json:"password,omitempty" yaml:"password,omitempty"`
	Mapping    MappingConfig `json:"mapping,omitempty" yaml:"mapping,omitempty"`
}

// MappingConfig defines field mapping configuration for vector databases
type MappingConfig struct {
	Fields []FieldMapping `json:"fields,omitempty" yaml:"fields,omitempty"`
	Index  IndexConfig    `json:"index,omitempty" yaml:"index,omitempty"`
	Search SearchConfig   `json:"search,omitempty" yaml:"search,omitempty"`
}

// // CollectionMapping defines field mapping for collection
// type CollectionMapping struct {
// 	Fields []FieldMapping `json:"fields,omitempty" yaml:"fields,omitempty"`
// }

// FieldMapping defines mapping for a single field
type FieldMapping struct {
	StandardName string                 `json:"standard_name" yaml:"standard_name"`
	RawName      string                 `json:"raw_name" yaml:"raw_name"`
	Properties   map[string]interface{} `json:"properties,omitempty" yaml:"properties,omitempty"`
}


type PreRetrieveConfig struct {
	Provider  string                 `json:"provider" yaml:"provider"`
	TimeOutMS int                    `json:"time_out_ms" yaml:"time_out_ms"`
	LLM       LLMConfig              `json:"llm" yaml:"llm"` // LLM 配置用于查询改写
	Memory    MemoryConfig           `json:"memory" yaml:"memory"`
	Alignment ContextAlignmentConfig `json:"alignment" yaml:"alignment"`
	Planning  PreQRAGPlanningConfig  `json:"planning" yaml:"planning"`
	Expansion ExpansionConfig        `json:"expansion" yaml:"expansion"`
	HyDE      HyDEConfig             `json:"hyde" yaml:"hyde"`
}

// MemoryConfig 定义记忆采集配置
type MemoryConfig struct {
	Enabled        bool `json:"enabled" yaml:"enabled"`
	LastNRounds    int  `json:"last_n_rounds" yaml:"last_n_rounds"`     // 最近 N 轮对话
	EnableDocIDs   bool `json:"enable_doc_ids" yaml:"enable_doc_ids"`   // 是否启用文档 ID
	EnableSession  bool `json:"enable_session" yaml:"enable_session"`   // 是否启用会话记忆
	EnableExternal bool `json:"enable_external" yaml:"enable_external"` // 是否启用外部记忆
}

// ContextAlignmentConfig 定义上下文对齐配置
type ContextAlignmentConfig struct {
	Enabled              bool    `json:"enabled" yaml:"enabled"`
	EnablePronouns       bool    `json:"enable_pronouns" yaml:"enable_pronouns"`               // 代词消解
	EnableTimeNorm       bool    `json:"enable_time_norm" yaml:"enable_time_norm"`             // 时间归一化
	EnableAnchor         bool    `json:"enable_anchor" yaml:"enable_anchor"`                   // 锚点裁决
	AnchorScoreThreshold float64 `json:"anchor_score_threshold" yaml:"anchor_score_threshold"` // 锚点分数阈值
	MaxAnchors           int     `json:"max_anchors" yaml:"max_anchors"`                       // 最大锚点数
}

// PreQRAGPlanningConfig 定义 PreQRAG 规划器配置
type PreQRAGPlanningConfig struct {
	Enabled                bool `json:"enabled" yaml:"enabled"`
	EnableNormalization    bool `json:"enable_normalization" yaml:"enable_normalization"`         // 规范化
	EnableDecomposition    bool `json:"enable_decomposition" yaml:"enable_decomposition"`         // 子问题分解
	EnableChannelRewrite   bool `json:"enable_channel_rewrite" yaml:"enable_channel_rewrite"`     // 通道感知重写
	MaxSubQueries          int  `json:"max_sub_queries" yaml:"max_sub_queries"`                   // 最大子查询数
	EnableCardinalityPrior bool `json:"enable_cardinality_prior" yaml:"enable_cardinality_prior"` // 单/多文档先验判定
}

// ExpansionConfig 定义扩写配置
type ExpansionConfig struct {
	Enabled          bool `json:"enabled" yaml:"enabled"`
	MaxTerms         int  `json:"max_terms" yaml:"max_terms"`                 // 最大扩展词数
	EnableTaxonomy   bool `json:"enable_taxonomy" yaml:"enable_taxonomy"`     // 域内分类
	EnableSynonyms   bool `json:"enable_synonyms" yaml:"enable_synonyms"`     // 同义词
	EnableAttributes bool `json:"enable_attributes" yaml:"enable_attributes"` // 属性对
}

// HyDEConfig 定义 HyDE (Hypothetical Document Embeddings) 配置
type HyDEConfig struct {
	Enabled               bool `json:"enabled" yaml:"enabled"`
	MinQueryLength        int  `json:"min_query_length" yaml:"min_query_length"`               // 最小查询长度
	GeneratedDocLength    int  `json:"generated_doc_length" yaml:"generated_doc_length"`       // 生成文档长度
	EnablePerplexityCheck bool `json:"enable_perplexity_check" yaml:"enable_perplexity_check"` // 困惑度检查
	EnableNLIGuardrail    bool `json:"enable_nli_guardrail" yaml:"enable_nli_guardrail"`       // NLI 护栏
}

func (f FieldMapping) IsPrimaryKey() bool {
	return f.StandardName == "id"
}

func (f FieldMapping) IsAutoID() bool {
	if f.Properties == nil {
		return false
	}
	autoID, ok := f.Properties["auto_id"].(bool)
	if !ok {
		return false
	}
	return autoID
}

func (f FieldMapping) IsVectorField() bool {
	return f.StandardName == "vector"
}

func (f FieldMapping) MaxLength() int {
	if f.Properties == nil {
		return 0
	}
	maxLength, ok := f.Properties["max_length"].(int)
	if !ok {
		return 256
	}
	return maxLength
}

// IndexConfig defines configuration for index parameters
type IndexConfig struct {
	// Index type, e.g., IVF_FLAT, IVF_SQ8, HNSW, etc.
	IndexType string `json:"index_type" yaml:"index_type"`
	// Index parameter configuration
	Params map[string]interface{} `json:"params" yaml:"params"`
}

func (i IndexConfig) ParamsString(key string) (string, error) {
	if mVal, ok := i.Params[key].(string); ok {
		return mVal, nil
	}
	return "", fmt.Errorf("params %s not found", key)
}

func (i IndexConfig) ParamsInt64(key string) (int64, error) {
	if mVal, ok := i.Params[key].(int64); ok {
		return mVal, nil
	}
	if mVal, ok := i.Params[key].(int); ok {
		return int64(mVal), nil
	}
	return 0, fmt.Errorf("params %s not found", key)
}

func (i IndexConfig) ParamsFloat64(key string) (float64, error) {
	if mVal, ok := i.Params[key].(float64); ok {
		return mVal, nil
	}
	if mVal, ok := i.Params[key].(float32); ok {
		return float64(mVal), nil
	}
	return 0, fmt.Errorf("params %s not found", key)
}

func (i IndexConfig) ParamsBool(key string) (bool, error) {
	if mVal, ok := i.Params[key].(bool); ok {
		return mVal, nil
	}
	return false, fmt.Errorf("params %s not found", key)
}

// SearchConfig defines configuration for search parameters
type SearchConfig struct {
	// Metric type, e.g., L2, IP, etc.
	MetricType string `json:"metric_type,omitempty" yaml:"metric_type,omitempty"`
	// Search parameter configuration
	Params map[string]interface{} `json:"params" yaml:"params"`
}

func (i SearchConfig) ParamsString(key string) (string, error) {
	if mVal, ok := i.Params[key].(string); ok {
		return mVal, nil
	}
	return "", fmt.Errorf("params %s not found", key)
}

func (i SearchConfig) ParamsInt64(key string) (int64, error) {
	if mVal, ok := i.Params[key].(int64); ok {
		return mVal, nil
	}
	return 0, fmt.Errorf("params %s not found", key)
}

func (i SearchConfig) ParamsFloat64(key string) (float64, error) {
	if mVal, ok := i.Params[key].(float64); ok {
		return mVal, nil
	}
	return 0, fmt.Errorf("params %s not found", key)
}

func (i SearchConfig) ParamsBool(key string) (bool, error) {
	if mVal, ok := i.Params[key].(bool); ok {
		return mVal, nil
	}
	return false, fmt.Errorf("params %s not found", key)
}
