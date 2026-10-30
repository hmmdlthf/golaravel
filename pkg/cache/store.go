package cache

import (
	"errors"
	"time"
)

var (
	ErrLockNotAcquired = errors.New("cache/lock: unable to acquire lock")
	ErrLockTimeout     = errors.New("cache/lock: lock acquisition timed out")
	ErrLockNotOwned    = errors.New("cache/lock: cannot release lock not owned by this process")
)

// Store defines the core interface for cache storage drivers.
type Store interface {
	Get(key string) (any, bool)
	GetString(key string, defaultVal ...string) string
	GetInt(key string, defaultVal ...int) int
	GetBool(key string, defaultVal ...bool) bool
	Put(key string, val any, ttl time.Duration) error
	Add(key string, val any, ttl time.Duration) (bool, error)
	Increment(key string, value ...int64) (int64, error)
	Decrement(key string, value ...int64) (int64, error)
	Forget(key string) error
	Flush() error
	Remember(key string, ttl time.Duration, fn func() (any, error)) (any, error)
	RememberForever(key string, fn func() (any, error)) (any, error)
	Pull(key string, defaultVal ...any) any
	Tags(names ...string) *TaggedStore
	Lock(name string, duration time.Duration, owner ...string) *AtomicLock
}

// Item represents a cached value with an optional expiration timestamp.
type Item struct {
	Value     any
	ExpiresAt time.Time
}

// IsExpired reports whether the cache item has passed its time-to-live.
func (i *Item) IsExpired() bool {
	if i.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(i.ExpiresAt)
}
