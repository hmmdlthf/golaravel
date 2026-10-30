package telemetry

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/golaravel/golaravel/pkg/context"
)

// MiddlewareOptions configures telemetry middlewares.
type MiddlewareOptions struct {
	RecordRequestSizes  bool
	RecordResponseSizes bool
	NormalizePaths      bool
}

// MetricsMiddleware tracks incoming HTTP requests, latencies, and active concurrency for Prometheus.
func MetricsMiddleware(reg *Registry, opts ...MiddlewareOptions) context.HandlerFunc {
	if reg == nil {
		reg = DefaultRegistry()
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
