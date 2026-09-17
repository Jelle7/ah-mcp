package tools

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	CacheTTLSearch  = 5 * time.Minute
	CacheTTLProduct = 10 * time.Minute
	CacheTTLBonus   = 2 * time.Minute
	CacheTTLStores  = 30 * time.Minute

	// cacheMaxEntries caps the cache so a long-running server cannot grow it
	// without bound — entries are only reclaimed lazily on read otherwise.
	cacheMaxEntries = 512
)

type cacheEntry struct {
	data      []byte
	expiresAt time.Time
}

// Cache is a simple in-memory TTL cache storing serialised JSON bytes.
type Cache struct {
	mu         sync.Mutex
	entries    map[string]cacheEntry
	maxEntries int
}

// GlobalCache is the shared cache instance used by all tool handlers.
var GlobalCache = NewCache(cacheMaxEntries)

// NewCache returns an empty cache holding at most maxEntries entries.
func NewCache(maxEntries int) *Cache {
	if maxEntries <= 0 {
		maxEntries = cacheMaxEntries
	}
	return &Cache{entries: make(map[string]cacheEntry), maxEntries: maxEntries}
}

// Get returns cached bytes for key. Returns nil, false on miss or expiry.
func (c *Cache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expiresAt) {
		delete(c.entries, key)
		return nil, false
	}
	return e.data, true
}

// Set stores data under key with the given TTL, evicting if necessary.
func (c *Cache) Set(key string, data []byte, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; !exists && len(c.entries) >= c.maxEntries {
		c.evictLocked()
	}
	c.entries[key] = cacheEntry{data: data, expiresAt: time.Now().Add(ttl)}
}

// evictLocked drops expired entries, falling back to the entry closest to
// expiry when nothing has expired yet. Callers must hold c.mu.
func (c *Cache) evictLocked() {
	now := time.Now()
	evicted := false
	for k, e := range c.entries {
		if now.After(e.expiresAt) {
			delete(c.entries, k)
			evicted = true
		}
	}
	if evicted {
		return
	}
	var oldestKey string
	var oldest time.Time
	for k, e := range c.entries {
		if oldestKey == "" || e.expiresAt.Before(oldest) {
			oldestKey, oldest = k, e.expiresAt
		}
	}
	if oldestKey != "" {
		delete(c.entries, oldestKey)
	}
}

// Invalidate removes all entries whose key starts with prefix.
func (c *Cache) Invalidate(prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.entries {
		if strings.HasPrefix(k, prefix) {
			delete(c.entries, k)
		}
	}
}

// Size returns the number of non-expired entries currently in the cache.
func (c *Cache) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	n := 0
	for k, e := range c.entries {
		if now.After(e.expiresAt) {
			delete(c.entries, k)
		} else {
			n++
		}
	}
	return n
}

// Cache key helpers.

func SearchCacheKey(query string, limit int) string {
	return fmt.Sprintf("search:%s:%d", query, limit)
}

func ProductCacheKey(id int) string {
	return fmt.Sprintf("product:%d", id)
}

func ProductFullCacheKey(id int) string {
	return fmt.Sprintf("product_full:%d", id)
}

func OrderCacheKey(id int) string {
	return fmt.Sprintf("order:%d", id)
}
