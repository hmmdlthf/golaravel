package ratelimit

import (
	"fmt"
	"sync"
	"time"

	"github.com/golaravel/golaravel/pkg/context"
)

// Limit represents a single throttling threshold rule.
type Limit struct {
	MaxAttempts      int
	Decay            time.Duration
	Key              string
	Unlimited        bool
	ResponseCallback func(c *context.Context, res Result) error
}

// PerSecond creates a rate limit rule for a 1-second sliding window.
func PerSecond(max int) *Limit {
	return &Limit{
		MaxAttempts: max,
		Decay:       1 * time.Second,
	}
}

// PerMinute creates a rate limit rule for a 1-minute sliding window.
func PerMinute(max int) *Limit {
	return &Limit{
		MaxAttempts: max,
		Decay:       1 * time.Minute,
	}
}

// PerHour creates a rate limit rule for a 1-hour sliding window.
func PerHour(max int) *Limit {
	return &Limit{
		MaxAttempts: max,
		Decay:       1 * time.Hour,
	}
}

// PerDay creates a rate limit rule for a 24-hour sliding window.
func PerDay(max int) *Limit {
	return &Limit{
		MaxAttempts: max,
		Decay:       24 * time.Hour,
	}
}

// None creates an unrestricted limit (bypasses rate limiting).
func None() *Limit {
	return &Limit{
		Unlimited: true,
	}
}

// By sets the partition / tracking key (e.g. client IP, user ID, API token).
func (l *Limit) By(key string) *Limit {
	l.Key = key
	return l
}

// Response configures a custom HTTP response handler invoked when throttled.
func (l *Limit) Response(fn func(c *context.Context, res Result) error) *Limit {
	l.ResponseCallback = fn
	return l
}

var (
	limitersMu sync.RWMutex
	limiters   = make(map[string]func(c *context.Context) []*Limit)
)

// For registers a named rate limiter resolver callback (modeled after Laravel RateLimiter::for).
// The resolver function can return *Limit or []*Limit.
func For(name string, resolver any) {
	limitersMu.Lock()
	defer limitersMu.Unlock()

	switch fn := resolver.(type) {
	case func(c *context.Context) *Limit:
		limiters[name] = func(c *context.Context) []*Limit {
			lim := fn(c)
			if lim == nil {
				return nil
			}
			return []*Limit{lim}
		}
	case func(c *context.Context) []*Limit:
		limiters[name] = fn
	default:
		panic(fmt.Sprintf("ratelimit.For: invalid resolver signature for '%s'", name))
	}
}

// Resolve looks up and resolves the limit rules for a named limiter.
func Resolve(name string, c *context.Context) []*Limit {
	limitersMu.RLock()
	resolver, ok := limiters[name]
	limitersMu.RUnlock()

	if !ok || resolver == nil {
		return nil
	}
	return resolver(c)
}

// ClearNamedLimiters flushes all registered named limiters.
func ClearNamedLimiters() {
	limitersMu.Lock()
	defer limitersMu.Unlock()
	limiters = make(map[string]func(c *context.Context) []*Limit)
}
