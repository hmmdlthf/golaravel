# Sliding-Window Rate Limiter & Tiered Throttling 🚦

GoLaravel provides a mathematically precise **sliding-window rate limiting and throttling engine** (`pkg/ratelimit`) modeled after Laravel's `RateLimiter` architecture. It supports dynamic named limiters, multi-tier user throttling, IETF/GitHub standard HTTP response headers, and pluggable storage backends (in-memory sliding logs and distributed Redis clusters).

---

## 1. Sliding-Window Log vs Fixed-Window

Traditional fixed-window rate limiters suffer from **boundary burst anomalies**: a client allowed 60 requests per minute can send 60 requests at 00:59 and another 60 requests at 01:00, causing a 120-request burst within a two-second window.

GoLaravel implements a **Sliding-Window Log** algorithm:
- Tracks exact nanosecond timestamps for every request.
- Automatically slides the evaluation window $[T - \text{window}, T]$ continuously.
- Calculates exact `Remaining` requests and accurate sub-second `Retry-After` wait times.
- Zero boundary bursting.

---

## 2. Defining Named Rate Limiters

Register named limiters during application bootstrap (for example in `app/limiters/` or in `main.go`) using `ratelimit.For`:

```go
import (
    "github.com/golaravel/golaravel/pkg/auth"
    "github.com/golaravel/golaravel/pkg/context"
    "github.com/golaravel/golaravel/pkg/ratelimit"
)

func init() {
    // 1. Basic IP-based rate limiter (60 requests per minute)
    ratelimit.For("global", func(c *context.Context) *ratelimit.Limit {
        return ratelimit.PerMinute(60).By(c.ClientIP())
    })

    // 2. Strict burst limiter for sensitive endpoints (e.g. login)
    ratelimit.For("login", func(c *context.Context) *ratelimit.Limit {
        email := c.PostForm("email")
        return ratelimit.PerMinute(5).By(email + "|" + c.ClientIP()).
            Response(func(c *context.Context, res ratelimit.Result) error {
                return c.JSON(429, map[string]string{
                    "error":   "Too Many Login Attempts",
                    "message": "Too many consecutive login failures. Please wait before retrying.",
                })
            })
    })
}
```

### Fluent Interval Helpers
| Constructor | Sliding Window | Use Case |
|---|---|---|
| `ratelimit.PerSecond(10)` | 1 second | Micro-burst protection for WebSocket or streaming endpoints |
| `ratelimit.PerMinute(60)` | 1 minute | General REST API endpoints |
| `ratelimit.PerHour(1000)` | 1 hour | Heavy data export or report generation |
| `ratelimit.PerDay(10000)` | 24 hours | Daily API consumption quotas |
| `ratelimit.None()` | Unlimited | Bypasses throttling completely (e.g. enterprise clients) |

---

## 3. Tiered Throttling Strategies

Dynamically adapt throughput limits based on authentication status and user subscription tiers:

```go
ratelimit.For("api", func(c *context.Context) *ratelimit.Limit {
    user := auth.User(c)

    // Unauthenticated Guest: 30 requests/minute by IP
    if user == nil {
        return ratelimit.PerMinute(30).By(c.ClientIP())
    }

    // Enterprise Tier: Unlimited throughput
    if user.GetTier() == "enterprise" {
        return ratelimit.None()
    }

    // Pro Tier: 1,000 requests/minute
    if user.GetTier() == "pro" {
        return ratelimit.PerMinute(1000).By(user.GetAuthIdentifier())
    }

    // Free Tier: 120 requests/minute
    return ratelimit.PerMinute(120).By(user.GetAuthIdentifier())
})
```

### Compound Multi-Rule Throttling
A resolver can return `[]*ratelimit.Limit` to enforce concurrent limits (e.g. burst limit AND sustained limit):

```go
ratelimit.For("burst-api", func(c *context.Context) []*ratelimit.Limit {
    return []*ratelimit.Limit{
        ratelimit.PerSecond(10).By(c.ClientIP()),  // Max 10 reqs/sec burst
        ratelimit.PerMinute(300).By(c.ClientIP()), // Max 300 reqs/min sustained
    }
})
```

---

## 4. HTTP Throttling Middleware

Attach rate limiting to routes or route groups:

```go
app := golaravel.New()

// Attach named limiter to a route group
api := app.Group("/api", ratelimit.Throttle("api"))
{
    api.GET("/users", listUsers)
    api.POST("/orders", createOrder)
}

// Inline sliding-window throttling without named limiter
app.GET("/search", ratelimit.ThrottleMax(10, 10*time.Second), searchHandler)
```

### Standard HTTP Response Headers
All requests passing through the throttling middleware receive RFC/GitHub rate limit headers:

```http
HTTP/1.1 200 OK
X-RateLimit-Limit: 60
X-RateLimit-Remaining: 57
X-RateLimit-Reset: 1793404860
```

When a client is throttled, GoLaravel automatically halts the request, sets the `Retry-After` header, and issues an HTTP `429 Too Many Requests`:

```http
HTTP/1.1 429 Too Many Requests
Content-Type: application/json
X-RateLimit-Limit: 60
X-RateLimit-Remaining: 0
X-RateLimit-Reset: 1793404860
Retry-After: 14

{
  "error": "Too Many Requests",
  "message": "Too many requests. Please retry in 14 seconds.",
  "retry_after": 14
}
```

---

## 5. Programmatic Rate Limiter API

Use rate limiting directly in services, queue jobs, or background tasks outside the HTTP pipeline:

```go
// 1. Attempt helper: executes callback only if limit is not exhausted
err := ratelimit.Attempt("charge:user_123", 3, 1*time.Minute, func() error {
    return paymentGateway.Charge(...)
})
if errors.Is(err, ratelimit.ErrTooManyAttempts) {
    // Throttled!
}

// 2. Check remaining attempts
remaining := ratelimit.Remaining("otp:phone:+1234567890", 3, 10*time.Minute)

// 3. Check retry wait duration
waitDuration := ratelimit.AvailableIn("otp:phone:+1234567890", 3, 10*time.Minute)

// 4. Manually reset / clear limit on success
if loginSuccessful {
    _ = ratelimit.Reset("login:user@example.com")
}
```

---

## 6. Distributed Redis Clustering

For multi-node cloud deployments (e.g. Kubernetes with multiple replica pods), switch storage from `MemoryStore` to `RedisStore`. It uses Redis sorted sets (`ZSET`) via Go's built-in socket RESP engine without third-party dependencies:

```go
import "github.com/golaravel/golaravel/pkg/ratelimit"

// Automatically connects to REDIS_HOST:REDIS_PORT with password and prefix
redisStore, err := ratelimit.NewRedisStore(ratelimit.DefaultRedisConfig())
if err != nil {
    log.Fatalf("failed to connect redis rate limiter: %v", err)
}

ratelimit.SetStore(redisStore)
```

---

## 7. Artisan Limiter Scaffolder

Scaffold a new rate limiter class using Artisan:

```bash
artisan make:limiter Api
```

Creates `app/limiters/api_limiter.go` pre-configured with tiered user logic:

```go
package limiters

import (
    "github.com/golaravel/golaravel/pkg/auth"
    "github.com/golaravel/golaravel/pkg/context"
    "github.com/golaravel/golaravel/pkg/ratelimit"
)

func RegisterApiLimiter() {
    ratelimit.For("api", func(c *context.Context) *ratelimit.Limit {
        user := auth.User(c)
        if user != nil {
            return ratelimit.PerMinute(120).By(user.GetAuthIdentifier())
        }
        return ratelimit.PerMinute(30).By(c.ClientIP())
    })
}
```
