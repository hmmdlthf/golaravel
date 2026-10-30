package telemetry

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/goccy/go-json"
)

type contextKey string

const (
	currentSpanKey contextKey = "golaravel_telemetry_span"
	TraceparentHeader        = "traceparent"
	TraceIDHeader            = "X-Trace-ID"
)

// StatusCode denotes the status of a span.
type StatusCode string

const (
	StatusUnset StatusCode = "UNSET"
	StatusOk    StatusCode = "OK"
	StatusError StatusCode = "ERROR"
)

// SpanEvent represents a timestamped event/annotation within a span.
type SpanEvent struct {
	Name       string         `json:"name"`
	Timestamp  time.Time      `json:"timestamp"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// Span represents a single operation within a distributed trace.
type Span struct {
	mu                sync.RWMutex
	TraceID           string         `json:"trace_id"`
	SpanID            string         `json:"span_id"`
	ParentSpanID      string         `json:"parent_span_id,omitempty"`
	Name              string         `json:"name"`
	ServiceName       string         `json:"service_name"`
	StartTime         time.Time      `json:"start_time"`
	EndTime           time.Time      `json:"end_time"`
	Duration          time.Duration  `json:"duration_ms"`
	Attributes        map[string]any `json:"attributes,omitempty"`
	Events            []SpanEvent    `json:"events,omitempty"`
	Status            StatusCode     `json:"status"`
	StatusDescription string         `json:"status_description,omitempty"`
	Sampled           bool           `json:"sampled"`
	tracer            *Tracer
	ended             bool
}

// SetAttribute sets a key-value attribute on the span.
func (s *Span) SetAttribute(key string, val any) *Span {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Attributes == nil {
		s.Attributes = make(map[string]any)
	}
	s.Attributes[key] = val
	return s
}

// SetAttributes sets multiple key-value attributes on the span.
func (s *Span) SetAttributes(attrs map[string]any) *Span {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Attributes == nil {
		s.Attributes = make(map[string]any)
	}
	for k, v := range attrs {
		s.Attributes[k] = v
	}
	return s
}

// SetStatus sets the completion status and optional description of the span.
func (s *Span) SetStatus(code StatusCode, description ...string) *Span {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Status = code
	if len(description) > 0 {
		s.StatusDescription = description[0]
	}
	return s
}

// RecordError records an error event and marks the span status as StatusError.
func (s *Span) RecordError(err error) *Span {
	if err == nil {
		return s
	}
	s.AddEvent("exception", map[string]any{
		"exception.message": err.Error(),
	})
	s.SetStatus(StatusError, err.Error())
	return s
}

// AddEvent appends a timestamped annotation event to the span.
func (s *Span) AddEvent(name string, attrs ...map[string]any) *Span {
	s.mu.Lock()
	defer s.mu.Unlock()
	var a map[string]any
	if len(attrs) > 0 {
		a = attrs[0]
	}
	s.Events = append(s.Events, SpanEvent{
		Name:       name,
		Timestamp:  time.Now(),
		Attributes: a,
	})
	return s
}

// End finishes the span and sends it to the tracer's registered exporters.
func (s *Span) End() {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	s.EndTime = time.Now()
	s.Duration = s.EndTime.Sub(s.StartTime)
	s.mu.Unlock()

	if s.tracer != nil {
		s.tracer.exportSpan(s)
	}
}

// Traceparent returns the W3C Trace Context header value: 00-{trace_id}-{span_id}-{flags}.
func (s *Span) Traceparent() string {
	flags := "01"
	if !s.Sampled {
		flags = "00"
	}
	return fmt.Sprintf("00-%s-%s-%s", s.TraceID, s.SpanID, flags)
}

// -------------------------------------------------------------
// Tracer
// -------------------------------------------------------------

// SpanExporter defines the contract for shipping spans to backends.
type SpanExporter interface {
	ExportSpans(spans []*Span) error
	Shutdown(ctx context.Context) error
}

// Tracer creates and manages spans.
type Tracer struct {
	mu          sync.RWMutex
	serviceName string
	exporters   []SpanExporter
}

// NewTracer creates a new Tracer instance with optional exporters.
func NewTracer(serviceName string, exporters ...SpanExporter) *Tracer {
	if serviceName == "" {
		serviceName = "golaravel"
	}
	return &Tracer{
		serviceName: serviceName,
		exporters:   exporters,
	}
}

// AddExporter registers an additional span exporter.
func (t *Tracer) AddExporter(exp SpanExporter) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.exporters = append(t.exporters, exp)
}

// Start begins a new span. If ctx already contains an active span, the new span becomes its child.
func (t *Tracer) Start(ctx context.Context, name string) (context.Context, *Span) {
	parent := SpanFromContext(ctx)

	traceID := ""
	parentSpanID := ""
	sampled := true

	if parent != nil {
		traceID = parent.TraceID
		parentSpanID = parent.SpanID
		sampled = parent.Sampled
	} else {
		traceID = randomHex(16) // 32 hex chars
	}

	spanID := randomHex(8) // 16 hex chars

	span := &Span{
		TraceID:      traceID,
		SpanID:       spanID,
		ParentSpanID: parentSpanID,
		Name:         name,
		ServiceName:  t.serviceName,
		StartTime:    time.Now(),
		Status:       StatusUnset,
		Sampled:      sampled,
		Attributes:   make(map[string]any),
		tracer:       t,
	}

	newCtx := context.WithValue(ctx, currentSpanKey, span)
	return newCtx, span
}

// StartWithParent begins a span with an explicit external W3C traceparent context.
func (t *Tracer) StartWithParent(ctx context.Context, name, traceparent string) (context.Context, *Span) {
	traceID, parentSpanID, sampled, err := ExtractW3C(traceparent)
	if err != nil || traceID == "" {
		return t.Start(ctx, name)
	}

	spanID := randomHex(8)
	span := &Span{
		TraceID:      traceID,
		SpanID:       spanID,
		ParentSpanID: parentSpanID,
		Name:         name,
		ServiceName:  t.serviceName,
		StartTime:    time.Now(),
		Status:       StatusUnset,
		Sampled:      sampled,
		Attributes:   make(map[string]any),
		tracer:       t,
	}

	newCtx := context.WithValue(ctx, currentSpanKey, span)
	return newCtx, span
}

func (t *Tracer) exportSpan(s *Span) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, exp := range t.exporters {
		_ = exp.ExportSpans([]*Span{s})
	}
}

// SpanFromContext retrieves the active Span from context, or nil if none exists.
func SpanFromContext(ctx context.Context) *Span {
	if ctx == nil {
		return nil
	}
	if val := ctx.Value(currentSpanKey); val != nil {
		if s, ok := val.(*Span); ok {
			return s
		}
	}
	return nil
}

// -------------------------------------------------------------
// W3C Trace Context Parser
// -------------------------------------------------------------

// ExtractW3C parses a standard W3C traceparent header: 00-{trace_id}-{parent_id}-{flags}.
func ExtractW3C(header string) (traceID, parentID string, sampled bool, err error) {
	header = strings.TrimSpace(header)
	parts := strings.Split(header, "-")
	if len(parts) < 4 {
		return "", "", false, errors.New("telemetry: invalid traceparent format")
	}

	// Version must be "00"
	if parts[0] != "00" {
		return "", "", false, errors.New("telemetry: unsupported traceparent version")
	}

	traceID = parts[1]
	if len(traceID) != 32 {
		return "", "", false, errors.New("telemetry: trace_id must be 32 hex chars")
	}

	parentID = parts[2]
	if len(parentID) != 16 {
		return "", "", false, errors.New("telemetry: parent_id must be 16 hex chars")
	}

	flags := parts[3]
	sampled = flags == "01"

	return traceID, parentID, sampled, nil
}

// -------------------------------------------------------------
// Built-in Exporters
// -------------------------------------------------------------

// MemoryExporter buffers finished spans in memory for unit testing and local assertions.
type MemoryExporter struct {
	mu    sync.RWMutex
	spans []*Span
}

// NewMemoryExporter creates a new in-memory span exporter.
func NewMemoryExporter() *MemoryExporter {
	return &MemoryExporter{
		spans: make([]*Span, 0),
	}
}

func (m *MemoryExporter) ExportSpans(spans []*Span) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.spans = append(m.spans, spans...)
	return nil
}

func (m *MemoryExporter) Shutdown(ctx context.Context) error {
	return nil
}

// Spans returns a copy of all exported spans.
func (m *MemoryExporter) Spans() []*Span {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cp := make([]*Span, len(m.spans))
	copy(cp, m.spans)
	return cp
}

// Clear clears the buffered spans.
func (m *MemoryExporter) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.spans = m.spans[:0]
}

// StdoutExporter writes finished spans to stdout or a custom writer.
type StdoutExporter struct {
	out io.Writer
}

// NewStdoutExporter creates a new stdout exporter.
func NewStdoutExporter(out ...io.Writer) *StdoutExporter {
	w := io.Writer(os.Stdout)
	if len(out) > 0 && out[0] != nil {
		w = out[0]
	}
	return &StdoutExporter{out: w}
}

func (e *StdoutExporter) ExportSpans(spans []*Span) error {
	for _, s := range spans {
		data, err := json.Marshal(s)
		if err == nil {
			_, _ = fmt.Fprintln(e.out, string(data))
		}
	}
	return nil
}

func (e *StdoutExporter) Shutdown(ctx context.Context) error {
	return nil
}

// OTLPHTTPExporter exports spans via standard OTLP/HTTP JSON to OpenTelemetry collectors
// (Jaeger, Grafana Tempo, SigNoz, Datadog, etc.).
type OTLPHTTPExporter struct {
	endpoint   string
	headers    map[string]string
	httpClient *http.Client
}

// NewOTLPHTTPExporter creates an exporter targeting an OTLP/HTTP endpoint (e.g. http://localhost:4318/v1/traces).
func NewOTLPHTTPExporter(endpoint string, headers ...map[string]string) *OTLPHTTPExporter {
	if endpoint == "" {
		endpoint = "http://localhost:4318/v1/traces"
	}
	h := make(map[string]string)
	if len(headers) > 0 {
		h = headers[0]
	}
	return &OTLPHTTPExporter{
		endpoint: endpoint,
		headers:  h,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

func (e *OTLPHTTPExporter) ExportSpans(spans []*Span) error {
	data, err := json.Marshal(map[string]any{
		"resourceSpans": []map[string]any{
			{
				"scopeSpans": []map[string]any{
					{"spans": spans},
				},
			},
		},
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", e.endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range e.headers {
		req.Header.Set(k, v)
	}

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (e *OTLPHTTPExporter) Shutdown(ctx context.Context) error {
	return nil
}

func randomHex(byteLen int) string {
	b := make([]byte, byteLen)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
