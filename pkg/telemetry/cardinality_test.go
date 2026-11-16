package telemetry

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golaravel/golaravel/pkg/context"
)

func hit(t *testing.T, mw context.HandlerFunc, path string) {
	t.Helper()
	c := context.NewContext()
	c.Reset(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
	c.SetHandlers([]context.HandlerFunc{mw, func(c *context.Context) error { return c.String(200, "ok") }})
	_ = c.Next()
}

func TestMetricsPathLabelsAreBounded(t *testing.T) {
	reg := NewRegistry()
	mw := MetricsMiddleware(reg, MiddlewareOptions{MaxPaths: 5})
	for i := 0; i < 200; i++ {
		hit(t, mw, fmt.Sprintf("/random/%d-%s", i, strings.Repeat("x", i%3)))
	}
	out := reg.RenderPrometheus()
	if n := strings.Count(out, "http_requests_total{"); n > 6 {
		t.Fatalf("%d request series for 200 random paths, want at most 6 (5 + other)", n)
	}
	if !strings.Contains(out, `path="other"`) {
		t.Fatal("paths beyond the cap must be counted as other")
	}
}

func TestNormalizePaths(t *testing.T) {
	for in, want := range map[string]string{
		"/users/42": "/users/:id",
		"/orders/123e4567-e89b-12d3-a456-426614174000/items": "/orders/:id/items",
		"/files/0123456789abcdef0123":                        "/files/:id",
		"/about":                                             "/about",
	} {
		if got := normalizePath(in); got != want {
			t.Errorf("normalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtractW3CRejectsNonHex(t *testing.T) {
	good := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	if _, _, _, err := ExtractW3C(good); err != nil {
		t.Fatalf("valid traceparent: %v", err)
	}
	for _, bad := range []string{
		"00-4BF92F3577B34DA6A3CE929D0E0E4736-00f067aa0ba902b7-01", // uppercase
		"00-zzf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", // not hex
		"00-00000000000000000000000000000000-00f067aa0ba902b7-01", // all zero
		"00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01",
	} {
		if _, _, _, err := ExtractW3C(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
