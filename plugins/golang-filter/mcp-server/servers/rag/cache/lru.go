package cache

import (
	"container/list"
	"sync"
	"time"
)

// Cache defines the common interface for L1 caches.
type Cache interface {
	Get(key string) (any, bool)
	Set(key string, value any, ttl time.Duration)
	Purge()
}

type entry struct {
	key     string
	value   any
	expires time.Time
	element *list.Element
}

type lruCache struct {
	mu       sync.Mutex
	capacity int
	ttl      time.Duration
	items    map[string]*entry
	order    *list.List
}

// NewLRU creates an LRU cache with capacity and default TTL.
func NewLRU(capacity int, ttl time.Duration) Cache {
	if capacity <= 0 {
		capacity = 512
	}
	if ttl <= 0 {
		ttl = time.Minute
	}
	return &lruCache{
		capacity: capacity,
		ttl:      ttl,
		items:    make(map[string]*entry, capacity),
		order:    list.New(),
	}
}

func (c *lruCache) Get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ent, ok := c.items[key]; ok {
		if ent.expires.IsZero() || time.Now().Before(ent.expires) {
			c.order.MoveToFront(ent.element)
			return ent.value, true
		}
		c.removeEntry(ent)
	}
	return nil, false
}

func (c *lruCache) Set(key string, value any, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ent, ok := c.items[key]; ok {
		ent.value = value
		ent.expires = c.computeExpiry(ttl)
		c.order.MoveToFront(ent.element)
		return
	}

	if len(c.items) >= c.capacity {
		c.evictOldest()
	}

	elem := c.order.PushFront(key)
	c.items[key] = &entry{
		key:     key,
		value:   value,
		expires: c.computeExpiry(ttl),
		element: elem,
	}
}

func (c *lruCache) Purge() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[string]*entry, c.capacity)
	c.order.Init()
}

func (c *lruCache) computeExpiry(ttl time.Duration) time.Time {
	if ttl <= 0 {
		ttl = c.ttl
	}
	if ttl <= 0 {
		return time.Time{}
	}
	return time.Now().Add(ttl)
}

func (c *lruCache) evictOldest() {
	elem := c.order.Back()
	if elem == nil {
		return
	}
	key := elem.Value.(string)
	if ent, ok := c.items[key]; ok {
		c.removeEntry(ent)
	}
}

func (c *lruCache) removeEntry(ent *entry) {
	if ent.element != nil {
		c.order.Remove(ent.element)
	}
	delete(c.items, ent.key)
}
