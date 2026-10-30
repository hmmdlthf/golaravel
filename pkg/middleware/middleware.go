package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/golaravel/golaravel/pkg/context"
)

// Recovery returns a middleware that recovers from any panics and writes a 500 response.
func Recovery() context.HandlerFunc {
	return func(c *context.Context) error {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				slog.Error("panic recovered",
					"error", fmt.Sprint(r),
					"path", c.Request.URL.Path,
					"stack", string(stack),
				)
				if !c.Response.Written() {
					_ = c.JSON(http.StatusInternalServerError, map[string]string{
						"error":   "Internal Server Error",
						"message": fmt.Sprint(r),
					})
				}
				c.Abort()
			}
		}()
		return c.Next()
	}
}

// Logger returns a high-performance structured request logger.
func Logger() context.HandlerFunc {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	return func(c *context.Context) error {
		start := time.Now()
		path := c.Request.URL.Path
		rawQuery := c.Request.URL.RawQuery

		err := c.Next()

		latency := time.Since(start)
		status := c.Response.Status()

		if rawQuery != "" {
			path = path + "?" + rawQuery
		}

		logger.Info("http request",
			"status", status,
			"method", c.Request.Method,
			"path", path,
			"ip", c.Request.RemoteAddr,
			"latency", latency,
		)

		return err
	}
}

// CORSOptions configures Cross-Origin Resource Sharing.
type CORSOptions struct {
	AllowedOrigins   []string
	AllowedMethods   []string
	AllowedHeaders   []string
	ExposedHeaders   []string
	AllowCredentials bool
	MaxAge           int
}

// DefaultCORS returns safe default CORS settings.
func DefaultCORS() CORSOptions {
	return CORSOptions{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Requested-With"},
		MaxAge:         86400,
	}
}

// CORS returns a high-performance CORS middleware.
func CORS(opts ...CORSOptions) context.HandlerFunc {
	config := DefaultCORS()
	if len(opts) > 0 {
		config = opts[0]
	}

	methodsHeader := strings.Join(config.AllowedMethods, ", ")
	headersHeader := strings.Join(config.AllowedHeaders, ", ")
	maxAgeHeader := fmt.Sprintf("%d", config.MaxAge)

	return func(c *context.Context) error {
		origin := c.Header("Origin")
		if origin != "" {
			if len(config.AllowedOrigins) == 1 && config.AllowedOrigins[0] == "*" {
				c.SetHeader("Access-Control-Allow-Origin", "*")
			} else {
				c.SetHeader("Access-Control-Allow-Origin", origin)
			}

			if config.AllowCredentials {
				c.SetHeader("Access-Control-Allow-Credentials", "true")
			}
			if len(config.ExposedHeaders) > 0 {
				c.SetHeader("Access-Control-Expose-Headers", strings.Join(config.ExposedHeaders, ", "))
			}
		}

		// Handle Preflight
		if c.Request.Method == http.MethodOptions {
			c.SetHeader("Access-Control-Allow-Methods", methodsHeader)
			c.SetHeader("Access-Control-Allow-Headers", headersHeader)
			c.SetHeader("Access-Control-Max-Age", maxAgeHeader)
			c.AbortWithStatus(http.StatusNoContent)
			return nil
		}

		return c.Next()
	}
}

// SecureHeaders adds security best practice headers to all responses.
func SecureHeaders() context.HandlerFunc {
	return func(c *context.Context) error {
		c.SetHeader("X-Content-Type-Options", "nosniff")
		c.SetHeader("X-Frame-Options", "SAMEORIGIN")
		c.SetHeader("X-XSS-Protection", "1; mode=block")
		c.SetHeader("Referrer-Policy", "strict-origin-when-cross-origin")
		return c.Next()
	}
}

// RateLimiter implements a fast in-memory token bucket rate limiter per client IP.
func RateLimiter(requestsPerMinute int) context.HandlerFunc {
	type clientBucket struct {
		tokens    float64
		lastCheck time.Time
	}

	var mu sync.Mutex
	clients := make(map[string]*clientBucket, 1024)
	rate := float64(requestsPerMinute) / 60.0
	capacity := float64(requestsPerMinute)

	return func(c *context.Context) error {
		ip := c.Request.RemoteAddr
		if colon := strings.LastIndex(ip, ":"); colon != -1 {
			ip = ip[:colon]
		}

		now := time.Now()

		mu.Lock()
		bucket, exists := clients[ip]
		if !exists {
			bucket = &clientBucket{
				tokens:    capacity - 1,
				lastCheck: now,
			}
			clients[ip] = bucket
			mu.Unlock()
			return c.Next()
		}

		elapsed := now.Sub(bucket.lastCheck).Seconds()
		bucket.lastCheck = now
		bucket.tokens += elapsed * rate
		if bucket.tokens > capacity {
			bucket.tokens = capacity
		}

		if bucket.tokens < 1.0 {
			mu.Unlock()
			c.SetHeader("Retry-After", "1")
			c.AbortWithStatus(http.StatusTooManyRequests)
			return c.JSON(http.StatusTooManyRequests, map[string]string{
				"error":   "Too Many Requests",
				"message": "Rate limit exceeded. Please retry later.",
			})
		}

		bucket.tokens -= 1.0
		mu.Unlock()

		return c.Next()
	}
}
