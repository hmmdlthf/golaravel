package telemetry

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/golaravel"
)

func TestTelemetryAuthorize(t *testing.T) {
	app := golaravel.New()
	Mount(app, MountOptions{
		Registry:      NewRegistry(),
		HealthChecker: NewHealthRegistry(),
		Authorize:     func(c *context.Context) bool { return c.Header("X-Ops-Key") == "k" },
	})
	get := func(path, key string) int {
		req := httptest.NewRequest("GET", path, nil)
		if key != "" {
			req.Header.Set("X-Ops-Key", key)
		}
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec.Code
	}
	for _, p := range []string{"/metrics", "/health"} {
		if code := get(p, ""); code != http.StatusForbidden {
			t.Fatalf("%s without key: %d", p, code)
		}
		if code := get(p, "k"); code != http.StatusOK {
			t.Fatalf("%s with key: %d", p, code)
		}
	}
	for _, p := range []string{"/livez", "/readyz"} {
		if code := get(p, ""); code != http.StatusOK {
			t.Fatalf("%s must stay public: %d", p, code)
		}
	}
}
