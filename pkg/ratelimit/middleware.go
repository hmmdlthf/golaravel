package ratelimit

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/golaravel/golaravel/pkg/context"
)

// Throttle returns an HTTP middleware enforcing the specified named rate limiter.
func Throttle(name string) context.HandlerFunc {
	return func(c *context.Context) error {
		limits := Resolve(name, c)
		if len(limits) == 0 {
			// No limit rule defined for this request, pass through
			return c.Next()
		}

		store := GetStore()

		// Evaluate all limits sequentially (e.g. tier limits, burst + sustained limits)
		for _, lim := range limits {
			if lim == nil || lim.Unlimited {
				continue
			}

			key := lim.Key
			if key == "" {
				key = c.ClientIP()
			}
			storageKey := fmt.Sprintf("rl:%s:%s", name, key)

			res, err := store.Hit(storageKey, lim.MaxAttempts, lim.Decay)
			if err != nil {
				// On store failure, fail-open to avoid taking down traffic
				return c.Next()
			}

			// Set standard IETF / GitHub rate limit headers
			c.SetHeader("X-RateLimit-Limit", strconv.Itoa(res.Limit))
			c.SetHeader("X-RateLimit-Remaining", strconv.Itoa(res.Remaining))
			c.SetHeader("X-RateLimit-Reset", strconv.FormatInt(res.ResetTime.Unix(), 10))

			if !res.Allowed {
				retrySeconds := int(math.Ceil(res.RetryAfter.Seconds()))
				if retrySeconds < 1 {
					retrySeconds = 1
				}
				c.SetHeader("Retry-After", strconv.Itoa(retrySeconds))
				c.AbortWithStatus(http.StatusTooManyRequests)

				if lim.ResponseCallback != nil {
					return lim.ResponseCallback(c, res)
				}

				return c.JSON(http.StatusTooManyRequests, map[string]any{
					"error":       "Too Many Requests",
					"message":     fmt.Sprintf("Too many requests. Please retry in %d seconds.", retrySeconds),
					"retry_after": retrySeconds,
				})
			}
		}

		return c.Next()
	}
}

// ThrottleMax returns an HTTP middleware enforcing an inline sliding-window threshold.
func ThrottleMax(maxAttempts int, window time.Duration, keyResolver ...func(c *context.Context) string) context.HandlerFunc {
	return func(c *context.Context) error {
		key := c.ClientIP()
		if len(keyResolver) > 0 && keyResolver[0] != nil {
			if customKey := keyResolver[0](c); customKey != "" {
				key = customKey
			}
		}

		storageKey := fmt.Sprintf("rl:inline:%s", key)
		store := GetStore()

		res, err := store.Hit(storageKey, maxAttempts, window)
		if err != nil {
			return c.Next()
		}

		c.SetHeader("X-RateLimit-Limit", strconv.Itoa(res.Limit))
		c.SetHeader("X-RateLimit-Remaining", strconv.Itoa(res.Remaining))
		c.SetHeader("X-RateLimit-Reset", strconv.FormatInt(res.ResetTime.Unix(), 10))

		if !res.Allowed {
			retrySeconds := int(math.Ceil(res.RetryAfter.Seconds()))
			if retrySeconds < 1 {
				retrySeconds = 1
			}
			c.SetHeader("Retry-After", strconv.Itoa(retrySeconds))
			c.AbortWithStatus(http.StatusTooManyRequests)

			return c.JSON(http.StatusTooManyRequests, map[string]any{
				"error":       "Too Many Requests",
				"message":     fmt.Sprintf("Too many requests. Please retry in %d seconds.", retrySeconds),
				"retry_after": retrySeconds,
			})
		}

		return c.Next()
	}
}
