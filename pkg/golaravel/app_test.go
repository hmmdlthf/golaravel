package golaravel

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/a-h/templ"
	"github.com/golaravel/golaravel/pkg/container"
	gcontext "github.com/golaravel/golaravel/pkg/context"
)

type UserPayload struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

func TestEngine_ServeHTTP_JSON(t *testing.T) {
	app := New()
	app.GET("/api/users/:id", func(c *gcontext.Context) error {
		_ = c.Param("id")
		return c.JSON(http.StatusOK, UserPayload{
			ID:    42,
			Name:  "Laravel Go",
			Email: "golaravel@example.com",
		})
	})

	req := httptest.NewRequest("GET", "/api/users/42", nil)
	w := httptest.NewRecorder()

	app.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	expectedJSON := `{"id":42,"name":"Laravel Go","email":"golaravel@example.com"}` + "\n"
	if w.Body.String() != expectedJSON {
		t.Fatalf("expected %s, got %s", expectedJSON, w.Body.String())
	}
}

func TestEngine_ServeHTTP_Templ(t *testing.T) {
	app := New()

	// A mock compiled templ component
	mockComponent := templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_, err := io.WriteString(w, "<h1>Welcome to GoLaravel with Templ</h1>")
		return err
	})

	app.GET("/", func(c *gcontext.Context) error {
		return c.Templ(http.StatusOK, mockComponent)
	})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	app.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != "<h1>Welcome to GoLaravel with Templ</h1>" {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestEngine_DI_Scoped(t *testing.T) {
	app := New()

	type RequestTracker struct {
		Path string
	}

	app.Use(func(c *gcontext.Context) error {
		container.Instance(c.Container, &RequestTracker{Path: c.Request.URL.Path})
		return c.Next()
	})

	app.GET("/test", func(c *gcontext.Context) error {
		tracker := container.MustResolve[*RequestTracker](c.Container)
		return c.String(http.StatusOK, "%s", tracker.Path)
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	app.ServeHTTP(w, req)

	if w.Body.String() != "/test" {
		t.Fatalf("expected '/test', got '%s'", w.Body.String())
	}
}

func BenchmarkEngine_ServeHTTP_JSON(b *testing.B) {
	app := New()
	app.GET("/api/users/:id", func(c *gcontext.Context) error {
		return c.JSON(http.StatusOK, UserPayload{
			ID:    42,
			Name:  "Taylor Otwell",
			Email: "taylor@laravel.com",
		})
	})

	req := httptest.NewRequest("GET", "/api/users/42", nil)
	w := httptest.NewRecorder()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		w.Body.Reset()
		app.ServeHTTP(w, req)
	}
}

func BenchmarkEngine_ServeHTTP_PlainString(b *testing.B) {
	app := New()
	app.GET("/ping", func(c *gcontext.Context) error {
		return c.String(http.StatusOK, "pong")
	})

	req := httptest.NewRequest("GET", "/ping", nil)
	w := httptest.NewRecorder()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		w.Body.Reset()
		app.ServeHTTP(w, req)
	}
}
