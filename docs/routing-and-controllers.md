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

## 3. Route Groups & Middleware Stacking

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

## 4. Named Routes & Reverse URL Generation

You can assign names to routes for safe reverse URL generation:

```go
app.GET("/invoices/:token", invoiceHandler).Name("invoice.show")

// Reverse URL generation
url, err := app.Router.URL("invoice.show", "token", "INV-2026-X8")
// Result: "/invoices/INV-2026-X8"
```

---

## 5. Controller Pattern

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
