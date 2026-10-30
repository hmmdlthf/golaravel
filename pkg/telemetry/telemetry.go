package telemetry

import (
	"context"
)

var (
	globalRegistry = NewRegistry()
	globalTracer   = NewTracer("golaravel")
	globalHealth   = NewHealthRegistry()
)

// DefaultRegistry returns the global default Prometheus metrics Registry.
func DefaultRegistry() *Registry {
	return globalRegistry
}

// DefaultTracer returns the global default OpenTelemetry Tracer.
func DefaultTracer() *Tracer {
	return globalTracer
}

// DefaultHealth returns the global default Health Check Registry.
func DefaultHealth() *HealthRegistry {
	return globalHealth
}

// Global Metrics Shortcuts

// NewCounter creates and registers a Counter on the default registry.
func NewCounter(name, help string, labels ...string) *Counter {
	return globalRegistry.NewCounter(name, help, labels...)
}

// NewGauge creates and registers a Gauge on the default registry.
func NewGauge(name, help string, labels ...string) *Gauge {
	return globalRegistry.NewGauge(name, help, labels...)
}

// NewHistogram creates and registers a Histogram on the default registry.
func NewHistogram(name, help string, buckets []float64, labels ...string) *Histogram {
	return globalRegistry.NewHistogram(name, help, buckets, labels...)
}

// Global Tracing Shortcuts

// StartSpan starts a new OpenTelemetry span on the default tracer.
func StartSpan(ctx context.Context, name string) (context.Context, *Span) {
	return globalTracer.Start(ctx, name)
}

// AddSpanExporter registers a new exporter on the default tracer.
func AddSpanExporter(exporter SpanExporter) {
	globalTracer.AddExporter(exporter)
}

// Global Health Check Shortcuts

// RegisterHealth registers a subsystem health check on the default health registry.
func RegisterHealth(check Check) {
	globalHealth.Register(check)
}

// RegisterPingCheck registers a simple ping/connectivity check on the default health registry.
func RegisterPingCheck(name string, fn func(ctx context.Context) error) {
	globalHealth.RegisterPing(name, fn)
}

// RunHealthChecks executes all registered health checks.
func RunHealthChecks(ctx context.Context) HealthReport {
	return globalHealth.Run(ctx)
}
