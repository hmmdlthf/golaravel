package telemetry

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golaravel/golaravel/pkg/context"
)

// MiddlewareOptions configures telemetry middlewares.
type MiddlewareOptions struct {
	RecordRequestSizes  bool
	RecordResponseSizes bool
	// NormalizePaths replaces ID-like path segments (numbers, UUIDs, long
	// hex / base64 tokens) with ":id", so /users/42 and /users/43 share a
	// series.
	NormalizePaths bool
	// MaxPaths caps distinct path labels (default 500). Requests to new
	// paths beyond it are counted under path "other", so random URLs
	// cannot grow the metrics without bound.
	MaxPaths int
}

var idSegment = regexp.MustCompile(`^(\d+|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|[0-9a-fA-F]{16,}|[A-Za-z0-9_-]{24,})$`)

func normalizePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		if s != "" && idSegment.MatchString(s) {
			segs[i] = ":id"
		}
	}
	return strings.Join(segs, "/")
}

// pathLabels hands out at most max distinct path labels.
type pathLabels struct {
	mu   sync.Mutex
	seen map[string]struct{}
	max  int
}

func (l *pathLabels) label(p string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.seen[p]; ok {
		return p
	}
	if len(l.seen) >= l.max {
		return "other"
	}
	l.seen[p] = struct{}{}
	return p
}

// MetricsMiddleware tracks incoming HTTP requests, latencies, and active concurrency for Prometheus.
func MetricsMiddleware(reg *Registry, opts ...MiddlewareOptions) context.HandlerFunc {
	if reg == nil {
		reg = DefaultRegistry()
	}
	var opt MiddlewareOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	paths := &pathLabels{seen: map[string]struct{}{}, max: opt.MaxPaths}
	if paths.max <= 0 {
		paths.max = 500
	}

	reqTotal := reg.NewCounter(
		"http_requests_total",
		"Total number of HTTP requests processed.",
		"method", "path", "status",
	)

	reqActive := reg.NewGauge(
		"http_requests_active",
		"Number of currently active in-flight HTTP requests.",
		"method",
	)

	reqDuration := reg.NewHistogram(
		"http_request_duration_seconds",
		"Histogram of HTTP request latency in seconds.",
		DefaultHistogramBuckets,
		"method", "path", "status",
	)

	return func(c *context.Context) error {
		method := c.Request.Method
		path := c.Request.URL.Path
		if opt.NormalizePaths {
			path = normalizePath(path)
		}
		path = paths.label(path)

		reqActive.Inc(map[string]string{"method": method})
		start := time.Now()

		err := c.Next()

		dur := time.Since(start).Seconds()
		reqActive.Dec(map[string]string{"method": method})

		status := c.Response.Status()
		if status == 0 {
			status = http.StatusOK
		}
		statusStr := strconv.Itoa(status)

		labels := map[string]string{
			"method": method,
			"path":   path,
			"status": statusStr,
		}

		reqTotal.Inc(labels)
		reqDuration.Observe(dur, labels)

		return err
	}
}

// TraceMiddleware instruments the request with an OpenTelemetry-compliant W3C distributed trace span.
func TraceMiddleware(tracer *Tracer) context.HandlerFunc {
	if tracer == nil {
		tracer = DefaultTracer()
	}

	return func(c *context.Context) error {
		incomingTraceparent := c.Header(TraceparentHeader)
		path := c.Request.URL.Path
		spanName := fmt.Sprintf("HTTP %s %s", c.Request.Method, path)

		spanCtx, span := tracer.StartWithParent(c.Request.Context(), spanName, incomingTraceparent)
		defer span.End()

		span.SetAttributes(map[string]any{
			"http.method":      c.Request.Method,
			"http.target":      path,
			"http.host":        c.Request.Host,
			"http.user_agent":  c.Header("User-Agent"),
			"http.remote_addr": c.Request.RemoteAddr,
		})

		// Propagate trace IDs back in response headers for client/frontend correlation
		c.SetHeader(TraceparentHeader, span.Traceparent())
		c.SetHeader(TraceIDHeader, span.TraceID)

		// Set span context on request
		c.Request = c.Request.WithContext(spanCtx)

		err := c.Next()

		status := c.Response.Status()
		if status == 0 {
			status = http.StatusOK
		}
		span.SetAttribute("http.status_code", status)

		if err != nil {
			span.RecordError(err)
		} else if status >= 500 {
			span.SetStatus(StatusError, fmt.Sprintf("HTTP %d", status))
		} else {
			span.SetStatus(StatusOk)
		}

		return err
	}
}
