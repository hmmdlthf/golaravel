package router_test

import (
	stdctx "context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/router"
)

type TestUser struct {
	ID    int
	Name  string
	Email string
}

type TestPost struct {
	ID     int
	UserID int
	Title  string
	Slug   string
}

type TestInvoice struct {
	ID     int
	Token  string
	Amount float64
}

// In-memory mock repositories
var mockUsers = map[int]*TestUser{
	1: {ID: 1, Name: "Taylor", Email: "taylor@laravel.com"},
	2: {ID: 2, Name: "Ahmad", Email: "ahmad@golaravel.dev"},
}

var mockPosts = map[int]*TestPost{
	10: {ID: 10, UserID: 1, Title: "Laravel Architecture", Slug: "laravel-architecture"},
	20: {ID: 20, UserID: 2, Title: "GoLaravel Extreme Performance", Slug: "golaravel-performance"},
}

var mockInvoices = map[string]*TestInvoice{
	"tok_secret_123": {ID: 501, Token: "tok_secret_123", Amount: 399.00},
}

func TestRouteModelBinding_Explicit(t *testing.T) {
	r := router.New()

	// Register explicit model binder
	router.BindModel[*TestUser](r, "user", func(ctx stdctx.Context, val string) (*TestUser, error) {
		id, err := strconv.Atoi(val)
		if err != nil {
			return nil, err
		}
		u, ok := mockUsers[id]
		if !ok {
			return nil, router.ErrModelNotFound
		}
		return u, nil
	})

	// Register route with {user}
	r.GET("/users/{user}", func(c *context.Context) error {
		u, ok := context.GetModel[*TestUser](c, "user")
		if !ok {
			t.Fatalf("expected resolved model in context")
		}
		return c.String(http.StatusOK, "User: %s", u.Name)
	})

	// 1. Success case: /users/1 -> Taylor
	req := httptest.NewRequest(http.MethodGet, "/users/1", nil)
	rec := httptest.NewRecorder()
	c := context.NewContext()
	c.Reset(rec, req)
	handlers := r.Match(http.MethodGet, "/users/1", c)
	c.SetHandlers(handlers)
	_ = c.Next()

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if rec.Body.String() != "User: Taylor" {
		t.Fatalf("expected 'User: Taylor', got '%s'", rec.Body.String())
	}

	// 2. Not found case: /users/999 -> 404
	req404 := httptest.NewRequest(http.MethodGet, "/users/999", nil)
	rec404 := httptest.NewRecorder()
	c404 := context.NewContext()
	c404.Reset(rec404, req404)
	handlers404 := r.Match(http.MethodGet, "/users/999", c404)
	c404.SetHandlers(handlers404)
	_ = c404.Next()

	if rec404.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rec404.Code)
	}
}

func TestRouteModelBinding_CustomKeyPredicate(t *testing.T) {
	r := router.New()

	// Register custom field binder (invoice:token)
	router.BindFieldModel[*TestInvoice](r, "invoice", "token", func(ctx stdctx.Context, token string) (*TestInvoice, error) {
		inv, ok := mockInvoices[token]
		if !ok {
			return nil, router.ErrModelNotFound
		}
		return inv, nil
	})

	// Route with custom key syntax {invoice:token}
	r.GET("/invoices/{invoice:token}", router.With[*TestInvoice]("invoice", func(c *context.Context, inv *TestInvoice) error {
		return c.String(http.StatusOK, "Invoice #%d: $%.2f", inv.ID, inv.Amount)
	}))

	// Success case
	req := httptest.NewRequest(http.MethodGet, "/invoices/tok_secret_123", nil)
	rec := httptest.NewRecorder()
	c := context.NewContext()
	c.Reset(rec, req)
	handlers := r.Match(http.MethodGet, "/invoices/tok_secret_123", c)
	c.SetHandlers(handlers)
	_ = c.Next()

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if rec.Body.String() != "Invoice #501: $399.00" {
		t.Fatalf("expected 'Invoice #501: $399.00', got '%s'", rec.Body.String())
	}

	// Not found case with JSON header
	req404 := httptest.NewRequest(http.MethodGet, "/invoices/invalid_token", nil)
	req404.Header.Set("Accept", "application/json")
	rec404 := httptest.NewRecorder()
	c404 := context.NewContext()
	c404.Reset(rec404, req404)
	handlers404 := r.Match(http.MethodGet, "/invoices/invalid_token", c404)
	c404.SetHandlers(handlers404)
	_ = c404.Next()

	if rec404.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec404.Code)
	}
	if rec404.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Errorf("expected JSON 404 content type, got %s", rec404.Header().Get("Content-Type"))
	}
}

func TestRouteModelBinding_ScopedBinding(t *testing.T) {
	r := router.New()

	// 1. User binder
	router.BindModel[*TestUser](r, "user", func(ctx stdctx.Context, val string) (*TestUser, error) {
		id, _ := strconv.Atoi(val)
		if u, ok := mockUsers[id]; ok {
			return u, nil
		}
		return nil, router.ErrModelNotFound
	})

	// 2. Scoped Post binder (post belongs to user)
	router.BindScopedModel[*TestPost, *TestUser](r, "post", "user", func(ctx stdctx.Context, val string, user *TestUser) (*TestPost, error) {
		postID, _ := strconv.Atoi(val)
		post, ok := mockPosts[postID]
		if !ok || post.UserID != user.ID {
			// Not found OR does not belong to this user!
			return nil, router.ErrModelNotFound
		}
		return post, nil
	})

	// Route: /users/{user}/posts/{post} using With2 handler wrapper
	r.GET("/users/{user}/posts/{post}", router.With2[*TestUser, *TestPost]("user", "post", func(c *context.Context, u *TestUser, p *TestPost) error {
		return c.String(http.StatusOK, "%s wrote %s", u.Name, p.Title)
	}))

	// Case 1: Post 10 belongs to User 1 (Taylor) -> 200 OK
	req1 := httptest.NewRequest(http.MethodGet, "/users/1/posts/10", nil)
	rec1 := httptest.NewRecorder()
	c1 := context.NewContext()
	c1.Reset(rec1, req1)
	c1.SetHandlers(r.Match(http.MethodGet, "/users/1/posts/10", c1))
	_ = c1.Next()

	if rec1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec1.Code)
	}
	if rec1.Body.String() != "Taylor wrote Laravel Architecture" {
		t.Fatalf("unexpected body: %s", rec1.Body.String())
	}

	// Case 2: Post 20 belongs to User 2 (Ahmad), but requested via User 1 (/users/1/posts/20) -> 404 Scoped Isolation!
	req2 := httptest.NewRequest(http.MethodGet, "/users/1/posts/20", nil)
	rec2 := httptest.NewRecorder()
	c2 := context.NewContext()
	c2.Reset(rec2, req2)
	c2.SetHandlers(r.Match(http.MethodGet, "/users/1/posts/20", c2))
	_ = c2.Next()

	if rec2.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for mismatched scoped parent-child, got %d", rec2.Code)
	}
}

func TestCustomModelNotFoundHandler(t *testing.T) {
	r := router.New()

	router.BindModel[*TestUser](r, "user", func(ctx stdctx.Context, val string) (*TestUser, error) {
		return nil, errors.New("database connection failed")
	})

	r.SetModelNotFoundHandler(func(c *context.Context) error {
		return c.String(http.StatusTeapot, "I'm a teapot, user does not exist")
	})

	r.GET("/users/{user}", func(c *context.Context) error {
		return c.String(http.StatusOK, "OK")
	})

	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	rec := httptest.NewRecorder()
	c := context.NewContext()
	c.Reset(rec, req)
	c.SetHandlers(r.Match(http.MethodGet, "/users/42", c))
	_ = c.Next()

	if rec.Code != http.StatusTeapot {
		t.Fatalf("expected custom status 418, got %d", rec.Code)
	}
	if rec.Body.String() != "I'm a teapot, user does not exist" {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}
