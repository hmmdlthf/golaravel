package telemetry

import (
	"net/http"
	"strings"

	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/router"
)

// MountOptions configures the mounted telemetry endpoints.
type MountOptions struct {
	MetricsPath   string // default: "/metrics"
	HealthPath    string // default: "/health"
	LivezPath     string // default: "/livez"
	ReadyzPath    string // default: "/readyz"
	Registry      *Registry
	HealthChecker *HealthRegistry
	// Authorize, when set, guards the metrics endpoint and the detailed
	// health report (and the report attached to a failing /readyz). The
	// liveness and readiness status stay public for load balancers and
	// orchestrators. Without it every endpoint is public, so mount them on
	// an internal listener or behind your own middleware.
	Authorize func(c *context.Context) bool
}

// Mount mounts the standard Prometheus and Health Check endpoints onto the router.
func Mount(r router.RouteRegistrar, opts ...MountOptions) {
	cfg := MountOptions{
		MetricsPath: "/metrics",
		HealthPath:  "/health",
		LivezPath:   "/livez",
		ReadyzPath:  "/readyz",
	}
	if len(opts) > 0 {
		o := opts[0]
		if o.MetricsPath != "" {
			cfg.MetricsPath = o.MetricsPath
		}
		if o.HealthPath != "" {
			cfg.HealthPath = o.HealthPath
		}
		if o.LivezPath != "" {
			cfg.LivezPath = o.LivezPath
		}
		if o.ReadyzPath != "" {
			cfg.ReadyzPath = o.ReadyzPath
		}
		cfg.Registry = o.Registry
		cfg.HealthChecker = o.HealthChecker
		cfg.Authorize = o.Authorize
	}

	reg := cfg.Registry
	if reg == nil {
		reg = DefaultRegistry()
	}

	health := cfg.HealthChecker
	if health == nil {
		health = DefaultHealth()
	}

	allowed := func(c *context.Context) bool { return cfg.Authorize == nil || cfg.Authorize(c) }
	forbidden := func(c *context.Context) error {
		return c.JSON(http.StatusForbidden, map[string]string{"error": "Forbidden"})
	}

	// 1. Prometheus Metrics Endpoint
	r.Get(cfg.MetricsPath, func(c *context.Context) error {
		if !allowed(c) {
			return forbidden(c)
		}
		metricsText := reg.RenderPrometheus()
		c.SetHeader("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		return c.String(http.StatusOK, "%s", metricsText)
	})

	// 2. Full Health Report Endpoint
	r.Get(cfg.HealthPath, func(c *context.Context) error {
		if !allowed(c) {
			return forbidden(c)
		}
		report := health.Run(c.Request.Context())
		status := http.StatusOK
		if report.Status == StatusUnhealthy {
			status = http.StatusServiceUnavailable
		}
		return c.JSON(status, report)
	})

	// 3. Kubernetes Liveness Probe (/livez)
	r.Get(cfg.LivezPath, func(c *context.Context) error {
		return c.JSON(http.StatusOK, map[string]string{
			"status": "alive",
		})
	})

	// 4. Kubernetes Readiness Probe (/readyz)
	r.Get(cfg.ReadyzPath, func(c *context.Context) error {
		report := health.Run(c.Request.Context())
		if report.Status == StatusUnhealthy {
			body := map[string]any{
				"status": "not_ready",
				"reason": "one or more critical subsystems unhealthy",
			}
			if allowed(c) {
				body["report"] = report
			}
			return c.JSON(http.StatusServiceUnavailable, body)
		}
		return c.JSON(http.StatusOK, map[string]string{
			"status": "ready",
		})
	})
}

// CleanPath normalizes endpoint URLs.
func CleanPath(prefix, path string) string {
	combined := strings.TrimRight(prefix, "/") + "/" + strings.TrimLeft(path, "/")
	return strings.TrimRight(combined, "/")
}
