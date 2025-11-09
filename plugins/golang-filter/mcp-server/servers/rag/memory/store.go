package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/alibaba/higress/plugins/golang-filter/mcp-session/common"
)

// ConversationStore 对话历史存储接口
// 提供对话历史和文档ID的存储和检索功能
// 注意：此接口与 rag 包中的 SessionStore（聊天会话管理）不同
type ConversationStore interface {
	// GetLastNRounds 获取最近 N 轮对话
	GetLastNRounds(ctx context.Context, sessionID string, n int) ([]ConversationRound, error)

	// GetDocIDs 获取会话相关的文档ID列表
	GetDocIDs(ctx context.Context, sessionID string) ([]string, error)

	// SaveRound 保存一轮对话
	SaveRound(ctx context.Context, sessionID string, round ConversationRound) error

	// SaveDocIDs 保存会话相关的文档ID列表
	SaveDocIDs(ctx context.Context, sessionID string, docIDs []string) error

	// Clear 清除指定会话的所有数据
	Clear(ctx context.Context, sessionID string) error
}

// =============================================================================
// InMemoryConversationStore - 内存实现
// =============================================================================

// InMemoryConversationStore 内存对话存储实现
// 适用于开发测试或单机部署
type InMemoryConversationStore struct {
	mu        sync.RWMutex
	sessions  map[string][]ConversationRound
	docIDs    map[string][]string
	maxRounds int
}

// NewInMemoryConversationStore 创建内存对话存储
func NewInMemoryConversationStore(maxRounds int) ConversationStore {
	if maxRounds <= 0 {
		maxRounds = 10
	}
	return &InMemoryConversationStore{
		sessions:  make(map[string][]ConversationRound),
		docIDs:    make(map[string][]string),
		maxRounds: maxRounds,
	}
}

// NewInMemorySessionStore 是 NewInMemoryConversationStore 的别名，为了向后兼容
func NewInMemorySessionStore(maxRounds int) ConversationStore {
	return NewInMemoryConversationStore(maxRounds)
}

func (s *InMemoryConversationStore) GetLastNRounds(ctx context.Context, sessionID string, n int) ([]ConversationRound, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rounds := s.sessions[sessionID]
	if len(rounds) == 0 {
		return []ConversationRound{}, nil
	}

	if n <= 0 || n >= len(rounds) {
		// 返回所有轮次的副本
		result := make([]ConversationRound, len(rounds))
		copy(result, rounds)
		return result, nil
	}

	// 返回最后 n 轮的副本
	result := make([]ConversationRound, n)
	copy(result, rounds[len(rounds)-n:])
	return result, nil
}

func (s *InMemoryConversationStore) GetDocIDs(ctx context.Context, sessionID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	docIDs := s.docIDs[sessionID]
	if len(docIDs) == 0 {
		return []string{}, nil
	}

	// 返回副本
	result := make([]string, len(docIDs))
	copy(result, docIDs)
	return result, nil
}

func (s *InMemoryConversationStore) SaveRound(ctx context.Context, sessionID string, round ConversationRound) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rounds := s.sessions[sessionID]
	rounds = append(rounds, round)

	// 保持最大轮数限制
	if len(rounds) > s.maxRounds {
		rounds = rounds[len(rounds)-s.maxRounds:]
	}

	s.sessions[sessionID] = rounds
	return nil
}

func (s *InMemoryConversationStore) SaveDocIDs(ctx context.Context, sessionID string, docIDs []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 保存副本
	newDocIDs := make([]string, len(docIDs))
	copy(newDocIDs, docIDs)
	s.docIDs[sessionID] = newDocIDs
	return nil
}

func (s *InMemoryConversationStore) Clear(ctx context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.sessions, sessionID)
	delete(s.docIDs, sessionID)
	return nil
}

// =============================================================================
// RedisConversationStore - Redis 实现
// =============================================================================

// RedisConversationStore Redis 对话存储实现
// 适用于生产环境，支持分布式部署
type RedisConversationStore struct {
	redisClient      *common.RedisClient
	keyPrefix        string
	sessionExpiry    time.Duration
	maxHistoryRounds int
}

// RedisConversationStoreConfig Redis 对话存储配置
type RedisConversationStoreConfig struct {
	RedisClient      *common.RedisClient
	KeyPrefix        string
	SessionExpiry    time.Duration
	MaxHistoryRounds int
}

// NewRedisConversationStore 创建 Redis 对话存储
func NewRedisConversationStore(cfg *RedisConversationStoreConfig) ConversationStore {
	if cfg.KeyPrefix == "" {
		cfg.KeyPrefix = "rag:conversation:"
	}
	if cfg.SessionExpiry == 0 {
		cfg.SessionExpiry = 24 * time.Hour
	}
	if cfg.MaxHistoryRounds == 0 {
		cfg.MaxHistoryRounds = 10
	}

	return &RedisConversationStore{
		redisClient:      cfg.RedisClient,
		keyPrefix:        cfg.KeyPrefix,
		sessionExpiry:    cfg.SessionExpiry,
		maxHistoryRounds: cfg.MaxHistoryRounds,
	}
}

// NewRedisSessionStore 是 NewRedisConversationStore 的别名，为了向后兼容
func NewRedisSessionStore(cfg *RedisConversationStoreConfig) ConversationStore {
	return NewRedisConversationStore(cfg)
}

func (s *RedisConversationStore) GetLastNRounds(ctx context.Context, sessionID string, n int) ([]ConversationRound, error) {
	key := s.keyPrefix + sessionID + ":rounds"
	value, err := s.redisClient.Get(key)
	if err != nil || value == "" {
		return []ConversationRound{}, nil
	}

	var rounds []ConversationRound
	if err := json.Unmarshal([]byte(value), &rounds); err != nil {
		return nil, fmt.Errorf("failed to unmarshal session rounds: %w", err)
	}

	if n <= 0 || n >= len(rounds) {
		return rounds, nil
	}

	return rounds[len(rounds)-n:], nil
}

func (s *RedisConversationStore) GetDocIDs(ctx context.Context, sessionID string) ([]string, error) {
	key := s.keyPrefix + sessionID + ":docs"
	value, err := s.redisClient.Get(key)
	if err != nil || value == "" {
		return []string{}, nil
	}

	var docIDs []string
	if err := json.Unmarshal([]byte(value), &docIDs); err != nil {
		return nil, fmt.Errorf("failed to unmarshal doc IDs: %w", err)
	}

	return docIDs, nil
}

func (s *RedisConversationStore) SaveRound(ctx context.Context, sessionID string, round ConversationRound) error {
	key := s.keyPrefix + sessionID + ":rounds"

	// 获取现有轮次
	rounds, err := s.GetLastNRounds(ctx, sessionID, s.maxHistoryRounds)
	if err != nil {
		return err
	}

	// 添加新轮次
	rounds = append(rounds, round)

	// 保持最大轮数限制
	if len(rounds) > s.maxHistoryRounds {
		rounds = rounds[len(rounds)-s.maxHistoryRounds:]
	}

	// 序列化并保存
	data, err := json.Marshal(rounds)
	if err != nil {
		return fmt.Errorf("failed to marshal rounds: %w", err)
	}

	return s.redisClient.Set(key, string(data), s.sessionExpiry)
}

func (s *RedisConversationStore) SaveDocIDs(ctx context.Context, sessionID string, docIDs []string) error {
	key := s.keyPrefix + sessionID + ":docs"

	data, err := json.Marshal(docIDs)
	if err != nil {
		return fmt.Errorf("failed to marshal doc IDs: %w", err)
	}

	return s.redisClient.Set(key, string(data), s.sessionExpiry)
}

func (s *RedisConversationStore) Clear(ctx context.Context, sessionID string) error {
	roundsKey := s.keyPrefix + sessionID + ":rounds"
	docsKey := s.keyPrefix + sessionID + ":docs"

	// 使用 Lua 脚本删除键
	script := `
		redis.call('DEL', KEYS[1])
		redis.call('DEL', KEYS[2])
		return 1
	`
	_, err := s.redisClient.Eval(script, 2, []string{roundsKey, docsKey}, nil)
	if err != nil {
		// 忽略错误，因为键可能不存在
		return nil
	}

	return nil
}
