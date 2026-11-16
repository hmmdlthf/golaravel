package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	gcontext "github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/hash"
	"github.com/golaravel/golaravel/pkg/ratelimit"
)

// Workflow throttling limits. They use the configured ratelimit store, so
// with ratelimit.SetStore(redis store) they hold across instances.
var (
	// LoginAttempts failed logins per email and client IP per LoginWindow.
	LoginAttempts = 5
	LoginWindow   = time.Minute
	// TwoFactorAttempts failed 2FA codes per user per TwoFactorWindow; a
	// 6-digit code must not be guessable within a challenge's lifetime.
	TwoFactorAttempts = 5
	TwoFactorWindow   = 5 * time.Minute
	// ResetEmails reset emails per address per ResetWindow.
	ResetEmails = 3
	ResetWindow = 15 * time.Minute
)

func throttleKey(parts ...string) string {
	h := sha256.Sum256([]byte(strings.ToLower(strings.Join(parts, "|"))))
	return "auth:" + hex.EncodeToString(h[:16])
}

// throttled reports whether key is over its limit, answering 429 if so.
func throttled(c *gcontext.Context, key string, max int, window time.Duration) bool {
	res, err := ratelimit.Status(key, max, window)
	if err != nil || res.Allowed {
		return false
	}
	secs := int(math.Ceil(res.RetryAfter.Seconds()))
	if secs < 1 {
		secs = 1
	}
	c.SetHeader("Retry-After", strconv.Itoa(secs))
	_ = c.JSON(429, map[string]any{
		"error":       "Too Many Attempts",
		"message":     "Too many attempts. Please try again in " + strconv.Itoa(secs) + " seconds.",
		"retry_after": secs,
	})
	return true
}

func recordFailure(key string, max int, window time.Duration) { _, _ = ratelimit.Hit(key, max, window) }

var (
	dummyHashOnce sync.Once
	dummyHash     string
)

// checkPasswordConstantTime always runs one hash comparison, so a missing
// user takes as long as a wrong password (no account enumeration by timing).
func checkPasswordConstantTime(user AuthenticatableUser, password string) bool {
	if user == nil {
		dummyHashOnce.Do(func() { dummyHash, _ = hash.Make("golaravel-timing-equaliser") })
		_ = hash.Check(password, dummyHash)
		return false
	}
	return hash.Check(password, user.GetPasswordHash())
}

// totpReplay remembers the last time step used per user, so a code cannot
// be used twice within its validity window.
type totpReplay struct {
	mu   sync.Mutex
	last map[string]int64
}

func (r *totpReplay) accept(userID string, step int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.last == nil {
		r.last = map[string]int64{}
	}
	if prev, ok := r.last[userID]; ok && step <= prev {
		return false
	}
	r.last[userID] = step
	return true
}
