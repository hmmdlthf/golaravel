package ratelimit

import (
	"time"
)

// Attempt executes the callback if the key has not exceeded maxAttempts in window.
// If the limit is reached, it returns ErrTooManyAttempts.
func Attempt(key string, maxAttempts int, window time.Duration, fn func() error) error {
	res, err := Hit(key, maxAttempts, window)
	if err != nil {
		return err
	}
	if !res.Allowed {
		return ErrTooManyAttempts
	}
	return fn()
}

// TooManyAttempts checks if the key has exhausted its allowed attempts in the window.
func TooManyAttempts(key string, maxAttempts int, window time.Duration) bool {
	res, err := Status(key, maxAttempts, window)
	if err != nil {
		return false
	}
	return !res.Allowed
}

// Hit records an attempt and returns the updated rate limit result.
func Hit(key string, maxAttempts int, window time.Duration) (Result, error) {
	return GetStore().Hit(key, maxAttempts, window)
}

// Status inspects the current rate limit status without recording a hit.
func Status(key string, maxAttempts int, window time.Duration) (Result, error) {
	return GetStore().Status(key, maxAttempts, window)
}

// Remaining returns how many requests are remaining in the current window.
func Remaining(key string, maxAttempts int, window time.Duration) int {
	res, err := Status(key, maxAttempts, window)
	if err != nil {
		return maxAttempts
	}
	return res.Remaining
}

// AvailableIn returns the duration to wait until attempts become available again.
func AvailableIn(key string, maxAttempts int, window time.Duration) time.Duration {
	res, err := Status(key, maxAttempts, window)
	if err != nil {
		return 0
	}
	return res.RetryAfter
}

// Reset clears recorded attempts for a specific key.
func Reset(key string) error {
	return GetStore().Reset(key)
}

// Clear flushes all rate limiting records from the active store.
func Clear() error {
	return GetStore().Clear()
}
