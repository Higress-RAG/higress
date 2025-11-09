# Memory - 对话历史管理模块

Memory 模块是一个通用的对话历史和会话上下文管理组件，提供了会话存储的抽象接口和多种实现。

## 📦 功能特性

- **对话历史管理**: 存储和检索多轮对话历史
- **文档ID管理**: 关联会话与相关文档
- **多种存储后端**: 内存存储（开发/测试）和 Redis 存储（生产环境）
- **线程安全**: 所有操作都是并发安全的
- **自动过期**: Redis 存储支持 TTL 自动过期
- **轮次限制**: 自动保持最近 N 轮对话

## 🏗️ 核心接口

### ConversationStore

对话存储的核心接口：

```go
type ConversationStore interface {
    // 获取最近 N 轮对话
    GetLastNRounds(ctx context.Context, sessionID string, n int) ([]ConversationRound, error)
    
    // 获取会话相关的文档ID列表
    GetDocIDs(ctx context.Context, sessionID string) ([]string, error)
    
    // 保存一轮对话
    SaveRound(ctx context.Context, sessionID string, round ConversationRound) error
    
    // 保存会话相关的文档ID列表
    SaveDocIDs(ctx context.Context, sessionID string, docIDs []string) error
    
    // 清除指定会话的所有数据
    Clear(ctx context.Context, sessionID string) error
}
```

### 数据结构

```go
// 对话轮次
type ConversationRound struct {
    Question  string    `json:"question"`
    Answer    string    `json:"answer"`
    Timestamp time.Time `json:"timestamp,omitempty"`
}

// 查询上下文
type QueryContext struct {
    Query       string              `json:"query"`
    LastNRounds []ConversationRound `json:"last_n_rounds,omitempty"`
    DocIDs      []string            `json:"doc_ids,omitempty"`
    SessionID   string              `json:"session_id,omitempty"`
    Timestamp   time.Time           `json:"timestamp"`
}
```

## 🚀 使用示例

### 1. 内存存储（开发/测试）

```go
import "github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/memory"

// 创建内存存储，最多保留 10 轮对话
store := memory.NewInMemoryConversationStore(10)

// 保存对话
round := memory.ConversationRound{
    Question:  "什么是 RAG？",
    Answer:    "RAG 是检索增强生成技术...",
    Timestamp: time.Now(),
}
err := store.SaveRound(context.Background(), "session-123", round)

// 获取历史对话
rounds, err := store.GetLastNRounds(context.Background(), "session-123", 5)
for _, r := range rounds {
    fmt.Printf("Q: %s\nA: %s\n\n", r.Question, r.Answer)
}
```

### 2. Redis 存储（生产环境）

```go
import "github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/memory"

// 配置 Redis 存储
cfg := &memory.RedisConversationStoreConfig{
    RedisClient:      redisClient,
    KeyPrefix:        "rag:conversation:",
    SessionExpiry:    24 * time.Hour,
    MaxHistoryRounds: 20,
}

store := memory.NewRedisConversationStore(cfg)

// 使用方式与内存存储相同
err := store.SaveRound(ctx, sessionID, round)
rounds, err := store.GetLastNRounds(ctx, sessionID, 10)
```

### 3. 管理文档关联

```go
// 保存会话相关的文档ID
docIDs := []string{"doc-001", "doc-002", "doc-003"}
err := store.SaveDocIDs(ctx, "session-123", docIDs)

// 获取相关文档
docIDs, err := store.GetDocIDs(ctx, "session-123")
fmt.Printf("相关文档: %v\n", docIDs)
```

### 4. 清理会话数据

```go
// 清除指定会话的所有数据
err := store.Clear(ctx, "session-123")
```

## 🔄 与其他模块的关系

### Pre-Retrieve 模块

Pre-Retrieve 模块使用 Memory 进行对话历史采集：

```go
// Pre-Retrieve 使用 memory.ConversationStore
sessionStore := memory.NewInMemoryConversationStore(10)
processor := pre_retrieve.NewMemoryIntakeProcessor(
    cfg,
    sessionStore,
    nil, // externalStore
)
```

### 类型别名

为了保持向后兼容，Memory 模块提供了以下别名：

```go
// SessionStore 是 ConversationStore 的别名
type SessionStore = ConversationStore

// 推荐使用 ConversationStore，以避免与 rag 包中的 SessionStore 混淆
```

## ⚠️ 注意事项

### 与 RAG SessionStore 的区别

Memory 模块的 `ConversationStore` 与 RAG 包中的 `SessionStore` 是**两个不同的概念**：

| 特性 | memory.ConversationStore | rag.SessionStore |
|------|-------------------------|------------------|
| 用途 | 对话历史管理 | 聊天会话管理 |
| 数据结构 | ConversationRound | ChatMessage |
| 主要功能 | 存储问答轮次 | 管理完整会话 |
| 使用场景 | Pre-Retrieve, 上下文采集 | 聊天接口 |

### 线程安全

- `InMemoryConversationStore`: 使用 RWMutex 保证并发安全
- `RedisConversationStore`: 利用 Redis 的原子操作保证安全

### 性能考虑

1. **内存存储**
   - 优点：速度快，无网络延迟
   - 缺点：不持久化，重启丢失
   - 适用：开发、测试、单机部署

2. **Redis 存储**
   - 优点：持久化，支持分布式
   - 缺点：网络延迟，依赖 Redis
   - 适用：生产环境，多实例部署

## 📊 存储格式

### Redis Key 格式

```
{keyPrefix}{sessionID}:rounds  - 存储对话轮次数组（JSON）
{keyPrefix}{sessionID}:docs    - 存储文档ID数组（JSON）
```

示例：
```
rag:conversation:session-123:rounds
rag:conversation:session-123:docs
```

### JSON 格式示例

```json
// rounds
[
  {
    "question": "什么是RAG?",
    "answer": "RAG是检索增强生成技术...",
    "timestamp": "2025-01-01T12:00:00Z"
  }
]

// docs
["doc-001", "doc-002", "doc-003"]
```

## 🔧 扩展性

要实现自定义存储后端，只需实现 `ConversationStore` 接口：

```go
type MyCustomStore struct {
    // your fields
}

func (s *MyCustomStore) GetLastNRounds(ctx context.Context, sessionID string, n int) ([]ConversationRound, error) {
    // your implementation
}

// ... 实现其他方法
```

## 📚 相关模块

- `pre-retrieve`: 使用 Memory 进行上下文采集
- `rag`: RAG 主流程
- `cache`: 缓存模块
- `session`: 会话管理模块（不同用途）

## 🎯 最佳实践

1. **开发环境**: 使用 `InMemoryConversationStore`
2. **生产环境**: 使用 `RedisConversationStore`
3. **设置合理的轮次限制**: 避免内存/存储占用过大
4. **定期清理**: 对不活跃的会话调用 `Clear()`
5. **错误处理**: 妥善处理存储失败的情况
6. **监控**: 监控 Redis 连接状态和存储大小

## 🐛 故障排查

### 内存存储问题

```go
// 检查并发访问
store := memory.NewInMemoryConversationStore(10)
// 内部使用 RWMutex，并发安全
```

### Redis 存储问题

```go
// 检查 Redis 连接
err := redisClient.checkConnection()

// 检查 TTL 设置
// 确保 SessionExpiry > 0

// 查看 Redis 键
// redis-cli KEYS "rag:conversation:*"
```

## 📝 TODO

- [ ] 支持更多存储后端（MongoDB, PostgreSQL）
- [ ] 添加批量操作接口
- [ ] 支持会话搜索和过滤
- [ ] 添加指标收集（存储大小、访问频率）
- [ ] 支持会话导出和备份

