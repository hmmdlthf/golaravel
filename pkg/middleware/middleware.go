package middleware

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/ratelimit"
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
				// The panic value is logged above, never sent to the client:
				// it can carry SQL, file paths or secrets.
				if !c.Response.Written() {
					_ = c.JSON(http.StatusInternalServerError, map[string]string{
						"error": "Internal Server Error",
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
			"ip", c.ClientIP(),
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
	// AllowOriginFunc, when set, is asked about origins not listed in
	// AllowedOrigins (for example to allow http://localhost:* in development).
	AllowOriginFunc func(origin string) bool
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

	wildcard := false
	allowed := make(map[string]bool, len(config.AllowedOrigins))
	for _, o := range config.AllowedOrigins {
		if o == "*" {
			wildcard = true
			continue
		}
		allowed[strings.ToLower(strings.TrimRight(o, "/"))] = true
	}

	return func(c *context.Context) error {
		origin := c.Header("Origin")
		c.Response.Header().Add("Vary", "Origin")
		if origin != "" {
			// A listed origin (or one AllowOriginFunc accepts) is reflected and
			// may send credentials. "*" allows any origin, never with
			// credentials. Anything else gets no CORS headers at all.
			listed := allowed[strings.ToLower(origin)] || (config.AllowOriginFunc != nil && config.AllowOriginFunc(origin))
			switch {
			case listed:
				c.SetHeader("Access-Control-Allow-Origin", origin)
				if config.AllowCredentials {
					c.SetHeader("Access-Control-Allow-Credentials", "true")
				}
			case wildcard:
				c.SetHeader("Access-Control-Allow-Origin", "*")
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

// SecureHeadersOptions configures SecureHeaders.
type SecureHeadersOptions struct {
	// ContentSecurityPolicy is sent as Content-Security-Policy when set.
	// StrictCSP is a reasonable starting point for server-rendered apps.
	ContentSecurityPolicy string
	// CSPReportOnly sends the policy as Content-Security-Policy-Report-Only,
	// to try a policy without breaking pages.
	CSPReportOnly bool
	// FrameOptions overrides X-Frame-Options (default "SAMEORIGIN").
	FrameOptions string
	// HSTSMaxAge overrides the HSTS max-age in seconds on HTTPS requests
	// (default one year); a negative value disables HSTS.
	HSTSMaxAge int
	// PermissionsPolicy is sent as Permissions-Policy when set.
	PermissionsPolicy string
}

// StrictCSP allows only same-origin scripts, styles, images and frames, and
// no plugins. Pages with inline scripts or styles need nonces or hashes, or
// a looser policy.
const StrictCSP = "default-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'self'; form-action 'self'"

// SecureHeaders adds security best practice headers to all responses.
func SecureHeaders(opts ...SecureHeadersOptions) context.HandlerFunc {
	var o SecureHeadersOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	frame := o.FrameOptions
	if frame == "" {
		frame = "SAMEORIGIN"
	}
	hsts := ""
	switch {
	case o.HSTSMaxAge == 0:
		hsts = "max-age=31536000"
	case o.HSTSMaxAge > 0:
		hsts = fmt.Sprintf("max-age=%d", o.HSTSMaxAge)
	}
	cspHeader := "Content-Security-Policy"
	if o.CSPReportOnly {
		cspHeader = "Content-Security-Policy-Report-Only"
	}
	return func(c *context.Context) error {
		c.SetHeader("X-Content-Type-Options", "nosniff")
		c.SetHeader("X-Frame-Options", frame)
		// The legacy XSS auditor is itself exploitable; "0" disables it
		// (current OWASP advice). Use a Content-Security-Policy instead.
		c.SetHeader("X-XSS-Protection", "0")
		c.SetHeader("Referrer-Policy", "strict-origin-when-cross-origin")
		if o.ContentSecurityPolicy != "" {
			c.SetHeader(cspHeader, o.ContentSecurityPolicy)
		}
		if o.PermissionsPolicy != "" {
			c.SetHeader("Permissions-Policy", o.PermissionsPolicy)
		}
		if hsts != "" && (c.Request.TLS != nil || strings.EqualFold(c.Header("X-Forwarded-Proto"), "https")) {
			c.SetHeader("Strict-Transport-Security", hsts)
		}
		return c.Next()
	}
}

// RateLimiter limits each client IP to requestsPerMinute requests per
// minute (sliding window).
//
// Deprecated: use ratelimit.ThrottleMax or ratelimit.Throttle, which this
// now delegates to. They use the configured ratelimit store, so with
// ratelimit.SetStore(redis store) the limit is shared by every instance.
func RateLimiter(requestsPerMinute int) context.HandlerFunc {
	return ratelimit.ThrottleMax(requestsPerMinute, time.Minute)
}

// BodyReadTimeout changes the request body read deadline for the routes it
// is attached to, for example a longer one for large uploads:
//
//	r.POST("/videos", middleware.BodyReadTimeout(30*time.Minute), upload)
//
// The deadline counts from when this middleware runs. d <= 0 removes it.
func BodyReadTimeout(d time.Duration) context.HandlerFunc {
	return func(c *context.Context) error {
		ctl := http.NewResponseController(c.Response.ResponseWriter)
		deadline := time.Time{}
		if d > 0 {
			deadline = time.Now().Add(d)
		}
		if ctl.SetReadDeadline(deadline) == nil && d > 0 && c.Request.Body != nil && c.Request.Body != http.NoBody {
			c.Request.Body = &clearOnEOF{ReadCloser: c.Request.Body, ctl: ctl}
		}
		return c.Next()
	}
}

// clearOnEOF removes the read deadline once the body is consumed (see
// golaravel's engine for why it must not fire during the handler).
type clearOnEOF struct {
	io.ReadCloser
	ctl  *http.ResponseController
	done bool
}

func (b *clearOnEOF) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF && !b.done {
		b.done = true
		_ = b.ctl.SetReadDeadline(time.Time{})
	}
	return n, err
}
