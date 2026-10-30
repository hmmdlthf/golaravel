# Routing & Controllers ⚡️

GoLaravel features an ultra-high-performance Radix Trie router designed from the ground up to achieve **zero heap allocations on the hot path**.

---

## 1. Defining Routes

Routes are registered directly on the `Engine`:

```go
package main

import (
	"github.com/golaravel/golaravel"
)

func main() {
	app := golaravel.Default()

	// Basic GET route
	app.GET("/hello", func(c *golaravel.Context) error {
		return c.String(200, "Hello World")
	})

	// JSON response
	app.GET("/api/ping", func(c *golaravel.Context) error {
		return c.JSON(200, map[string]string{"status": "ok"})
	})

	app.Run(":8080")
}
```

Supported HTTP methods: `GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `OPTIONS`, `HEAD`, and `Any`.

---

## 2. Route Parameters

GoLaravel supports both standard colon (`:param`) and Laravel curly brace (`{param}`) syntax:

```go
app.GET("/users/:id", func(c *golaravel.Context) error {
	id := c.Param("id")
	return c.JSON(200, map[string]string{"user_id": id})
})

// Laravel curly braces:
app.GET("/posts/{slug}/comments/{commentId}", func(c *golaravel.Context) error {
	slug := c.Param("slug")
	commentId := c.Param("commentId")
	return c.String(200, "Post: %s, Comment: %s", slug, commentId)
})
```

### Zero-Allocation Guarantee
Route parameter values are populated directly into an inline fixed-size array (`[16]Param`) on the pooled context, **completely eliminating heap slice allocations** during matching.

---

## 3. Route Model Binding (PRD §3.1)

GoLaravel provides compile-time type-safe Route Model Binding. When you type-hint or bind an entity to a route parameter, the framework automatically resolves the entity from Ent ORM, injects it into the handler, or returns a standard 404 response.

### A. Explicit Model Binding (`router.BindModel`)

Register a model binder for a parameter:

```go
router.BindModel[*ent.User](app.Router, "user", func(ctx context.Context, idStr string) (*ent.User, error) {
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return nil, err
	}
	return client.User.Get(ctx, id)
})
```

Define a route using `{user}`:

```go
// Direct typed handler:
app.GET("/users/{user}", router.With[*ent.User]("user", func(c *gcontext.Context, user *ent.User) error {
	return c.JSON(200, user)
}))

// Or retrieve from Context:
app.GET("/users/{user}", func(c *gcontext.Context) error {
	user := context.MustGetModel[*ent.User](c, "user")
	return c.JSON(200, user)
})
```

If the user does not exist, GoLaravel automatically halts the pipeline and responds with:
```json
{
  "message": "No query results for model [user] with key [999]",
  "status": 404
}
```

---

### B. Custom Field & Predicate Binding (`{param:field}`)

To resolve entities by unique fields other than the primary key (e.g. `token`, `slug`, `uuid`):

```go
// Register field-specific binder for {invoice:token}
router.BindFieldModel[*ent.Invoice](app.Router, "invoice", "token", func(ctx context.Context, token string) (*ent.Invoice, error) {
	return client.Invoice.Query().
		Where(invoice.TokenEQ(token)).
		WithItems().
		Only(ctx)
})

// Route definition with custom predicate syntax:
app.GET("/invoices/{invoice:token}", router.With[*ent.Invoice]("invoice", func(c *gcontext.Context, inv *ent.Invoice) error {
	return c.Templ(200, views.PublicInvoiceView(inv))
}))
```

---

### C. Scoped Child Model Binding (`router.BindScopedModel`)

In nested resource routes (e.g., `/users/{user}/posts/{post}`), scoped binding automatically ensures that the child entity actually belongs to the parent entity, preventing unauthorized cross-tenant data access:

```go
// Scoped binder: post must belong to user
router.BindScopedModel[*ent.Post, *ent.User](app.Router, "post", "user", func(ctx context.Context, postIDStr string, user *ent.User) (*ent.Post, error) {
	postID, _ := strconv.Atoi(postIDStr)
	return client.Post.Query().
		Where(post.IDEQ(postID), post.HasAuthorWith(user.IDEQ(user.ID))).
		Only(ctx)
})

// Route definition:
app.GET("/users/{user}/posts/{post}", router.With2[*ent.User, *ent.Post]("user", "post", func(c *gcontext.Context, u *ent.User, p *ent.Post) error {
	return c.JSON(200, p)
}))
```
If post `20` exists but does not belong to user `1`, requesting `/users/1/posts/20` returns `404 Not Found`.

---

### D. Customizing the ModelNotFound Handler

Customize how missing models are handled globally:

```go
app.Router.SetModelNotFoundHandler(func(c *gcontext.Context) error {
	return c.JSON(404, map[string]string{
		"error": "The requested resource could not be found.",
	})
})
```

---

## 4. Route Groups & Middleware Stacking

Route groups allow you to share common URL prefixes and middleware stacks:

```go
// Public API
api := app.Group("/api")
api.GET("/status", statusHandler)

// Protected API with JWT Bearer Guard
v1 := api.Group("/v1", auth.BearerGuard(jwtManager))
{
	v1.GET("/profile", profileHandler)
	v1.POST("/settings", updateSettingsHandler)
}
```

---

## 5. Named Routes & Reverse URL Generation

You can assign names to routes for safe reverse URL generation:

```go
app.GET("/invoices/:token", invoiceHandler).Name("invoice.show")

// Reverse URL generation
url, err := app.Router.URL("invoice.show", "token", "INV-2026-X8")
// Result: "/invoices/INV-2026-X8"
```

---

## 6. Controller Pattern

Controllers group related HTTP logic into struct methods:

```go
package handlers

import (
	"net/http"
	"github.com/golaravel/golaravel"
)

type UserController struct {
	// dependencies
}

func NewUserController() *UserController {
	return &UserController{}
}

func (ctrl *UserController) Index(c *golaravel.Context) error {
	return c.JSON(http.StatusOK, []string{"Alice", "Bob"})
}

func (ctrl *UserController) Show(c *golaravel.Context) error {
	id := c.Param("id")
	return c.JSON(http.StatusOK, map[string]string{"id": id})
}
```

Generate controllers instantly with the CLI:
```bash
artisan make:controller UserController
```
