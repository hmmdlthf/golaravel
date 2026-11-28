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

func TestRouter_CatchAllWithExistingPrefix(t *testing.T) {
	r := New()
	r.GET("/admin/assets/*filepath", func(c *context.Context) error {
		return c.String(http.StatusOK, "assets:%s", c.Param("filepath"))
	})
	r.GET("/admin/api/schema", func(c *context.Context) error {
		return c.String(http.StatusOK, "schema")
	})
	r.GET("/admin/*filepath", func(c *context.Context) error {
		return c.String(http.StatusOK, "spa:%s", c.Param("filepath"))
	})

	// Test static API route
	c := context.NewContext()
	c.Reset(httptest.NewRecorder(), httptest.NewRequest("GET", "/admin/api/schema", nil))
	handlers := r.Match("GET", "/admin/api/schema", c)
	if handlers == nil {
		t.Fatal("expected /admin/api/schema to match")
	}

	// Test assets route
	c.Reset(httptest.NewRecorder(), httptest.NewRequest("GET", "/admin/assets/app.js", nil))
	handlers = r.Match("GET", "/admin/assets/app.js", c)
	if handlers == nil {
		t.Fatal("expected /admin/assets/app.js to match")
	}

	// Test catch-all fallback route
	c.Reset(httptest.NewRecorder(), httptest.NewRequest("GET", "/admin/dashboard", nil))
	handlers = r.Match("GET", "/admin/dashboard", c)
	if handlers == nil {
		t.Fatal("expected /admin/dashboard to match catch-all")
	}

	// Test deep subpath under catch-all
	c.Reset(httptest.NewRecorder(), httptest.NewRequest("GET", "/admin/other/deep/file.otf", nil))
	handlers = r.Match("GET", "/admin/other/deep/file.otf", c)
	if handlers == nil {
		t.Fatal("expected /admin/other/deep/file.otf to match catch-all")
	}

	// Test assets under catch-all when no /admin/assets route exists
	r2 := New()
	r2.GET("/admin/api/schema", func(c *context.Context) error {
		return c.String(http.StatusOK, "schema")
	})
	r2.GET("/admin/*filepath", func(c *context.Context) error {
		return c.String(http.StatusOK, "spa:%s", c.Param("filepath"))
	})
	c.Reset(httptest.NewRecorder(), httptest.NewRequest("GET", "/admin/assets/fonts/MaterialIcons-Regular.otf", nil))
	handlers = r2.Match("GET", "/admin/assets/fonts/MaterialIcons-Regular.otf", c)
	if handlers == nil {
		t.Fatal("expected /admin/assets/fonts/MaterialIcons-Regular.otf to match catch-all on r2")
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

func TestRouterStaticRouteRegisteredAfterParamSibling(t *testing.T) {
	r := New()
	hit := func(name string) context.HandlerFunc {
		return func(c *context.Context) error { c.Set("hit", name); return nil }
	}
	// parameter route first, static siblings later (the order panels use)
	r.POST("/r/:id/actions/:action", hit("action"))
	r.POST("/r/reorder", hit("reorder"))
	r.POST("/r/bulk", hit("bulk"))
	r.POST("/r/:id", hit("update"))
	r.POST("/r/:id/cell", hit("cell"))
	r.GET("/r/export", hit("export"))
	r.GET("/r/:id", hit("show"))

	cases := []struct{ method, path, want, id string }{
		{"POST", "/r/reorder", "reorder", ""},
		{"POST", "/r/bulk", "bulk", ""},
		{"POST", "/r/7", "update", "7"},
		{"POST", "/r/7/cell", "cell", "7"},
		{"POST", "/r/7/actions/post", "action", "7"},
		{"GET", "/r/export", "export", ""},
		{"GET", "/r/9", "show", "9"},
	}
	for _, tc := range cases {
		c := context.NewContext()
		hs := r.Match(tc.method, tc.path, c)
		if hs == nil {
			t.Errorf("%s %s: no match", tc.method, tc.path)
			continue
		}
		c.SetHandlers(hs)
		_ = c.Next()
		got, _ := c.Get("hit")
		if got != tc.want || c.Param("id") != tc.id {
			t.Errorf("%s %s: got %v id=%q, want %s id=%q", tc.method, tc.path, got, c.Param("id"), tc.want, tc.id)
		}
	}
}

// A static route registered before a parameter sibling under the same
// parent parameter must still match (it used to be shadowed by the
// parameter route because the parent parameter's child was not indexed).
func TestStaticRouteBeforeParamSiblingUnderParam(t *testing.T) {
	orders := [][]string{
		{"/a/:id/rel/notes/uploads", "/a/:id/rel/notes", "/a/:id/rel/notes/:child", "/a/:id/rel/notes/:child/delete"},
		{"/a/:id/rel/notes", "/a/:id/rel/notes/:child", "/a/:id/rel/notes/:child/delete", "/a/:id/rel/notes/uploads"},
		{"/a/:id/rel/notes/:child/delete", "/a/:id/rel/notes/uploads", "/a/:id/rel/notes/:child", "/a/:id/rel/notes"},
	}
	want := map[string]string{
		"/a/1/rel/notes/uploads":  "/a/:id/rel/notes/uploads",
		"/a/1/rel/notes":          "/a/:id/rel/notes",
		"/a/1/rel/notes/7":        "/a/:id/rel/notes/:child",
		"/a/1/rel/notes/7/delete": "/a/:id/rel/notes/:child/delete",
	}
	for _, order := range orders {
		r := New()
		r.POST("/a/uploads", func(c *context.Context) error { c.Set("route", "/a/uploads"); return nil })
		for _, p := range order {
			p := p
			r.POST(p, func(c *context.Context) error { c.Set("route", p); return nil })
		}
		for path, route := range want {
			c := context.NewContext()
			hs := r.Match("POST", path, c)
			if hs == nil {
				t.Fatalf("order %v: %s did not match", order, path)
			}
			c.SetHandlers(hs)
			_ = c.Next()
			if got, _ := c.Get("route"); got != route {
				t.Fatalf("order %v: %s matched %v, want %s", order, path, got, route)
			}
		}
		if c := context.NewContext(); true {
			r.Match("POST", "/a/1/rel/notes/7", c)
			if c.Param("child") != "7" || c.Param("id") != "1" {
				t.Fatalf("params: id=%q child=%q", c.Param("id"), c.Param("child"))
			}
		}
	}
}
