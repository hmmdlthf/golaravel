package features_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golaravel/golaravel"
	gcontext "github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/features"
	"github.com/golaravel/golaravel/pkg/privacy"
)

func TestPublicEndpoint(t *testing.T) {
	d := newDefs()
	m := newManager(t, openDB(t), features.Options{}, d.recurring, d.taxMode, d.theme)
	app := golaravel.New()
	tenantMw := privacy.TenantMiddlewareWithOptions(privacy.TenantMiddlewareOptions{
		Required:  false,
		Authorize: func(*gcontext.Context, string) bool { return true },
	}, privacy.HeaderResolver())
	app.GET("/features", tenantMw, m.Handler())

	get := func(tenantID, etag string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/features", nil)
		if tenantID != "" {
			req.Header.Set("X-Tenant-ID", tenantID)
		}
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		app.ServeHTTP(w, req)
		return w
	}

	w := get("acme", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var body features.PublicResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	// Only Public features: recurring and theme, never tax.mode.
	if len(body.Features) != 2 || body.Features["invoices.recurring"] != false {
		t.Fatalf("features = %v", body.Features)
	}
	etag := w.Header().Get("ETag")
	if etag == "" || w.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("headers = %v", w.Header())
	}

	// Unchanged: 304 with no body, also for a weak validator.
	for _, tag := range []string{etag, "W/" + etag, `"other", ` + etag} {
		if w := get("acme", tag); w.Code != http.StatusNotModified || w.Body.Len() != 0 {
			t.Fatalf("If-None-Match %s: %d %q", tag, w.Code, w.Body)
		}
	}
	// A change gives a new ETag and the new value.
	if err := d.recurring.Set(tenantCtx("acme"), true); err != nil {
		t.Fatal(err)
	}
	w = get("acme", etag)
	if w.Code != http.StatusOK || w.Header().Get("ETag") == etag {
		t.Fatalf("after change: %d etag %s", w.Code, w.Header().Get("ETag"))
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Features["invoices.recurring"] != true {
		t.Fatalf("after change = %v", body.Features)
	}
	// Other tenants are unaffected; no tenant fails closed.
	if w := get("globex", w.Header().Get("ETag")); w.Code != http.StatusOK {
		t.Fatalf("globex reused acme's ETag: %d", w.Code)
	}
	if w := get("", ""); w.Code != http.StatusForbidden {
		t.Fatalf("no tenant: %d", w.Code)
	}
}
