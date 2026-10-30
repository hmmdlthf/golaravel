# Prometheus, OpenTelemetry & Health Check Engine (pkg/telemetry) (PRD §9) 📊

GoLaravel delivers a zero-dependency, cloud-native observability and telemetry suite integrating Prometheus text metrics, OpenTelemetry (OTel) W3C distributed tracing, and multi-subsystem health probes directly into the core framework.

---

## 🌟 Key Features

- **Prometheus Metrics Engine**:
  - Full support for Prometheus Text Exposition Format (v0.0.4) on `GET /metrics`.
  - Type-safe core metrics: `Counter`, `Gauge`, and `Histogram` with custom label dimensions.
  - Automatic Go runtime statistics collection: `go_goroutines`, `go_threads`, `go_memstats_alloc_bytes`, `go_memstats_heap_alloc_bytes`, `go_gc_duration_seconds_total`, etc.
  - HTTP request middleware tracking requests total, in-flight concurrency, and request latency distribution.
- **OpenTelemetry (OTel) Distributed Tracing**:
  - Compliant with the W3C Trace Context recommendation (`traceparent` header).
  - High-performance trace & span management (`StartSpan`, `SpanFromContext`).
  - Hierarchical parent-child relationship tracking and error/event recording.
  - Multi-backend exporters: `StdoutExporter`, `MemoryExporter` (for unit testing), and `OTLPHTTPExporter` (for Jaeger, Grafana Tempo, SigNoz, and Datadog).
  - Distributed tracing middleware injecting `traceparent` and `X-Trace-ID` response headers for full client-to-backend request correlation.
- **Kubernetes & Cloud Health Check Probes**:
  - Comprehensive health evaluation report (`GET /health`) returning subsystem pass/degrade/fail states, latencies, and diagnostic details.
  - Kubernetes Liveness Probe (`GET /livez`) for instant container vitality checks.
  - Kubernetes Readiness Probe (`GET /readyz`) returning `200 OK` when healthy/degraded or `503 Service Unavailable` when any critical dependency fails.
  - Built-in probes for memory allocation thresholds, filesystem free space, and custom ping/database connections.
- **Artisan CLI Health Inspector**:
  - `artisan health` displays a terminal dashboard with color status badges, latency benchmarks, and error diagnostics.

---

## 🚀 Mounting Endpoints in One Line

Mount all telemetry endpoints (`/metrics`, `/health`, `/livez`, `/readyz`) directly onto your application router:

```go
package main

import (
    "github.com/golaravel/golaravel"
    "github.com/golaravel/golaravel/pkg/telemetry"
)

func main() {
    app := golaravel.New()

    // 1. Enable Observability Middlewares
    app.Use(telemetry.MetricsMiddleware(nil)) // nil uses DefaultRegistry()
    app.Use(telemetry.TraceMiddleware(nil))   // nil uses DefaultTracer()

    // 2. Mount Telemetry Endpoints
    telemetry.Mount(app)

    // Application business routes
    app.Get("/api/orders", handleOrders)

    app.Run(":8080")
}
```

This immediately provides:
- `GET /metrics`: Prometheus scraper target.
- `GET /health`: JSON health report with all subsystem checks.
- `GET /livez`: K8s liveness probe (`{"status":"alive"}`).
- `GET /readyz`: K8s readiness probe (returns `200` or `503`).

---

## 📈 Prometheus Metrics

### 1. Defining & Updating Metrics

Use the top-level facade or package functions:

```go
import "github.com/golaravel/golaravel/pkg/telemetry"

var (
    // Counter: monotonically increasing
    ordersTotal = telemetry.NewCounter(
        "orders_placed_total",
        "Total number of orders placed.",
        "payment_method", "currency",
    )

    // Gauge: value that goes up and down
    activeWorkers = telemetry.NewGauge(
        "background_active_workers",
        "Current number of background workers processing jobs.",
    )

    // Histogram: latency or size observations in buckets
    checkoutDuration = telemetry.NewHistogram(
        "checkout_duration_seconds",
        "Time spent completing checkout in seconds.",
        []float64{0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0},
        "payment_method",
    )
)

func placeOrder(c *context.Context) error {
    start := time.Now()
    activeWorkers.Inc()
    defer activeWorkers.Dec()

    // Business logic...

    ordersTotal.Inc(map[string]string{
        "payment_method": "stripe",
        "currency":       "USD",
    })

    checkoutDuration.Observe(time.Since(start).Seconds(), map[string]string{
        "payment_method": "stripe",
    })

    return c.JSON(200, map[string]string{"status": "ok"})
}
```

### 2. Sample `/metrics` Prometheus Output

```promql
# HELP http_requests_total Total number of HTTP requests processed.
# TYPE http_requests_total counter
http_requests_total{method="GET",path="/api/orders",status="200"} 42
# HELP http_request_duration_seconds Histogram of HTTP request latency in seconds.
# TYPE http_request_duration_seconds histogram
http_request_duration_seconds_bucket{method="GET",path="/api/orders",status="200",le="0.05"} 38
http_request_duration_seconds_bucket{method="GET",path="/api/orders",status="200",le="0.1"} 41
http_request_duration_seconds_bucket{method="GET",path="/api/orders",status="200",le="+Inf"} 42
http_request_duration_seconds_sum{method="GET",path="/api/orders",status="200"} 0.842
http_request_duration_seconds_count{method="GET",path="/api/orders",status="200"} 42
# HELP go_goroutines Number of goroutines that currently exist.
# TYPE go_goroutines gauge
go_goroutines 14
# HELP go_memstats_alloc_bytes Number of bytes allocated and still in use.
# TYPE go_memstats_alloc_bytes gauge
go_memstats_alloc_bytes 6291456
```

---

## 🛰️ OpenTelemetry (OTel) Distributed Tracing

### 1. Manual Span Instrumentation

```go
func processInvoice(ctx context.Context, invoiceID string) error {
    // Starts a span (child of ctx span if one exists)
    spanCtx, span := telemetry.StartSpan(ctx, "process_invoice")
    defer span.End()

    span.SetAttribute("invoice.id", invoiceID)
    span.SetAttribute("tenant.id", "tenant_42")

    // Sub-operation with its own child span
    if err := chargePayment(spanCtx, invoiceID); err != nil {
        span.RecordError(err) // Marks span status as ERROR and logs event
        return err
    }

    span.AddEvent("invoice_processed_successfully")
    span.SetStatus(telemetry.StatusOk)
    return nil
}
```

### 2. W3C Context Header Propagation

When `TraceMiddleware` is active:
1. Incoming HTTP requests with `traceparent` headers are automatically extracted and set as the parent trace context.
2. The server generates a unique `X-Trace-ID` and response `traceparent` header:
   ```http
   HTTP/1.1 200 OK
   Content-Type: application/json
   traceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01
   X-Trace-ID: 4bf92f3577b34da6a3ce929d0e0e4736
   ```
3. Frontend clients (Flutter, Vue, React Inertia) can log `X-Trace-ID` on failure to correlate frontend logs with server traces in Grafana Tempo, Jaeger, or Datadog.

### 3. Shipping Spans to OpenTelemetry Collectors

Export spans to an OTLP/HTTP collector (Jaeger, Signoz, Tempo):

```go
exporter := telemetry.NewOTLPHTTPExporter(
    "http://localhost:4318/v1/traces",
    map[string]string{
        "Authorization": "Bearer secret-token",
    },
)
telemetry.AddSpanExporter(exporter)
```

---

## 🩺 Subsystem Health Checks

### 1. Registering Custom Probes

Register database pings, Redis cache checks, and filesystem probes:

```go
// 1. Database Ping
telemetry.RegisterPingCheck("database", func(ctx context.Context) error {
    return db.PingContext(ctx)
})

// 2. Redis Cache Ping
telemetry.RegisterPingCheck("redis", func(ctx context.Context) error {
    return redisClient.Ping(ctx)
})

// 3. Memory Allocation Limit (e.g. 512 MB threshold)
telemetry.RegisterHealth(telemetry.MemoryCheck("memory", 512*1024*1024))

// 4. Free Disk Space Limit (e.g. 1 GB minimum free)
telemetry.RegisterHealth(telemetry.DiskCheck("disk_storage", "/var/data", 1024*1024*1024))

// 5. Custom Business Logic Check
telemetry.DefaultHealth().RegisterFunc("payment_gateway", func(ctx context.Context) (telemetry.HealthStatus, map[string]any, error) {
    status, latency, err := pingPaymentGateway(ctx)
    if err != nil {
        return telemetry.StatusUnhealthy, nil, err
    }
    if latency > 1500*time.Millisecond {
        return telemetry.StatusDegraded, map[string]any{"latency_ms": latency.Milliseconds()}, nil
    }
    return telemetry.StatusHealthy, map[string]any{"region": "us-east"}, nil
})
```

### 2. Sample `/health` JSON Response

```json
{
  "status": "healthy",
  "uptime": "3h12m",
  "timestamp": "2026-10-30T22:00:00Z",
  "checks": {
    "database": {
      "name": "database",
      "status": "healthy",
      "latency_ms": 1.25,
      "details": { "connected": true },
      "checked_at": "2026-10-30T22:00:00Z"
    },
    "memory": {
      "name": "memory",
      "status": "healthy",
      "latency_ms": 0.02,
      "details": { "alloc_mb": 12, "max_alloc_bytes": 536870912 },
      "checked_at": "2026-10-30T22:00:00Z"
    },
    "disk_storage": {
      "name": "disk_storage",
      "status": "healthy",
      "latency_ms": 0.08,
      "details": { "free_mb": 14200, "min_free_bytes": 1073741824 },
      "checked_at": "2026-10-30T22:00:00Z"
    }
  }
}
```

---

## 💻 Artisan CLI Health Check

Inspect the runtime status of all subsystems directly from your terminal:

```bash
artisan health
```

*Output:*
```
GoLaravel System Health Status: [HEALTHY] (Uptime: 2h45m)

  SUBSYSTEM   STATUS   LATENCY   DETAILS
  ---------   ------   -------   -------
  database    PASS     1.20ms    connected=true
  disk        PASS     0.05ms    free_mb=128450, total_bytes=209129086976
  memory      PASS     0.02ms    alloc_mb=14, max_alloc_bytes=536870912
  redis       PASS     0.45ms    connected=true
```
