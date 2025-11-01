package pre_retrieve

import "time"

// QueryContext 查询上下文，包含原始查询和会话信息
type QueryContext struct {
	// 原始用户查询
	Query string `json:"query"`
	// 最近 N 轮对话历史
	LastNRounds []ConversationRound `json:"last_n_rounds,omitempty"`
	// 相关文档 ID
	DocIDs []string `json:"doc_ids,omitempty"`
	// 会话 ID
	SessionID string `json:"session_id,omitempty"`
	// 时间戳
	Timestamp time.Time `json:"timestamp"`
}

// ConversationRound 对话轮次
type ConversationRound struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// Anchor 锚点信息
type Anchor struct {
	// 锚点 ID
	ID string `json:"id"`
	// 必须保留的槽位信息
	MustKeep []string `json:"must_keep"`
	// 锚点分数
	Score float64 `json:"score"`
	// 锚点类型 (entity, document, concept 等)
	Type string `json:"type"`
	// 锚点原始内容
	Content string `json:"content"`
}

// AlignedQuery 对齐后的查询
type AlignedQuery struct {
	// 对齐后的自包含查询
	Query string `json:"query"`
	// 锚点列表 (0-2 个)
	Anchors []Anchor `json:"anchors,omitempty"`
	// 对齐操作记录
	AlignmentOps []string `json:"alignment_ops,omitempty"`
}

// CardinalityType 文档数量先验类型
type CardinalityType string

const (
	// 单文档查询
	CardinalitySingle CardinalityType = "single"
	// 多文档查询
	CardinalityMulti CardinalityType = "multi"
	// 未知
	CardinalityUnknown CardinalityType = "unknown"
)

// QueryNode 查询节点（在 DAG 中）
type QueryNode struct {
	// 节点 ID
	ID string `json:"id"`
	// 子查询内容
	Query string `json:"query"`
	// 稀疏检索重写
	SparseRewrite string `json:"sparse_rewrite"`
	// 稠密检索重写
	DenseRewrite string `json:"dense_rewrite"`
	// 规范化操作
	Normalizations []string `json:"normalizations,omitempty"`
	// 依赖的节点 ID
	Dependencies []string `json:"dependencies,omitempty"`
}

// PlanEdge 计划边（节点依赖关系）
type PlanEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"` // "parallel", "sequential", "conditional"
}

// PreQRAGPlan PreQRAG 查询计划
type PreQRAGPlan struct {
	// 查询节点列表
	Nodes []QueryNode `json:"nodes"`
	// 节点间的边
	Edges []PlanEdge `json:"edges,omitempty"`
	// 结果合并策略
	JoinStrategy string `json:"join_strategy"` // "union", "intersection", "weighted"
	// 文档数量先验
	CardinalityPrior CardinalityType `json:"cardinality_prior"`
}

// ExpansionTerm 扩展词项
type ExpansionTerm struct {
	// 词项
	Term string `json:"term"`
	// 权重
	Weight float64 `json:"weight"`
	// Facet 分类
	Facet string `json:"facet,omitempty"`
	// 来源 (anchor, taxonomy, synonym, attribute)
	Source string `json:"source"`
}

// QueryExpansion 查询扩展
type QueryExpansion struct {
	// 节点 ID
	NodeID string `json:"node_id"`
	// 扩展词项列表
	Terms []ExpansionTerm `json:"terms"`
}

// HyDEVector HyDE 生成的向量
type HyDEVector struct {
	// 节点 ID
	NodeID string `json:"node_id"`
	// 生成的假设文档
	HypotheticalDoc string `json:"hypothetical_doc"`
	// 向量表示
	Vector []float32 `json:"vector"`
	// 质量分数（困惑度、NLI 等）
	QualityScore float64 `json:"quality_score"`
}

// PreRetrieveResult Pre-Retrieve 完整结果
type PreRetrieveResult struct {
	// 原始上下文
	Context QueryContext `json:"context"`
	// 对齐后的查询
	AlignedQuery AlignedQuery `json:"aligned_query"`
	// 查询计划
	Plan PreQRAGPlan `json:"plan"`
	// 查询扩展（按节点）
	Expansions map[string]QueryExpansion `json:"expansions,omitempty"`
	// HyDE 向量（按节点）
	HyDEVectors map[string]HyDEVector `json:"hyde_vectors,omitempty"`
	// 处理耗时（毫秒）
	ProcessingTimeMS int64 `json:"processing_time_ms"`
}
