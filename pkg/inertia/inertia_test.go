package inertia

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golaravel/golaravel/pkg/context"
)

func TestInertia_FullPageVisit(t *testing.T) {
	engine := New("1.0.0")
	context.SetInertiaRenderer(engine)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	c := context.NewContext()
	c.Reset(w, req)

	err := c.Inertia(http.StatusOK, "Dashboard/Index", map[string]any{
		"user": "Taylor",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `<div id="app" data-page=`) {
		t.Fatalf("expected data-page in HTML body, got: %s", body)
	}
	if !strings.Contains(body, "Dashboard/Index") {
		t.Fatalf("expected component name in body, got: %s", body)
	}
}

func TestInertia_XInertiaVisit(t *testing.T) {
	engine := New("1.0.0")
	engine.Share("appName", "GoLaravel")
	context.SetInertiaRenderer(engine)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/users", nil)
	req.Header.Set(HeaderInertia, "true")
	req.Header.Set(HeaderInertiaVersion, "1.0.0")
	c := context.NewContext()
	c.Reset(w, req)

	err := c.Inertia(http.StatusOK, "Users/Index", map[string]any{
		"count": 42,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w.Header().Get(HeaderInertia) != "true" {
		t.Fatalf("expected X-Inertia: true header")
	}

	var page Page
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}

	if page.Component != "Users/Index" {
		t.Fatalf("expected Users/Index, got %s", page.Component)
	}
	if page.Props["appName"] != "GoLaravel" {
		t.Fatalf("expected shared prop appName=GoLaravel, got %v", page.Props["appName"])
	}
	if page.Props["count"] != float64(42) {
		t.Fatalf("expected count=42, got %v", page.Props["count"])
	}
}

func TestInertia_VersionConflict(t *testing.T) {
	engine := New("2.0.0")
	context.SetInertiaRenderer(engine)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.Header.Set(HeaderInertia, "true")
	req.Header.Set(HeaderInertiaVersion, "1.0.0") // Mismatch!
	c := context.NewContext()
	c.Reset(w, req)

	_ = c.Inertia(http.StatusOK, "Settings/Index", nil)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict, got %d", w.Code)
	}
	if w.Header().Get(HeaderInertiaLocation) != "/settings" {
		t.Fatalf("expected X-Inertia-Location /settings, got %s", w.Header().Get(HeaderInertiaLocation))
	}
}
