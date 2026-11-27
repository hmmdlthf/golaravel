package features

import (
	"errors"
	"log/slog"
	"net/http"
	"slices"

	gcontext "github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/privacy"
)

// Middleware installs the request memo (WithMemo), so a request loads its
// tenant's values once. Put it after privacy.TenantMiddleware. Require and
// RequireValue install it too.
func Middleware() gcontext.HandlerFunc {
	return func(c *gcontext.Context) error {
		withMemo(c)
		return c.Next()
	}
}

func withMemo(c *gcontext.Context) {
	ctx := c.Context()
	if memoFrom(ctx) == nil {
		c.Request = c.Request.WithContext(WithMemo(ctx))
	}
}

// RequireOption configures Require and RequireValue.
type RequireOption func(*requireConfig)

type requireConfig struct {
	status   int
	onDenied gcontext.HandlerFunc
}

// DeniedStatus sets the status sent when the feature is off. The default
// is 404, so a disabled feature looks like it does not exist; use 403 to
// say it exists but is not available.
func DeniedStatus(code int) RequireOption {
	return func(c *requireConfig) { c.status = code }
}

// OnDenied replaces the response sent when the feature is off, for
// example to redirect to an upgrade page.
func OnDenied(h gcontext.HandlerFunc) RequireOption {
	return func(c *requireConfig) { c.onDenied = h }
}

// Require lets a request through only when the bool feature f is on for
// its tenant. It fails closed: a request with no tenant gets 403, and a
// lookup error gets 500 (logged), never the feature.
func Require(f *Feature[bool], opts ...RequireOption) gcontext.HandlerFunc {
	return RequireValue(f, []bool{true}, opts...)
}

// RequireValue lets a request through only when f's value for its tenant
// is one of allowed:
//
//	r.POST("/invoices/:id/tax", h, features.RequireValue(TaxMode, []string{"exclusive", "inclusive"}))
func RequireValue[T comparable](f *Feature[T], allowed []T, opts ...RequireOption) gcontext.HandlerFunc {
	cfg := requireConfig{status: http.StatusNotFound}
	for _, o := range opts {
		o(&cfg)
	}
	return func(c *gcontext.Context) error {
		withMemo(c)
		v, err := f.Get(c.Context())
		switch {
		case errors.Is(err, privacy.ErrTenantMissing):
			c.AbortWithStatus(http.StatusForbidden)
			return c.JSON(http.StatusForbidden, map[string]string{"error": "Tenant Required"})
		case err != nil:
			slog.ErrorContext(c.Context(), "features: Require lookup failed", "key", f.Key(), "error", err)
			c.AbortWithStatus(http.StatusInternalServerError)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Internal Server Error"})
		case !slices.Contains(allowed, v):
			c.Abort()
			if cfg.onDenied != nil {
				return cfg.onDenied(c)
			}
			c.Response.WriteHeader(cfg.status)
			return c.JSON(cfg.status, map[string]string{"error": http.StatusText(cfg.status)})
		}
		return c.Next()
	}
}

// InertiaProp returns the tenant's Public features as a key → value map,
// for an Inertia shared prop:
//
//	inertiaEngine.ShareFunc("features", mgr.InertiaProp())
//
// On error (no tenant, database down) it shares an empty map, so pages
// treat every feature as unknown rather than failing to render.
func (m *Manager) InertiaProp() func(c *gcontext.Context) any {
	return func(c *gcontext.Context) any {
		withMemo(c)
		pub, err := m.Public(c.Context())
		if err != nil {
			if !errors.Is(err, privacy.ErrTenantMissing) {
				m.log.ErrorContext(c.Context(), "features: Inertia prop failed", "error", err)
			}
			return map[string]any{}
		}
		return pub
	}
}
