package cache

import (
	"time"
)

// MemoryCache aliases MemoryStore for backward compatibility.
type MemoryCache = MemoryStore

// Global default cache store
var defaultStore Store = NewMemoryStore()

// SetDefaultStore configures the global cache store.
func SetDefaultStore(s Store) {
	defaultStore = s
}

// DefaultStore returns the active global cache store.
func DefaultStore() Store {
	return defaultStore
}

// Global facade functions matching Laravel's Cache facade:

// Put stores an item in the cache for the specified duration.
func Put(key string, val any, ttl time.Duration) error {
	return defaultStore.Put(key, val, ttl)
}

// Add stores an item only if the key does not already exist.
func Add(key string, val any, ttl time.Duration) (bool, error) {
	return defaultStore.Add(key, val, ttl)
}

// Get retrieves an item from the cache.
func Get(key string) (any, bool) {
	return defaultStore.Get(key)
}

// GetString retrieves a string attribute.
func GetString(key string, defaultVal ...string) string {
	return defaultStore.GetString(key, defaultVal...)
}

// GetInt retrieves an integer attribute.
func GetInt(key string, defaultVal ...int) int {
	return defaultStore.GetInt(key, defaultVal...)
}

// GetBool retrieves a boolean attribute.
func GetBool(key string, defaultVal ...bool) bool {
	return defaultStore.GetBool(key, defaultVal...)
}

// Increment increases an integer value in the cache.
func Increment(key string, value ...int64) (int64, error) {
	return defaultStore.Increment(key, value...)
}

// Decrement decreases an integer value in the cache.
func Decrement(key string, value ...int64) (int64, error) {
	return defaultStore.Decrement(key, value...)
}

// Remember retrieves an item, or computes and caches it if missing.
func Remember(key string, ttl time.Duration, fn func() (any, error)) (any, error) {
	return defaultStore.Remember(key, ttl, fn)
}

// RememberForever stores an item indefinitely.
func RememberForever(key string, fn func() (any, error)) (any, error) {
	return defaultStore.RememberForever(key, fn)
}

// Pull retrieves and removes an item from the cache.
func Pull(key string, defaultVal ...any) any {
	return defaultStore.Pull(key, defaultVal...)
}

// Forget removes an item from the cache.
func Forget(key string) error {
	return defaultStore.Forget(key)
}

// Flush clears all cache items.
func Flush() error {
	return defaultStore.Flush()
}

// Tags returns a TaggedStore scoped to the specified tag names.
func Tags(names ...string) *TaggedStore {
	return defaultStore.Tags(names...)
}

// Lock returns an atomic Lock instance.
func Lock(name string, duration time.Duration, owner ...string) *AtomicLock {
	return defaultStore.Lock(name, duration, owner...)
}
