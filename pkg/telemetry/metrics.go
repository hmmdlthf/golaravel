package telemetry

import (
	"bytes"
	"fmt"
	"math"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// MetricType denotes the Prometheus metric type.
type MetricType string

const (
	MetricTypeCounter   MetricType = "counter"
	MetricTypeGauge     MetricType = "gauge"
	MetricTypeHistogram MetricType = "histogram"
)

// DefaultHistogramBuckets provides standard HTTP and RPC latency bucket thresholds in seconds.
var DefaultHistogramBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

// Registry manages and aggregates all application metrics.
type Registry struct {
	mu         sync.RWMutex
	counters   map[string]*Counter
	gauges     map[string]*Gauge
	histograms map[string]*Histogram
	collectors []func()
}

// NewRegistry creates a new metrics registry with Go runtime collectors initialized.
func NewRegistry() *Registry {
	r := &Registry{
		counters:   make(map[string]*Counter),
		gauges:     make(map[string]*Gauge),
		histograms: make(map[string]*Histogram),
	}
	r.registerRuntimeMetrics()
	return r
}

func (r *Registry) registerRuntimeMetrics() {
	goroutinesGauge := r.NewGauge("go_goroutines", "Number of goroutines that currently exist.")
	threadsGauge := r.NewGauge("go_threads", "Number of OS threads created.")
	allocBytesGauge := r.NewGauge("go_memstats_alloc_bytes", "Number of bytes allocated and still in use.")
	heapAllocGauge := r.NewGauge("go_memstats_heap_alloc_bytes", "Number of heap bytes allocated and still in use.")
	heapInuseGauge := r.NewGauge("go_memstats_heap_inuse_bytes", "Number of heap bytes in use.")
	stackInuseGauge := r.NewGauge("go_memstats_stack_inuse_bytes", "Number of bytes in use by the stack allocator.")
	sysBytesGauge := r.NewGauge("go_memstats_sys_bytes", "Number of bytes obtained from system.")
	gcDurationCounter := r.NewCounter("go_gc_duration_seconds_total", "Total GC pause duration in seconds.")

	r.collectors = append(r.collectors, func() {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)

		goroutinesGauge.Set(float64(runtime.NumGoroutine()))
		threadsGauge.Set(float64(runtime.GOMAXPROCS(0)))
		allocBytesGauge.Set(float64(m.Alloc))
		heapAllocGauge.Set(float64(m.HeapAlloc))
		heapInuseGauge.Set(float64(m.HeapInuse))
		stackInuseGauge.Set(float64(m.StackInuse))
		sysBytesGauge.Set(float64(m.Sys))
		gcDurationCounter.Set(float64(m.PauseTotalNs) / 1e9)
	})
}

// NewCounter registers and returns a new Counter.
func (r *Registry) NewCounter(name, help string, labelKeys ...string) *Counter {
	r.mu.Lock()
	defer r.mu.Unlock()

	if c, ok := r.counters[name]; ok {
		return c
	}

	c := &Counter{
		name:      name,
		help:      help,
		labelKeys: labelKeys,
		values:    make(map[string]*atomicValue),
	}
	r.counters[name] = c
	return c
}

// NewGauge registers and returns a new Gauge.
func (r *Registry) NewGauge(name, help string, labelKeys ...string) *Gauge {
	r.mu.Lock()
	defer r.mu.Unlock()

	if g, ok := r.gauges[name]; ok {
		return g
	}

	g := &Gauge{
		name:      name,
		help:      help,
		labelKeys: labelKeys,
		values:    make(map[string]*atomicValue),
	}
	r.gauges[name] = g
	return g
}

// NewHistogram registers and returns a new Histogram.
func (r *Registry) NewHistogram(name, help string, buckets []float64, labelKeys ...string) *Histogram {
	r.mu.Lock()
	defer r.mu.Unlock()

	if h, ok := r.histograms[name]; ok {
		return h
	}

	if len(buckets) == 0 {
		buckets = DefaultHistogramBuckets
	}
	// Sort buckets ascending
	sortedBuckets := make([]float64, len(buckets))
	copy(sortedBuckets, buckets)
	sort.Float64s(sortedBuckets)

	h := &Histogram{
		name:      name,
		help:      help,
		buckets:   sortedBuckets,
		labelKeys: labelKeys,
		series:    make(map[string]*histogramSeries),
	}
	r.histograms[name] = h
	return h
}

// RenderPrometheus formats all metrics into Prometheus Text Exposition Format (0.0.4).
func (r *Registry) RenderPrometheus() string {
	r.mu.RLock()
	for _, fn := range r.collectors {
		fn()
	}
	r.mu.RUnlock()

	var buf bytes.Buffer

	r.mu.RLock()
	defer r.mu.RUnlock()

	// 1. Counters
	var counterNames []string
	for k := range r.counters {
		counterNames = append(counterNames, k)
	}
	sort.Strings(counterNames)

	for _, name := range counterNames {
		c := r.counters[name]
		buf.WriteString(fmt.Sprintf("# HELP %s %s\n", c.name, c.help))
		buf.WriteString(fmt.Sprintf("# TYPE %s %s\n", c.name, MetricTypeCounter))
		c.mu.RLock()
		for labels, val := range c.values {
			valFloat := math.Float64frombits(atomic.LoadUint64(&val.bits))
			if labels == "" {
				buf.WriteString(fmt.Sprintf("%s %g\n", c.name, valFloat))
			} else {
				buf.WriteString(fmt.Sprintf("%s{%s} %g\n", c.name, labels, valFloat))
			}
		}
		c.mu.RUnlock()
	}

	// 2. Gauges
	var gaugeNames []string
	for k := range r.gauges {
		gaugeNames = append(gaugeNames, k)
	}
	sort.Strings(gaugeNames)

	for _, name := range gaugeNames {
		g := r.gauges[name]
		buf.WriteString(fmt.Sprintf("# HELP %s %s\n", g.name, g.help))
		buf.WriteString(fmt.Sprintf("# TYPE %s %s\n", g.name, MetricTypeGauge))
		g.mu.RLock()
		for labels, val := range g.values {
			valFloat := math.Float64frombits(atomic.LoadUint64(&val.bits))
			if labels == "" {
				buf.WriteString(fmt.Sprintf("%s %g\n", g.name, valFloat))
			} else {
				buf.WriteString(fmt.Sprintf("%s{%s} %g\n", g.name, labels, valFloat))
			}
		}
		g.mu.RUnlock()
	}

	// 3. Histograms
	var histNames []string
	for k := range r.histograms {
		histNames = append(histNames, k)
	}
	sort.Strings(histNames)

	for _, name := range histNames {
		h := r.histograms[name]
		buf.WriteString(fmt.Sprintf("# HELP %s %s\n", h.name, h.help))
		buf.WriteString(fmt.Sprintf("# TYPE %s %s\n", h.name, MetricTypeHistogram))
		h.mu.RLock()
		for labels, s := range h.series {
			s.mu.RLock()
			count := s.count
			sum := s.sum

			for _, b := range h.buckets {
				bCount := s.bucketCounts[b]
				if labels == "" {
					buf.WriteString(fmt.Sprintf("%s_bucket{le=\"%g\"} %d\n", h.name, b, bCount))
				} else {
					buf.WriteString(fmt.Sprintf("%s_bucket{%s,le=\"%g\"} %d\n", h.name, labels, b, bCount))
				}
			}
			// +Inf bucket
			if labels == "" {
				buf.WriteString(fmt.Sprintf("%s_bucket{le=\"+Inf\"} %d\n", h.name, count))
				buf.WriteString(fmt.Sprintf("%s_sum %g\n", h.name, sum))
				buf.WriteString(fmt.Sprintf("%s_count %d\n", h.name, count))
			} else {
				buf.WriteString(fmt.Sprintf("%s_bucket{%s,le=\"+Inf\"} %d\n", h.name, labels, count))
				buf.WriteString(fmt.Sprintf("%s_sum{%s} %g\n", h.name, labels, sum))
				buf.WriteString(fmt.Sprintf("%s_count{%s} %d\n", h.name, labels, count))
			}
			s.mu.RUnlock()
		}
		h.mu.RUnlock()
	}

	return buf.String()
}

// -------------------------------------------------------------
// Counter
// -------------------------------------------------------------

// Counter represents a monotonically increasing cumulative metric.
type Counter struct {
	name      string
	help      string
	labelKeys []string
	mu        sync.RWMutex
	values    map[string]*atomicValue
}

func (c *Counter) getOrCreate(labels map[string]string) *atomicValue {
	key := formatLabels(labels)
	c.mu.RLock()
	v, ok := c.values[key]
	c.mu.RUnlock()
	if ok {
		return v
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok = c.values[key]; ok {
		return v
	}
	v = &atomicValue{}
	c.values[key] = v
	return v
}

// Inc increments the counter by 1.
func (c *Counter) Inc(labels ...map[string]string) {
	c.Add(1, labels...)
}

// Add adds the given delta to the counter.
func (c *Counter) Add(delta float64, labels ...map[string]string) {
	if delta <= 0 {
		return
	}
	var l map[string]string
	if len(labels) > 0 {
		l = labels[0]
	}
	val := c.getOrCreate(l)
	val.add(delta)
}

// Set sets the counter value directly (used by runtime metrics).
func (c *Counter) Set(val float64, labels ...map[string]string) {
	var l map[string]string
	if len(labels) > 0 {
		l = labels[0]
	}
	v := c.getOrCreate(l)
	v.set(val)
}

// -------------------------------------------------------------
// Gauge
// -------------------------------------------------------------

// Gauge represents a numerical value that can arbitrarily go up and down.
type Gauge struct {
	name      string
	help      string
	labelKeys []string
	mu        sync.RWMutex
	values    map[string]*atomicValue
}

func (g *Gauge) getOrCreate(labels map[string]string) *atomicValue {
	key := formatLabels(labels)
	g.mu.RLock()
	v, ok := g.values[key]
	g.mu.RUnlock()
	if ok {
		return v
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if v, ok = g.values[key]; ok {
		return v
	}
	v = &atomicValue{}
	g.values[key] = v
	return v
}

// Set sets the gauge to the given value.
func (g *Gauge) Set(val float64, labels ...map[string]string) {
	var l map[string]string
	if len(labels) > 0 {
		l = labels[0]
	}
	v := g.getOrCreate(l)
	v.set(val)
}

// Inc increments the gauge by 1.
func (g *Gauge) Inc(labels ...map[string]string) {
	g.Add(1, labels...)
}

// Dec decrements the gauge by 1.
func (g *Gauge) Dec(labels ...map[string]string) {
	g.Add(-1, labels...)
}

// Add adds the given delta to the gauge.
func (g *Gauge) Add(delta float64, labels ...map[string]string) {
	var l map[string]string
	if len(labels) > 0 {
		l = labels[0]
	}
	v := g.getOrCreate(l)
	v.add(delta)
}

// Sub subtracts the given delta from the gauge.
func (g *Gauge) Sub(delta float64, labels ...map[string]string) {
	g.Add(-delta, labels...)
}

// -------------------------------------------------------------
// Histogram
// -------------------------------------------------------------

type histogramSeries struct {
	mu           sync.RWMutex
	count        uint64
	sum          float64
	bucketCounts map[float64]uint64
}

// Histogram samples observations and counts them in configurable buckets.
type Histogram struct {
	name      string
	help      string
	buckets   []float64
	labelKeys []string
	mu        sync.RWMutex
	series    map[string]*histogramSeries
}

func (h *Histogram) getOrCreate(labels map[string]string) *histogramSeries {
	key := formatLabels(labels)
	h.mu.RLock()
	s, ok := h.series[key]
	h.mu.RUnlock()
	if ok {
		return s
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if s, ok = h.series[key]; ok {
		return s
	}
	s = &histogramSeries{
		bucketCounts: make(map[float64]uint64),
	}
	h.series[key] = s
	return s
}

// Observe records a sample value in the histogram.
func (h *Histogram) Observe(val float64, labels ...map[string]string) {
	var l map[string]string
	if len(labels) > 0 {
		l = labels[0]
	}
	s := h.getOrCreate(l)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.count++
	s.sum += val

	for _, b := range h.buckets {
		if val <= b {
			s.bucketCounts[b]++
		}
	}
}

// -------------------------------------------------------------
// Helpers
// -------------------------------------------------------------

type atomicValue struct {
	bits uint64
}

func (v *atomicValue) set(val float64) {
	atomic.StoreUint64(&v.bits, math.Float64bits(val))
}

func (v *atomicValue) add(delta float64) {
	for {
		oldBits := atomic.LoadUint64(&v.bits)
		oldVal := math.Float64frombits(oldBits)
		newVal := oldVal + delta
		newBits := math.Float64bits(newVal)
		if atomic.CompareAndSwapUint64(&v.bits, oldBits, newBits) {
			break
		}
	}
}

func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=\"%s\"", k, labels[k]))
	}
	return strings.Join(parts, ",")
}
