package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golaravel/golaravel/pkg/context"
)

func TestRouter_Static(t *testing.T) {
	r := New()
	called := false
	r.GET("/hello", func(c *context.Context) error {
		called = true
		return c.String(http.StatusOK, "world")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/hello", nil)
	c := context.NewContext()
	c.Reset(w, req)

	handlers := r.Match("GET", "/hello", c)
	if handlers == nil {
		t.Fatal("expected route to match")
	}
	c.SetHandlers(handlers)
	if err := c.Next(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !called {
		t.Fatal("expected handler to be called")
	}
	if w.Body.String() != "world" {
		t.Fatalf("expected 'world', got '%s'", w.Body.String())
	}
}

func TestRouter_Params(t *testing.T) {
	r := New()
	var userID, postID string

	r.GET("/users/:user_id/posts/:post_id", func(c *context.Context) error {
		userID = c.Param("user_id")
		postID = c.Param("post_id")
		return c.NoContent(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/users/42/posts/99", nil)
	c := context.NewContext()
	c.Reset(w, req)

	handlers := r.Match("GET", "/users/42/posts/99", c)
	if handlers == nil {
		t.Fatal("expected route to match")
	}
	c.SetHandlers(handlers)
	_ = c.Next()

	if userID != "42" || postID != "99" {
		t.Fatalf("expected user_id=42, post_id=99, got user_id=%s, post_id=%s", userID, postID)
	}
}

func TestRouter_LaravelCurlyBraces(t *testing.T) {
	r := New()
	var entityID string

	r.GET("/entities/{id}", func(c *context.Context) error {
		entityID = c.Param("id")
		return nil
	})

	c := context.NewContext()
	c.Reset(httptest.NewRecorder(), httptest.NewRequest("GET", "/entities/uuid-1234", nil))

	handlers := r.Match("GET", "/entities/uuid-1234", c)
	if handlers == nil {
		t.Fatal("expected route with curly braces to match")
	}
	c.SetHandlers(handlers)
	_ = c.Next()

	if entityID != "uuid-1234" {
		t.Fatalf("expected id=uuid-1234, got %s", entityID)
	}
}

func TestRouter_GroupAndMiddleware(t *testing.T) {
	r := New()
	logTrace := make([]string, 0, 4)

	r.Use(func(c *context.Context) error {
		logTrace = append(logTrace, "global-start")
		err := c.Next()
		logTrace = append(logTrace, "global-end")
		return err
	})

	api := r.Group("/api", func(c *context.Context) error {
		logTrace = append(logTrace, "group-middleware")
		return c.Next()
	})

	api.GET("/v1/ping", func(c *context.Context) error {
		logTrace = append(logTrace, "handler")
		return c.String(http.StatusOK, "pong")
	})

	c := context.NewContext()
	c.Reset(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/ping", nil))

	handlers := r.Match("GET", "/api/v1/ping", c)
	if handlers == nil {
		t.Fatal("expected group route to match")
	}
	c.SetHandlers(handlers)
	_ = c.Next()

	expectedTrace := []string{"global-start", "group-middleware", "handler", "global-end"}
	if len(logTrace) != len(expectedTrace) {
		t.Fatalf("expected trace %v, got %v", expectedTrace, logTrace)
	}
	for i := range expectedTrace {
		if logTrace[i] != expectedTrace[i] {
			t.Fatalf("at index %d: expected %s, got %s", i, expectedTrace[i], logTrace[i])
		}
	}
}

func TestRouter_NamedRoutesAndURL(t *testing.T) {
	r := New()
	r.GET("/articles/:slug/comments/:id", func(c *context.Context) error {
		return nil
	}).Name("articles.comments.show")

	url, err := r.URL("articles.comments.show", "slug", "speed-of-go", "id", "77")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "/articles/speed-of-go/comments/77" {
		t.Fatalf("expected /articles/speed-of-go/comments/77, got %s", url)
	}
}

func BenchmarkRouter_Match_Static(b *testing.B) {
	r := New()
	r.GET("/api/v1/users/profile/settings", func(c *context.Context) error {
		return nil
	})

	c := context.NewContext()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = r.Match("GET", "/api/v1/users/profile/settings", c)
	}
}

func BenchmarkRouter_Match_Param(b *testing.B) {
	r := New()
	r.GET("/api/v1/users/:user_id/posts/:post_id", func(c *context.Context) error {
		return nil
	})

	c := context.NewContext()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c.Reset(nil, nil)
		_ = r.Match("GET", "/api/v1/users/12345/posts/67890", c)
	}
}
