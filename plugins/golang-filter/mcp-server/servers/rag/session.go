package rag

import (
    "sort"
    "sync"
    "time"

    "github.com/google/uuid"
)

// ChatMessage represents a single chat turn.
type ChatMessage struct {
    Role      string    `json:"role"`
    Content   string    `json:"content"`
    Timestamp time.Time `json:"timestamp"`
}

// Session holds chat messages with creation time.
type Session struct {
    ID        string        `json:"session_id"`
    CreatedAt time.Time     `json:"created_at"`
    Messages  []ChatMessage `json:"messages"`
}

// SessionStore is an abstraction for session persistence.
type SessionStore interface {
    Create() *Session
    Get(id string) (*Session, bool)
    Delete(id string) bool
    List() []*Session
    AddMessage(id string, msg ChatMessage) bool
    // ListRange returns sessions from offset with limit, ordered by recency (desc)
    ListRange(offset, limit int) []*Session
    // Clean keeps at most max sessions (by recency); returns error if failed.
    Clean(max int) error
}

// MemSessionStore manages sessions in memory.
type MemSessionStore struct {
    mu       sync.RWMutex
    sessions map[string]*Session
}

func NewMemSessionStore() *MemSessionStore {
    return &MemSessionStore{sessions: make(map[string]*Session)}
}

func (m *MemSessionStore) Create() *Session {
    s := &Session{ID: newID(), CreatedAt: time.Now(), Messages: []ChatMessage{}}
    m.mu.Lock()
    m.sessions[s.ID] = s
    m.mu.Unlock()
    return s
}

func (m *MemSessionStore) Get(id string) (*Session, bool) {
    m.mu.RLock()
    s, ok := m.sessions[id]
    m.mu.RUnlock()
    return s, ok
}

func (m *MemSessionStore) Delete(id string) bool {
    m.mu.Lock()
    _, ok := m.sessions[id]
    if ok { delete(m.sessions, id) }
    m.mu.Unlock()
    return ok
}

func (m *MemSessionStore) List() []*Session {
    m.mu.RLock()
    out := make([]*Session, 0, len(m.sessions))
    for _, s := range m.sessions { out = append(out, s) }
    m.mu.RUnlock()
    // order by CreatedAt desc for convenience
    sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
    return out
}

func (m *MemSessionStore) ListRange(offset, limit int) []*Session {
    list := m.List()
    if offset < 0 { offset = 0 }
    if limit <= 0 { return []*Session{} }
    if offset >= len(list) { return []*Session{} }
    end := offset + limit
    if end > len(list) { end = len(list) }
    return list[offset:end]
}

func (m *MemSessionStore) Clean(max int) error {
    if max <= 0 { return nil }
    m.mu.Lock()
    out := make([]*Session, 0, len(m.sessions))
    for _, s := range m.sessions { out = append(out, s) }
    sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
    if len(out) <= max { m.mu.Unlock(); return nil }
    for _, s := range out[max:] { delete(m.sessions, s.ID) }
    m.mu.Unlock()
    return nil
}

func (m *MemSessionStore) AddMessage(id string, msg ChatMessage) bool {
    m.mu.Lock()
    s, ok := m.sessions[id]
    if ok {
        s.Messages = append(s.Messages, msg)
    }
    m.mu.Unlock()
    return ok
}

// newID creates a lightweight random id. Falls back to timestamp if UUID not available at build time.
func newID() string { return uuid.New().String() }
