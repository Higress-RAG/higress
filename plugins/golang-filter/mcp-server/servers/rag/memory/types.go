package memory

import "time"

// ConversationRound 对话轮次
type ConversationRound struct {
	Question  string    `json:"question"`
	Answer    string    `json:"answer"`
	Timestamp time.Time `json:"timestamp,omitempty"`
}

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
