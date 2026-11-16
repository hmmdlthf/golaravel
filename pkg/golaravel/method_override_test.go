package golaravel

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gcontext "github.com/golaravel/golaravel/pkg/context"
)

func TestMethodOverride(t *testing.T) {
	app := New()
	app.PUT("/posts/1", func(c *gcontext.Context) error { return c.String(200, "put title=%s", c.FormValue("title")) })
	app.DELETE("/posts/1", func(c *gcontext.Context) error { return c.String(200, "deleted") })
	app.POST("/posts/1", func(c *gcontext.Context) error { return c.String(200, "post") })

	do := func(body, header string) string {
		req := httptest.NewRequest("POST", "/posts/1", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if header != "" {
			req.Header.Set("X-HTTP-Method-Override", header)
		}
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec.Body.String()
	}
	if got := do("_method=PUT&title=Hello", ""); got != "put title=Hello" {
		t.Fatalf("_method=PUT: %q", got)
	}
	if got := do("_method=delete", ""); got != "deleted" {
		t.Fatalf("_method=delete: %q", got)
	}
	if got := do("", "DELETE"); got != "deleted" {
		t.Fatalf("header override: %q", got)
	}
	// Only PUT / PATCH / DELETE can be spoofed.
	if got := do("_method=GET", ""); got != "post" {
		t.Fatalf("_method=GET must be ignored: %q", got)
	}
	// GET requests are never overridden.
	req := httptest.NewRequest("GET", "/posts/1?_method=DELETE", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET with _method must not reach DELETE: %d %s", rec.Code, rec.Body.String())
	}

	off := New().MethodOverride(false)
	off.POST("/x", func(c *gcontext.Context) error { return c.String(200, "post") })
	off.PUT("/x", func(c *gcontext.Context) error { return c.String(200, "put") })
	req = httptest.NewRequest("POST", "/x", strings.NewReader("_method=PUT"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	off.ServeHTTP(rec, req)
	if rec.Body.String() != "post" {
		t.Fatalf("disabled override: %q", rec.Body.String())
	}
}
