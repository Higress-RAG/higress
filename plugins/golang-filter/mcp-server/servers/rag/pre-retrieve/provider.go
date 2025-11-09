package pre_retrieve

import (
	"context"
	"fmt"
	"time"

	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/config"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/embedding"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/llm"
	"github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/memory"
)

const (
	// Provider 类型常量
	PROVIDER_TYPE_DEFAULT = "default"
	PROVIDER_TYPE_PREQRAG = "preqrag"
)

// Provider Pre-Retrieve Provider 接口
type Provider interface {
	// GetProviderType 返回 Provider 类型
	GetProviderType() string

	// Process 处理原始查询，返回 Pre-Retrieve 结果
	Process(ctx context.Context, rawQuery string, sessionID string) (*PreRetrieveResult, error)
}

// DefaultPreRetrieveProvider 默认 Pre-Retrieve Provider 实现
// 整合所有阶段：Memory Intake -> Context Alignment -> PreQRAG Planning -> Expansion -> HyDE
type DefaultPreRetrieveProvider struct {
	providerType string
	config       *config.PreRetrieveConfig

	// 各个阶段的处理器
	memoryProcessor    MemoryIntakeProcessor
	alignmentProcessor ContextAlignmentProcessor
	planner            PreQRAGPlanner
	expansionProcessor ExpansionProcessor
	hydeProcessor      HyDEProcessor
}

// GetProviderType 返回 Provider 类型
func (p *DefaultPreRetrieveProvider) GetProviderType() string {
	return p.providerType
}

// Process 处理原始查询，返回 Pre-Retrieve 结果
func (p *DefaultPreRetrieveProvider) Process(ctx context.Context, rawQuery string, sessionID string) (*PreRetrieveResult, error) {
	startTime := time.Now()
	result := &PreRetrieveResult{}

	// 阶段 1: Memory Intake - 采集上下文
	queryCtx, err := p.memoryProcessor.Process(ctx, rawQuery, sessionID)
	if err != nil {
		return nil, fmt.Errorf("memory intake failed: %w", err)
	}
	queryCtx.Timestamp = startTime
	result.Context = *queryCtx

	// 阶段 2: Context Alignment - 上下文对齐
	alignedQuery, err := p.alignmentProcessor.Process(ctx, queryCtx)
	if err != nil {
		return nil, fmt.Errorf("context alignment failed: %w", err)
	}
	result.AlignedQuery = *alignedQuery

	// 阶段 3: PreQRAG Planning - 统一规划
	plan, err := p.planner.Plan(ctx, alignedQuery)
	if err != nil {
		return nil, fmt.Errorf("preqrag planning failed: %w", err)
	}
	result.Plan = *plan

	// 阶段 4: Expansion - 扩写（可选）
	if p.expansionProcessor != nil {
		expansions, err := p.expansionProcessor.Expand(ctx, plan, alignedQuery)
		if err == nil {
			result.Expansions = expansions
		}
	}

	// 阶段 5: HyDE - 生成假设文档（可选）
	if p.hydeProcessor != nil {
		hydeVectors, err := p.hydeProcessor.Generate(ctx, plan, alignedQuery)
		if err == nil {
			result.HyDEVectors = hydeVectors
		}
	}

	result.ProcessingTimeMS = time.Since(startTime).Milliseconds()
	return result, nil
}

// providerInitializer Provider 初始化器接口
type providerInitializer interface {
	ValidateConfig(cfg *config.PreRetrieveConfig) error
	CreateProvider(cfg *config.PreRetrieveConfig) (Provider, error)
}

// PreRetrieveInitializer Provider 初始化器实现
type PreRetrieveInitializer struct{}

// ValidateConfig 验证配置
func (i *PreRetrieveInitializer) ValidateConfig(cfg *config.PreRetrieveConfig) error {
	if cfg.Provider == "" {
		return fmt.Errorf("provider type is required")
	}

	needLLM := cfg.Alignment.Enabled || cfg.Planning.Enabled || cfg.Expansion.Enabled || cfg.HyDE.Enabled
	if needLLM && cfg.LLM.Provider == "" {
		return fmt.Errorf("LLM provider is required when alignment/planning/expansion/hyde is enabled")
	}

	if cfg.HyDE.Enabled {
		if cfg.HyDE.MinQueryLength == 0 {
			cfg.HyDE.MinQueryLength = 10
		}
		if cfg.HyDE.GeneratedDocLength == 0 {
			cfg.HyDE.GeneratedDocLength = 100
		}
	}

	return nil
}

// CreateProvider 创建 Provider 实例
func (i *PreRetrieveInitializer) CreateProvider(cfg *config.PreRetrieveConfig) (Provider, error) {
	if err := i.ValidateConfig(cfg); err != nil {
		return nil, err
	}

	provider := &DefaultPreRetrieveProvider{
		providerType: cfg.Provider,
		config:       cfg,
	}

	// 创建 LLM Provider
	var llmProvider llm.Provider
	var err error
	if cfg.LLM.Provider != "" {
		llmProvider, err = llm.NewLLMProvider(cfg.LLM)
		if err != nil {
			return nil, fmt.Errorf("failed to create LLM provider: %w", err)
		}
	}

	// 创建 Embedding Provider（如果 HyDE 启用）
	var embeddingProvider embedding.Provider
	if cfg.HyDE.Enabled {
		// 注意：这里需要从外部传入或配置中获取 embedding config
		// 暂时留空，实际使用时需要补充
	}

	// 1. Memory Intake Processor
	sessionStore := memory.NewInMemorySessionStore(cfg.Memory.LastNRounds)
	provider.memoryProcessor = NewMemoryIntakeProcessor(&cfg.Memory, sessionStore, nil)

	// 2. Context Alignment Processor
	anchorRetriever := NewDefaultAnchorCandidateRetriever()
	provider.alignmentProcessor = NewContextAlignmentProcessor(&cfg.Alignment, llmProvider, anchorRetriever)

	// 3. PreQRAG Planner
	provider.planner = NewPreQRAGPlanner(&cfg.Planning, llmProvider)

	// 4. Expansion Processor（可选）
	if cfg.Expansion.Enabled {
		taxonomyProvider := NewDefaultTaxonomyProvider()
		provider.expansionProcessor = NewExpansionProcessor(&cfg.Expansion, llmProvider, taxonomyProvider)
	}

	// 5. HyDE Processor（可选）
	if cfg.HyDE.Enabled && embeddingProvider != nil {
		provider.hydeProcessor = NewHyDEProcessor(&cfg.HyDE, llmProvider, embeddingProvider)
	}

	return provider, nil
}

// providerInitializers Provider 初始化器映射
var providerInitializers = map[string]providerInitializer{
	PROVIDER_TYPE_DEFAULT: &PreRetrieveInitializer{},
	PROVIDER_TYPE_PREQRAG: &PreRetrieveInitializer{},
}

// NewPreRetrieveProvider 创建 Pre-Retrieve Provider
func NewPreRetrieveProvider(cfg *config.PreRetrieveConfig) (Provider, error) {
	initializer, ok := providerInitializers[cfg.Provider]
	if !ok {
		return nil, fmt.Errorf("unknown provider type: %s", cfg.Provider)
	}
	return initializer.CreateProvider(cfg)
}
