package telemetry

import (
	"bytes"
	stdContext "context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/goccy/go-json"
	gcontext "github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/golaravel"
)

func TestMetrics_CounterGaugeHistogram(t *testing.T) {
	reg := NewRegistry()

	// 1. Counter
	cnt := reg.NewCounter("test_requests_total", "Total requests", "method", "status")
	cnt.Inc(map[string]string{"method": "GET", "status": "200"})
	cnt.Add(4, map[string]string{"method": "GET", "status": "200"})
	cnt.Inc(map[string]string{"method": "POST", "status": "201"})

	// 2. Gauge
	gauge := reg.NewGauge("test_active_users", "Active users", "region")
	gauge.Set(100, map[string]string{"region": "us-east"})
	gauge.Inc(map[string]string{"region": "us-east"})
	gauge.Dec(map[string]string{"region": "us-east"})
	gauge.Add(5, map[string]string{"region": "us-east"})
	gauge.Sub(2, map[string]string{"region": "us-east"}) // 103

	// 3. Histogram
	hist := reg.NewHistogram("test_duration_seconds", "Duration", []float64{0.1, 0.5, 1.0}, "service")
	hist.Observe(0.05, map[string]string{"service": "api"}) // bucket 0.1
	hist.Observe(0.3, map[string]string{"service": "api"})  // bucket 0.5
	hist.Observe(1.5, map[string]string{"service": "api"})  // +Inf

	// 4. Render Prometheus Text Format
	out := reg.RenderPrometheus()

	// Verify Header and Type lines
	if !strings.Contains(out, "# HELP test_requests_total Total requests") {
		t.Fatalf("missing HELP line for counter in output:\n%s", out)
	}
	if !strings.Contains(out, "# TYPE test_requests_total counter") {
		t.Fatalf("missing TYPE line for counter in output:\n%s", out)
	}
	if !strings.Contains(out, `test_requests_total{method="GET",status="200"} 5`) {
		t.Fatalf("missing counter value 5 in output:\n%s", out)
	}

	// Verify Gauge
	if !strings.Contains(out, "# TYPE test_active_users gauge") {
		t.Fatalf("missing TYPE line for gauge in output:\n%s", out)
	}
	if !strings.Contains(out, `test_active_users{region="us-east"} 103`) {
		t.Fatalf("missing gauge value 103 in output:\n%s", out)
	}

	// Verify Histogram buckets
	if !strings.Contains(out, `test_duration_seconds_bucket{service="api",le="0.1"} 1`) {
		t.Fatalf("missing bucket 0.1 count in output:\n%s", out)
	}
	if !strings.Contains(out, `test_duration_seconds_bucket{service="api",le="0.5"} 2`) {
		t.Fatalf("missing bucket 0.5 count in output:\n%s", out)
	}
	if !strings.Contains(out, `test_duration_seconds_bucket{service="api",le="+Inf"} 3`) {
		t.Fatalf("missing bucket +Inf count in output:\n%s", out)
	}
	if !strings.Contains(out, `test_duration_seconds_count{service="api"} 3`) {
		t.Fatalf("missing histogram count in output:\n%s", out)
	}

	// Verify Runtime Metrics
	if !strings.Contains(out, "go_goroutines") {
		t.Fatalf("missing runtime metric go_goroutines in output:\n%s", out)
	}
	if !strings.Contains(out, "go_memstats_alloc_bytes") {
		t.Fatalf("missing runtime metric go_memstats_alloc_bytes in output:\n%s", out)
	}
}

func TestTracer_HierarchyAndW3C(t *testing.T) {
	memExp := NewMemoryExporter()
	tracer := NewTracer("test_service", memExp)

	// 1. Root Span
	ctx, rootSpan := tracer.Start(stdContext.Background(), "parent_operation")
	rootSpan.SetAttribute("component", "order_service")
	rootSpan.AddEvent("cache_miss", map[string]any{"key": "order_123"})

	if rootSpan.TraceID == "" || len(rootSpan.TraceID) != 32 {
		t.Fatalf("expected 32-char trace ID, got: %s", rootSpan.TraceID)
	}

	// 2. Child Span
	childCtx, childSpan := tracer.Start(ctx, "child_database_query")
	childSpan.SetAttribute("db.statement", "SELECT * FROM orders WHERE id = 123")
	childSpan.RecordError(errors.New("connection timeout"))

	if childSpan.TraceID != rootSpan.TraceID {
		t.Fatalf("child span must inherit root trace ID, got %s vs %s", childSpan.TraceID, rootSpan.TraceID)
	}
	if childSpan.ParentSpanID != rootSpan.SpanID {
		t.Fatalf("child ParentSpanID must equal parent SpanID, got %s vs %s", childSpan.ParentSpanID, rootSpan.SpanID)
	}

	childSpan.End()
	rootSpan.End()

	// Verify Exported Spans
	spans := memExp.Spans()
	if len(spans) != 2 {
		t.Fatalf("expected 2 exported spans, got %d", len(spans))
	}
	if spans[0].Name != "child_database_query" || spans[0].Status != StatusError {
		t.Fatalf("child span not recorded properly: %+v", spans[0])
	}
	if spans[1].Name != "parent_operation" {
		t.Fatalf("parent span not recorded properly: %+v", spans[1])
	}

	// 3. W3C Trace Context Parsing
	traceparent := rootSpan.Traceparent()
	tid, pid, sampled, err := ExtractW3C(traceparent)
	if err != nil {
		t.Fatalf("ExtractW3C failed: %v", err)
	}
	if tid != rootSpan.TraceID || pid != rootSpan.SpanID || !sampled {
		t.Fatalf("ExtractW3C mismatch: %s vs %s", tid, rootSpan.TraceID)
	}

	// 4. StartWithParent
	_, externalChild := tracer.StartWithParent(childCtx, "external_call", traceparent)
	if externalChild.TraceID != rootSpan.TraceID || externalChild.ParentSpanID != rootSpan.SpanID {
		t.Fatalf("external child did not inherit W3C traceparent correctly")
	}
	externalChild.End()

	// 5. Stdout exporter
	var buf bytes.Buffer
	stdoutExp := NewStdoutExporter(&buf)
	_ = stdoutExp.ExportSpans([]*Span{rootSpan})
	if !strings.Contains(buf.String(), `"trace_id"`) {
		t.Fatalf("stdout exporter did not output valid JSON: %s", buf.String())
	}
}

func TestHealthRegistry_ChecksAndAggregation(t *testing.T) {
	health := NewHealthRegistry()

	// 1. Healthy Ping check
	health.RegisterPing("postgres", func(ctx stdContext.Context) error {
		return nil
	})

	// 2. Degraded check
	health.RegisterFunc("redis", func(ctx stdContext.Context) (HealthStatus, map[string]any, error) {
		return StatusDegraded, map[string]any{"connected_clients": 500}, nil
	})

	// 3. Built-in Memory Check
	health.Register(MemoryCheck("memory", 1024*1024*1024)) // 1GB threshold

	report := health.Run(stdContext.Background())
	if report.Status != StatusDegraded {
		t.Fatalf("expected overall status degraded, got %s", report.Status)
	}
	if len(report.Checks) != 3 {
		t.Fatalf("expected 3 checks, got %d", len(report.Checks))
	}
	if report.Checks["postgres"].Status != StatusHealthy {
		t.Fatalf("expected postgres healthy, got %s", report.Checks["postgres"].Status)
	}
	if report.Checks["redis"].Status != StatusDegraded {
		t.Fatalf("expected redis degraded, got %s", report.Checks["redis"].Status)
	}

	// 4. Failing Check -> turns overall to Unhealthy
	health.RegisterPing("smtp", func(ctx stdContext.Context) error {
		return errors.New("connection refused: port 25")
	})

	report2 := health.Run(stdContext.Background())
	if report2.Status != StatusUnhealthy {
		t.Fatalf("expected overall status unhealthy after smtp failure, got %s", report2.Status)
	}
	if report2.Checks["smtp"].Error != "connection refused: port 25" {
		t.Fatalf("expected error message in smtp check, got: %s", report2.Checks["smtp"].Error)
	}
}

func TestTelemetry_HTTPPipelineAndMount(t *testing.T) {
	app := golaravel.New()
	reg := NewRegistry()
	memExp := NewMemoryExporter()
	tracer := NewTracer("test_api", memExp)
	health := NewHealthRegistry()

	health.RegisterPing("db", func(ctx stdContext.Context) error {
		return nil
	})

	// Apply Middlewares
	app.Use(MetricsMiddleware(reg))
	app.Use(TraceMiddleware(tracer))

	// Mount Telemetry endpoints
	Mount(app, MountOptions{
		Registry:      reg,
		HealthChecker: health,
	})

	// Application routes
	app.Get("/users", func(c *gcontext.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"user": "alice"})
	})

	app.Get("/crash", func(c *gcontext.Context) error {
		c.AbortWithStatus(http.StatusInternalServerError)
		return errors.New("server error")
	})

	// 1. Request /users
	req := httptest.NewRequest("GET", "/users", nil)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	traceID := w.Header().Get(TraceIDHeader)
	traceparent := w.Header().Get(TraceparentHeader)
	if traceID == "" || traceparent == "" {
		t.Fatalf("expected trace headers in response: trace_id=%s, traceparent=%s", traceID, traceparent)
	}

	// Verify trace span was created
	spans := memExp.Spans()
	if len(spans) == 0 || spans[0].Name != "HTTP GET /users" {
		t.Fatalf("expected exported HTTP span, got %+v", spans)
	}

	// 2. Request /metrics -> Prometheus
	reqMetrics := httptest.NewRequest("GET", "/metrics", nil)
	wMetrics := httptest.NewRecorder()
	app.ServeHTTP(wMetrics, reqMetrics)

	if wMetrics.Code != http.StatusOK {
		t.Fatalf("expected 200 for /metrics, got %d", wMetrics.Code)
	}
	body := wMetrics.Body.String()
	if !strings.Contains(body, `http_requests_total{method="GET",path="/users",status="200"} 1`) {
		t.Fatalf("metrics output missing tracked /users count: %s", body)
	}

	// 3. Request /health -> JSON
	reqHealth := httptest.NewRequest("GET", "/health", nil)
	wHealth := httptest.NewRecorder()
	app.ServeHTTP(wHealth, reqHealth)

	if wHealth.Code != http.StatusOK {
		t.Fatalf("expected 200 for /health, got %d", wHealth.Code)
	}
	var healthResp HealthReport
	if err := json.Unmarshal(wHealth.Body.Bytes(), &healthResp); err != nil {
		t.Fatalf("failed to decode health JSON: %v", err)
	}
	if healthResp.Status != StatusHealthy {
		t.Fatalf("expected status healthy, got %s", healthResp.Status)
	}
	if healthResp.Checks["db"].Status != StatusHealthy {
		t.Fatalf("expected db check healthy")
	}

	// 4. Request /livez
	reqLivez := httptest.NewRequest("GET", "/livez", nil)
	wLivez := httptest.NewRecorder()
	app.ServeHTTP(wLivez, reqLivez)
	if wLivez.Code != http.StatusOK {
		t.Fatalf("expected 200 for /livez, got %d", wLivez.Code)
	}

	// 5. Request /readyz
	reqReadyz := httptest.NewRequest("GET", "/readyz", nil)
	wReadyz := httptest.NewRecorder()
	app.ServeHTTP(wReadyz, reqReadyz)
	if wReadyz.Code != http.StatusOK {
		t.Fatalf("expected 200 for /readyz, got %d", wReadyz.Code)
	}

	// 6. Request /crash -> should record error on span
	memExp.Clear()
	reqCrash := httptest.NewRequest("GET", "/crash", nil)
	wCrash := httptest.NewRecorder()
	app.ServeHTTP(wCrash, reqCrash)

	crashSpans := memExp.Spans()
	if len(crashSpans) == 0 || crashSpans[0].Status != StatusError {
		t.Fatalf("expected error status on crashed span, got: %+v", crashSpans)
	}
}
