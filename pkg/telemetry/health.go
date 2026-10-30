package telemetry

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"time"
)

// HealthStatus denotes the overall health evaluation of a subsystem.
type HealthStatus string

const (
	StatusHealthy   HealthStatus = "healthy"
	StatusDegraded  HealthStatus = "degraded"
	StatusUnhealthy HealthStatus = "unhealthy"
)

// CheckResult represents the outcome of a single subsystem health check.
type CheckResult struct {
	Name      string         `json:"name"`
	Status    HealthStatus   `json:"status"`
	Latency   time.Duration  `json:"-"`
	LatencyMS float64        `json:"latency_ms"`
	Error     string         `json:"error,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
	CheckedAt time.Time      `json:"checked_at"`
}

// Check defines the contract for an individual subsystem health probe.
type Check interface {
	Name() string
	Check(ctx context.Context) CheckResult
}

// CheckFunc is a functional adapter implementing the Check interface.
type CheckFunc struct {
	name string
	fn   func(ctx context.Context) (HealthStatus, map[string]any, error)
}

func NewCheckFunc(name string, fn func(ctx context.Context) (HealthStatus, map[string]any, error)) *CheckFunc {
	return &CheckFunc{name: name, fn: fn}
}

func (c *CheckFunc) Name() string {
	return c.name
}

func (c *CheckFunc) Check(ctx context.Context) CheckResult {
	start := time.Now()
	status, details, err := c.fn(ctx)
	dur := time.Since(start)

	errStr := ""
	if err != nil {
		errStr = err.Error()
	}

	return CheckResult{
		Name:      c.name,
		Status:    status,
		Latency:   dur,
		LatencyMS: float64(dur.Microseconds()) / 1000.0,
		Error:     errStr,
		Details:   details,
		CheckedAt: time.Now(),
	}
}

// HealthRegistry orchestrates registered health check probes.
type HealthRegistry struct {
	mu        sync.RWMutex
	checks    map[string]Check
	startedAt time.Time
}

// NewHealthRegistry creates a new HealthRegistry.
func NewHealthRegistry() *HealthRegistry {
	return &HealthRegistry{
		checks:    make(map[string]Check),
		startedAt: time.Now(),
	}
}

// Register registers a new health check probe.
func (h *HealthRegistry) Register(check Check) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checks[check.Name()] = check
}

// RegisterPing registers a simple ping/connectivity health check.
func (h *HealthRegistry) RegisterPing(name string, fn func(ctx context.Context) error) {
	h.Register(NewCheckFunc(name, func(ctx context.Context) (HealthStatus, map[string]any, error) {
		if err := fn(ctx); err != nil {
			return StatusUnhealthy, nil, err
		}
		return StatusHealthy, map[string]any{"connected": true}, nil
	}))
}

// RegisterFunc registers a functional health check.
func (h *HealthRegistry) RegisterFunc(name string, fn func(ctx context.Context) (HealthStatus, map[string]any, error)) {
	h.Register(NewCheckFunc(name, fn))
}

// HealthReport summarizes all subsystem health check outcomes.
type HealthReport struct {
	Status    HealthStatus           `json:"status"`
	Uptime    string                 `json:"uptime"`
	Timestamp time.Time              `json:"timestamp"`
	Checks    map[string]CheckResult `json:"checks"`
}

// Run executes all registered health checks concurrently and returns the composite report.
func (h *HealthRegistry) Run(ctx context.Context) HealthReport {
	h.mu.RLock()
	checks := make([]Check, 0, len(h.checks))
	for _, c := range h.checks {
		checks = append(checks, c)
	}
	startedAt := h.startedAt
	h.mu.RUnlock()

	results := make(map[string]CheckResult)
	var mu sync.Mutex
	var wg sync.WaitGroup

	overallStatus := StatusHealthy

	for _, c := range checks {
		wg.Add(1)
		go func(chk Check) {
			defer wg.Done()
			res := chk.Check(ctx)

			mu.Lock()
			results[res.Name] = res
			if res.Status == StatusUnhealthy {
				overallStatus = StatusUnhealthy
			} else if res.Status == StatusDegraded && overallStatus != StatusUnhealthy {
				overallStatus = StatusDegraded
			}
			mu.Unlock()
		}(c)
	}

	wg.Wait()

	return HealthReport{
		Status:    overallStatus,
		Uptime:    time.Since(startedAt).Round(time.Second).String(),
		Timestamp: time.Now(),
		Checks:    results,
	}
}

// RunCheck executes a specific named health check.
func (h *HealthRegistry) RunCheck(ctx context.Context, name string) (CheckResult, bool) {
	h.mu.RLock()
	chk, exists := h.checks[name]
	h.mu.RUnlock()

	if !exists {
		return CheckResult{}, false
	}

	return chk.Check(ctx), true
}

// -------------------------------------------------------------
// Built-in Probes
// -------------------------------------------------------------

// MemoryCheck returns a Check that verifies Go heap allocation remains below a threshold.
func MemoryCheck(name string, maxAllocBytes uint64) Check {
	return NewCheckFunc(name, func(ctx context.Context) (HealthStatus, map[string]any, error) {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)

		details := map[string]any{
			"alloc_bytes":     m.Alloc,
			"max_alloc_bytes": maxAllocBytes,
			"alloc_mb":        m.Alloc / 1024 / 1024,
			"sys_mb":          m.Sys / 1024 / 1024,
			"goroutines":      runtime.NumGoroutine(),
		}

		if maxAllocBytes > 0 && m.Alloc > maxAllocBytes {
			return StatusDegraded, details, fmt.Errorf("memory allocation %d bytes exceeded threshold of %d", m.Alloc, maxAllocBytes)
		}

		return StatusHealthy, details, nil
	})
}

// DiskCheck returns a Check that verifies storage filesystem free space.
func DiskCheck(name, path string, minFreeBytes uint64) Check {
	return NewCheckFunc(name, func(ctx context.Context) (HealthStatus, map[string]any, error) {
		var stat syscall.Statfs_t
		if err := syscall.Statfs(path, &stat); err != nil {
			return StatusUnhealthy, nil, fmt.Errorf("failed to statfs path '%s': %w", path, err)
		}

		freeBytes := stat.Bavail * uint64(stat.Bsize)
		totalBytes := stat.Blocks * uint64(stat.Bsize)

		details := map[string]any{
			"path":           path,
			"free_bytes":     freeBytes,
			"total_bytes":    totalBytes,
			"free_mb":        freeBytes / 1024 / 1024,
			"min_free_bytes": minFreeBytes,
		}

		if minFreeBytes > 0 && freeBytes < minFreeBytes {
			return StatusDegraded, details, fmt.Errorf("free disk space %d bytes is below threshold %d", freeBytes, minFreeBytes)
		}

		return StatusHealthy, details, nil
	})
}
