package metrics

import (
	"encoding/json"
	"time"

	"github.com/envoyproxy/envoy/contrib/golang/common/go/api"
)

// RetrievalMetrics 记录单次检索的完整指标
type RetrievalMetrics struct {
	// 查询信息
	QueryID   string    `json:"query_id"`
	Query     string    `json:"query"`
	Timestamp time.Time `json:"timestamp"`

	// Profile 信息
	ProfileName       string   `json:"profile_name"`
	ProfileSource     string   `json:"profile_source,omitempty"` // "intent_match" | "default" | "config"
	Intent            string   `json:"intent,omitempty"`
	IntentConfidence  float64  `json:"intent_confidence,omitempty"`
	RetrieversUsed    []string `json:"retrievers_used"`
	RetrieversSkipped []string `json:"retrievers_skipped,omitempty"` // 被 Gating 跳过的检索器

	// Pre 阶段
	PreEnabled      bool  `json:"pre_enabled"`
	PreLatencyMs    int64 `json:"pre_latency_ms,omitempty"`
	SubQueriesCount int   `json:"sub_queries_count,omitempty"`

	// 检索阶段（增强）
	RetrieverMetrics  map[string]RetrieverStats `json:"retriever_metrics"`
	TotalRetrieved    int                       `json:"total_retrieved"`
	RetrievalPhases   []string                  `json:"retrieval_phases,omitempty"` // ["vector_preflight", "parallel_retrieve", "fallback"]
	FallbackTriggered bool                      `json:"fallback_triggered"`

	// 融合阶段
	FusionMethod       string `json:"fusion_method"`
	FusionResultCount  int    `json:"fusion_result_count"`
	FusionLatencyMs    int64  `json:"fusion_latency_ms,omitempty"`
	DeduplicationCount int    `json:"deduplication_count,omitempty"` // 融合前去重的文档数

	// Post 阶段
	RerankEnabled     bool  `json:"rerank_enabled"`
	RerankLatencyMs   int64 `json:"rerank_latency_ms,omitempty"`
	RerankResultCount int   `json:"rerank_result_count,omitempty"`
	CompressEnabled   bool  `json:"compress_enabled"`

	// CRAG 阶段
	CRAGEnabled bool    `json:"crag_enabled"`
	CRAGVerdict string  `json:"crag_verdict,omitempty"`
	CRAGScore   float64 `json:"crag_score,omitempty"`

	// Gating 决策（增强）
	GatingEnabled   bool     `json:"gating_enabled"`
	GatingDecisions []string `json:"gating_decisions,omitempty"`
	GatingLatencyMs int64    `json:"gating_latency_ms,omitempty"`

	// 总体
	TotalLatencyMs int64  `json:"total_latency_ms"`
	Success        bool   `json:"success"`
	ErrorMsg       string `json:"error_msg,omitempty"`
}

// RetrieverStats 单个检索器的统计信息
type RetrieverStats struct {
	Type        string  `json:"type"`
	LatencyMs   int64   `json:"latency_ms"`
	ResultCount int     `json:"result_count"`
	AvgScore    float64 `json:"avg_score"`
	TopScore    float64 `json:"top_score"`
}

// NewRetrievalMetrics 创建新的检索指标实例
func NewRetrievalMetrics() *RetrievalMetrics {
	return &RetrievalMetrics{
		RetrieverMetrics: make(map[string]RetrieverStats),
		RetrievalPhases:  make([]string, 0),
		GatingDecisions:  make([]string, 0),
	}
}

// Log 将指标以 JSON 格式输出到日志
func (m *RetrievalMetrics) Log() {
	if data, err := json.Marshal(m); err == nil {
		api.LogInfof("[RAG_METRICS] %s", string(data))
	}
}

// LogJSON 是 Log 的别名（为了更清晰的语义）
func (m *RetrievalMetrics) LogJSON() {
	m.Log()
}

// AddRetrieverStats 添加或更新检索器统计
func (m *RetrievalMetrics) AddRetrieverStats(stats RetrieverStats) {
	if m.RetrieverMetrics == nil {
		m.RetrieverMetrics = make(map[string]RetrieverStats)
	}

	key := stats.Type
	if existing, ok := m.RetrieverMetrics[key]; ok {
		// 合并统计（简单平均）
		existing.LatencyMs = (existing.LatencyMs + stats.LatencyMs) / 2
		existing.ResultCount += stats.ResultCount
		if stats.TopScore > existing.TopScore {
			existing.TopScore = stats.TopScore
		}
		existing.AvgScore = (existing.AvgScore + stats.AvgScore) / 2
		m.RetrieverMetrics[key] = existing
	} else {
		m.RetrieverMetrics[key] = stats
	}
}

// AddGatingDecision 记录 gating 决策
func (m *RetrievalMetrics) AddGatingDecision(decision string) {
	m.GatingDecisions = append(m.GatingDecisions, decision)
}

// RecordProfileSelection 记录 Profile 选择信息
func (m *RetrievalMetrics) RecordProfileSelection(name, source string) {
	m.ProfileName = name
	m.ProfileSource = source
}

// RecordIntentMatch 记录 Intent 匹配信息
func (m *RetrievalMetrics) RecordIntentMatch(intent string, confidence float64) {
	m.Intent = intent
	m.IntentConfidence = confidence
}

// AddRetrievalPhase 记录检索阶段
func (m *RetrievalMetrics) AddRetrievalPhase(phase string) {
	m.RetrievalPhases = append(m.RetrievalPhases, phase)
}

// AddSkippedRetriever 记录被跳过的检索器
func (m *RetrievalMetrics) AddSkippedRetriever(retriever string) {
	m.RetrieversSkipped = append(m.RetrieversSkipped, retriever)
}

// RecordFusion 记录融合信息
func (m *RetrievalMetrics) RecordFusion(method string, resultCount, deduplicationCount int, latencyMs int64) {
	m.FusionMethod = method
	m.FusionResultCount = resultCount
	m.DeduplicationCount = deduplicationCount
	m.FusionLatencyMs = latencyMs
}
