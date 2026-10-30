package cache

import (
	"sync"
	"time"
)

type cacheItem struct {
	value     any
	expiresAt time.Time
}

func (ci *cacheItem) isExpired() bool {
	if ci.expiresAt.IsZero() {
		return false
	}
	return time.Now().After(ci.expiresAt)
}

// MemoryCache is an ultra-fast in-memory cache with TTL eviction.
type MemoryCache struct {
	mu    sync.RWMutex
	items map[string]*cacheItem
}

// Global default cache
var globalCache = NewMemoryCache()

// NewMemoryCache creates a new in-memory cache.
func NewMemoryCache() *MemoryCache {
	c := &MemoryCache{
		items: make(map[string]*cacheItem, 1024),
	}
	// Background eviction goroutine
	go c.evictionLoop()
	return c
}

// Put stores an item in the cache for the specified duration.
func (c *MemoryCache) Put(key string, val any, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}

	c.items[key] = &cacheItem{
		value:     val,
		expiresAt: exp,
	}
}

// Get retrieves an item from the cache.
func (c *MemoryCache) Get(key string) (any, bool) {
	c.mu.RLock()
	item, found := c.items[key]
	c.mu.RUnlock()

	if !found || item.isExpired() {
		return nil, false
	}
	return item.value, true
}

// GetString retrieves a string value from the cache.
func (c *MemoryCache) GetString(key string) string {
	if val, ok := c.Get(key); ok {
		if s, ok := val.(string); ok {
			return s
		}
	}
	return ""
}

// Remember retrieves an item, or computes and caches it if missing.
func (c *MemoryCache) Remember(key string, ttl time.Duration, fn func() (any, error)) (any, error) {
	if val, ok := c.Get(key); ok {
		return val, nil
	}

	val, err := fn()
	if err != nil {
		return nil, err
	}

	c.Put(key, val, ttl)
	return val, nil
}

// Forget removes an item from the cache.
func (c *MemoryCache) Forget(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, key)
}

// Flush clears all cache items.
func (c *MemoryCache) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[string]*cacheItem, 1024)
}

func (c *MemoryCache) evictionLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for k, v := range c.items {
			if !v.expiresAt.IsZero() && now.After(v.expiresAt) {
				delete(c.items, k)
			}
		}
		c.mu.Unlock()
	}
}

// Global helpers
func Put(key string, val any, ttl time.Duration)                          { globalCache.Put(key, val, ttl) }
func Get(key string) (any, bool)                                          { return globalCache.Get(key) }
func GetString(key string) string                                         { return globalCache.GetString(key) }
func Remember(key string, ttl time.Duration, fn func() (any, error)) (any, error) {
	return globalCache.Remember(key, ttl, fn)
}
func Forget(key string) { globalCache.Forget(key) }
func Flush()            { globalCache.Flush() }
