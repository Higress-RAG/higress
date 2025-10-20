package rag

import (
    "encoding/json"
    "fmt"
    "sort"
    "time"

    "github.com/alibaba/higress/plugins/golang-filter/mcp-server/servers/rag/config"
    "github.com/alibaba/higress/plugins/golang-filter/mcp-session/common"
    "github.com/google/uuid"
)

// RedisSessionStore persists sessions in Redis via common.RedisClient.
// Data model:
//  - key prefix+"session:"+id => JSON(Session) with TTL
//  - key prefix+"index" => JSON array of IDs (best-effort index)
type RedisSessionStore struct {
    rc     *common.RedisClient
    prefix string
    ttl    time.Duration
}

func NewRedisSessionStore(cfg *config.SessionConfig) (*RedisSessionStore, error) {
    ttl := time.Duration(cfg.TTLSeconds) * time.Second
    if ttl <= 0 { ttl = 24 * time.Hour }
    prefix := "rag:sess:"
    rcfg, err := common.ParseRedisConfig(cfg.Redis)
    if err != nil { return nil, err }
    rcli, err := common.NewRedisClient(rcfg)
    if err != nil { return nil, err }
    return &RedisSessionStore{rc: rcli, prefix: prefix, ttl: ttl}, nil
}

func (s *RedisSessionStore) idxKey() string { return s.prefix + "idx" }
func (s *RedisSessionStore) sessKey(id string) string { return s.prefix + "session:" + id }

func (s *RedisSessionStore) Create() *Session {
    id := uuid.New().String()
    sess := &Session{ID: id, CreatedAt: time.Now(), Messages: []ChatMessage{}}
    b, _ := json.Marshal(sess)
    // Lua script: SET session json with TTL; ZADD idx with score=createdAt
    script := `
local sess_key = KEYS[1]
local idx_key = KEYS[2]
local sess_json = ARGV[1]
local ttl = tonumber(ARGV[2])
local score = tonumber(ARGV[3])
local id = ARGV[4]
redis.call('SET', sess_key, sess_json, 'EX', ttl)
redis.call('ZADD', idx_key, score, id)
return 1`
    keys := []string{s.sessKey(id), s.idxKey()}
    args := []interface{}{string(b), int64(s.ttl / time.Second), time.Now().Unix(), id}
    if _, err := s.rc.Eval(script, len(keys), keys, args); err != nil {
        // fallback: best-effort set
        _ = s.rc.Set(s.sessKey(id), string(b), s.ttl)
    }
    return sess
}

func (s *RedisSessionStore) Get(id string) (*Session, bool) {
    script := `
local sess_key = KEYS[1]
if redis.call('EXISTS', sess_key) == 0 then return nil end
return redis.call('HGETALL', sess_key)`
    keys := []string{s.sessKey(id)}
    v, err := s.rc.Eval(script, len(keys), keys, nil)
    if err != nil || v == nil { return nil, false }
    m, ok := toHash(v)
    if !ok { return nil, false }
    sess := &Session{ID: m["id"], Messages: []ChatMessage{}}
    // parse created_at
    if ts := m["created_at"]; ts != "" {
        if sec, perr := parseInt64(ts); perr == nil { sess.CreatedAt = time.Unix(sec, 0) }
    }
    // parse messages json
    if js := m["messages"]; js != "" {
        _ = json.Unmarshal([]byte(js), &sess.Messages)
    }
    return sess, true
}

func (s *RedisSessionStore) Delete(id string) bool {
    script := `
local sess_key = KEYS[1]
local idx_key = KEYS[2]
local id = ARGV[1]
redis.call('DEL', sess_key)
redis.call('ZREM', idx_key, id)
return 1`
    keys := []string{s.sessKey(id), s.idxKey()}
    args := []interface{}{id}
    if _, err := s.rc.Eval(script, len(keys), keys, args); err != nil {
        return false
    }
    return true
}

func (s *RedisSessionStore) List() []*Session {
    return s.ListRange(0, 100)
}

func (s *RedisSessionStore) AddMessage(id string, msg ChatMessage) bool {
    st, ok := s.Get(id)
    if !ok || st == nil { return false }
    st.Messages = append(st.Messages, msg)
    msgs, _ := json.Marshal(st.Messages)
    script := `
local sess_key = KEYS[1]
local idx_key = KEYS[2]
local msgs = ARGV[1]
local ttl = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
redis.call('HSET', sess_key, 'messages', msgs)
redis.call('EXPIRE', sess_key, ttl)
redis.call('ZADD', idx_key, now, redis.call('HGET', sess_key, 'id'))
return 1`
    keys := []string{s.sessKey(id), s.idxKey()}
    args := []interface{}{string(msgs), int64(s.ttl / time.Second), time.Now().Unix()}
    if _, err := s.rc.Eval(script, len(keys), keys, args); err != nil {
        _ = fmt.Errorf("redis update failed: %v", err)
        return false
    }
    return true
}

// ListRange returns sessions from offset with limit (by recency desc)
func (s *RedisSessionStore) ListRange(offset, limit int) []*Session {
    if offset < 0 { offset = 0 }
    if limit <= 0 { return []*Session{} }
    // fetch ids first
    script := `
local idx_key = KEYS[1]
local start = tonumber(ARGV[1])
local stop = tonumber(ARGV[2])
return redis.call('ZREVRANGE', idx_key, start, stop)`
    keys := []string{s.idxKey()}
    args := []interface{}{offset, offset + limit - 1}
    v, err := s.rc.Eval(script, len(keys), keys, args)
    if err != nil { return []*Session{} }
    ids, ok := toSliceString(v)
    if !ok || len(ids) == 0 { return []*Session{} }
    res := make([]*Session, 0, len(ids))
    for _, id := range ids {
        if st, ok := s.Get(id); ok && st != nil { res = append(res, st) }
    }
    // ensure recency order
    sort.Slice(res, func(i, j int) bool { return res[i].CreatedAt.After(res[j].CreatedAt) })
    return res
}

// Clean keeps only top max sessions by recency
func (s *RedisSessionStore) Clean(max int) error {
    if max <= 0 { return nil }
    script := `
local idx_key = KEYS[1]
local prefix = ARGV[1]
local keep = tonumber(ARGV[2])
local total = redis.call('ZCARD', idx_key)
if total <= keep then return 0 end
local rem = total - keep
local ids = redis.call('ZRANGE', idx_key, 0, rem-1)
for i,id in ipairs(ids) do
  redis.call('ZREM', idx_key, id)
  redis.call('DEL', prefix .. 'session:' .. id)
end
return rem`
    keys := []string{s.idxKey()}
    args := []interface{}{s.prefix, max}
    _, err := s.rc.Eval(script, len(keys), keys, args)
    return err
}

// helpers
func toHash(v interface{}) (map[string]string, bool) {
    arr, ok := v.([]interface{})
    if !ok { return nil, false }
    m := make(map[string]string, len(arr)/2)
    for i := 0; i+1 < len(arr); i += 2 {
        k, kOk := toString(arr[i])
        val, vOk := toString(arr[i+1])
        if kOk && vOk { m[k] = val }
    }
    return m, true
}

func toString(v interface{}) (string, bool) {
    switch t := v.(type) {
    case string:
        return t, true
    case []byte:
        return string(t), true
    default:
        return "", false
    }
}

func toSliceString(v interface{}) ([]string, bool) {
    arr, ok := v.([]interface{})
    if !ok { return nil, false }
    out := make([]string, 0, len(arr))
    for _, it := range arr {
        if s, ok := toString(it); ok { out = append(out, s) }
    }
    return out, true
}

func parseInt64(sv string) (int64, error) {
    var x int64
    _, err := fmt.Sscan(sv, &x)
    return x, err
}

// sanitizeKey ensures no spaces or newlines in keys (not strictly necessary here)
// no-op
