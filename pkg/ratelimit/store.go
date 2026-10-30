package ratelimit

import (
	"errors"
	"sync"
	"time"
)

var (
	// ErrTooManyAttempts is returned when an action exceeds the rate limit.
	ErrTooManyAttempts = errors.New("ratelimit: too many attempts")
)

// Result encapsulates the outcome of a rate limit evaluation.
type Result struct {
	Allowed    bool          `json:"allowed"`
	Limit      int           `json:"limit"`
	Remaining  int           `json:"remaining"`
	ResetTime  time.Time     `json:"reset_time"`
	RetryAfter time.Duration `json:"retry_after"`
}

// Store defines the storage engine contract for rate limiting counters and logs.
type Store interface {
	// Hit records an attempt at the current timestamp and returns the rate limit outcome.
	Hit(key string, maxAttempts int, window time.Duration) (Result, error)

	// Status inspects the current rate limit status for a key without incrementing it.
	Status(key string, maxAttempts int, window time.Duration) (Result, error)

	// Reset clears all recorded attempts for a specific key.
	Reset(key string) error

	// Clear purges all rate limit records from storage.
	Clear() error
}

var (
	storeMu      sync.RWMutex
	defaultStore Store = NewMemoryStore()
)

// SetStore configures the global rate limiting store (e.g. MemoryStore or RedisStore).
func SetStore(s Store) {
	storeMu.Lock()
	defer storeMu.Unlock()
	defaultStore = s
}

// GetStore retrieves the currently configured global rate limit store.
func GetStore() Store {
	storeMu.RLock()
	defer storeMu.RUnlock()
	return defaultStore
}
