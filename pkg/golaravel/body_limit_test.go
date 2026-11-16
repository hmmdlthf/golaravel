package golaravel

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gcontext "github.com/golaravel/golaravel/pkg/context"
)

func TestRequestBodiesAreCapped(t *testing.T) {
	app := New().MaxBodyBytes(16)
	app.POST("/echo", func(c *gcontext.Context) error {
		var v map[string]any
		if err := c.BindJSON(&v); err != nil {
			return c.String(http.StatusRequestEntityTooLarge, "too large")
		}
		return c.String(http.StatusOK, "ok")
	})
	do := func(body string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/echo", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		app.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := do(`{"a":1}`); code != http.StatusOK {
		t.Fatalf("small body: %d", code)
	}
	if code := do(`{"a":"` + strings.Repeat("x", 100) + `"}`); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("large body should fail to bind: %d", code)
	}
	if New().maxBody != DefaultMaxBodyBytes {
		t.Fatal("default limit not set")
	}
}
