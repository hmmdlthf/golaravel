package cache

import (
	"fmt"
	"sync"
	"time"
)

// MemoryStore is a high-performance in-memory cache supporting TaggedStore and Atomic Locks.
type MemoryStore struct {
	mu           sync.RWMutex
	items        map[string]*Item
	tagManager   *TagManager
	lockProvider *MemoryLockProvider
	flights      flightGroup
}

// NewMemoryStore creates a new memory-backed cache store.
func NewMemoryStore() *MemoryStore {
	ms := &MemoryStore{
		items:        make(map[string]*Item, 1024),
		tagManager:   NewTagManager(),
		lockProvider: NewMemoryLockProvider(),
	}
	go ms.evictionLoop()
	return ms
}

// Get retrieves an item from the cache.
func (s *MemoryStore) Get(key string) (any, bool) {
	s.mu.RLock()
	item, found := s.items[key]
	s.mu.RUnlock()

	if !found || item.IsExpired() {
		return nil, false
	}
	return item.Value, true
}

// GetString retrieves a string attribute.
func (s *MemoryStore) GetString(key string, defaultVal ...string) string {
	if val, ok := s.Get(key); ok {
		if str, ok := val.(string); ok {
			return str
		}
		return fmt.Sprint(val)
	}
	if len(defaultVal) > 0 {
		return defaultVal[0]
	}
	return ""
}

// GetInt retrieves an integer attribute.
func (s *MemoryStore) GetInt(key string, defaultVal ...int) int {
	if val, ok := s.Get(key); ok {
		switch v := val.(type) {
		case int:
			return v
		case int64:
			return int(v)
		case float64:
			return int(v)
		}
	}
	if len(defaultVal) > 0 {
		return defaultVal[0]
	}
	return 0
}

// GetBool retrieves a boolean attribute.
func (s *MemoryStore) GetBool(key string, defaultVal ...bool) bool {
	if val, ok := s.Get(key); ok {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	if len(defaultVal) > 0 {
		return defaultVal[0]
	}
	return false
}

// Put stores an item in the cache for the specified duration.
func (s *MemoryStore) Put(key string, val any, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}

	s.items[key] = &Item{
		Value:     val,
		ExpiresAt: exp,
	}
	return nil
}

// Add stores an item only if the key does not already exist (or has expired).
func (s *MemoryStore) Add(key string, val any, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	item, exists := s.items[key]
	if exists && !item.IsExpired() {
		return false, nil
	}

	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}

	s.items[key] = &Item{
		Value:     val,
		ExpiresAt: exp,
	}
	return true, nil
}

// Increment increases an integer value in the cache.
func (s *MemoryStore) Increment(key string, value ...int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	by := int64(1)
	if len(value) > 0 {
		by = value[0]
	}

	item, exists := s.items[key]
	var current int64

	if exists && !item.IsExpired() {
		switch v := item.Value.(type) {
		case int:
			current = int64(v)
		case int64:
			current = v
		}
	}

	current += by
	s.items[key] = &Item{Value: current}
	return current, nil
}

// Decrement decreases an integer value in the cache.
func (s *MemoryStore) Decrement(key string, value ...int64) (int64, error) {
	by := int64(1)
	if len(value) > 0 {
		by = value[0]
	}
	return s.Increment(key, -by)
}

// Forget removes an item from the cache.
func (s *MemoryStore) Forget(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, key)
	return nil
}

// Flush clears all cache items.
func (s *MemoryStore) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = make(map[string]*Item, 1024)
	return nil
}

// Remember retrieves an item, or computes and caches it if missing.
//
// Concurrent callers that miss the same key wait for one computation
// instead of all running fn (cache stampede protection).
func (s *MemoryStore) Remember(key string, ttl time.Duration, fn func() (any, error)) (any, error) {
	if val, ok := s.Get(key); ok {
		return val, nil
	}
	return s.flights.do(key, func() (any, error) {
		if val, ok := s.Get(key); ok {
			return val, nil
		}
		val, err := fn()
		if err != nil {
			return nil, err
		}
		_ = s.Put(key, val, ttl)
		return val, nil
	})
}

// RememberForever stores an item indefinitely.
func (s *MemoryStore) RememberForever(key string, fn func() (any, error)) (any, error) {
	return s.Remember(key, 0, fn)
}

// Pull retrieves and removes an item from the cache.
func (s *MemoryStore) Pull(key string, defaultVal ...any) any {
	s.mu.Lock()
	defer s.mu.Unlock()

	item, exists := s.items[key]
	if exists && !item.IsExpired() {
		delete(s.items, key)
		return item.Value
	}

	if len(defaultVal) > 0 {
		return defaultVal[0]
	}
	return nil
}

// Tags returns a TaggedStore scoped to the specified tag names.
func (s *MemoryStore) Tags(names ...string) *TaggedStore {
	return NewTaggedStore(s, s.tagManager, names...)
}

// Lock returns an atomic Lock instance.
func (s *MemoryStore) Lock(name string, duration time.Duration, owner ...string) *AtomicLock {
	return NewLock(s.lockProvider, name, duration, owner...)
}

func (s *MemoryStore) evictionLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		s.mu.Lock()
		now := time.Now()
		for k, v := range s.items {
			if !v.ExpiresAt.IsZero() && now.After(v.ExpiresAt) {
				delete(s.items, k)
			}
		}
		s.mu.Unlock()
	}
}
