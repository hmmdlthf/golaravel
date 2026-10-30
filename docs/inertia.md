# Inertia.js Protocol Adapter ⚡️

GoLaravel includes native support for **Inertia.js**, enabling you to build modern single-page apps (React, Vue, Svelte) using classic server-side routing and controllers, without creating an API or maintaining state synchronization boilerplate.

---

## 1. Setting Up Inertia

Initialize the Inertia adapter and configure it on the application context:

```go
package main

import (
	"github.com/golaravel/golaravel"
	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/inertia"
)

func main() {
	app := golaravel.Default()

	// Initialize Inertia with an asset version string for cache busting
	inertiaEngine := inertia.New("1.0.0")
	context.SetInertiaRenderer(inertiaEngine)

	// Share global props across all views (e.g. auth user, app name)
	inertiaEngine.Share("appName", "GoLaravel Enterprise")

	app.GET("/users", func(c *golaravel.Context) error {
		return c.Inertia(200, "Users/Index", map[string]any{
			"users": []string{"Alice", "Bob"},
		})
	})

	app.Run(":8080")
}
```

---

## 2. How the Protocol Works

1. **Initial Browser Request**:
   - The user requests `http://localhost:8080/users` in their browser.
   - GoLaravel detects a full-page visit and returns an HTML document containing:
     ```html
     <div id="app" data-page='{"component":"Users/Index","props":{"users":["Alice","Bob"]},"url":"/users","version":"1.0.0"}'></div>
     ```
2. **Subsequent Navigation**:
   - Inertia intercepts client-side link clicks and sends an XHR request with the header `X-Inertia: true`.
   - GoLaravel responds with pure JSON:
     ```json
     {
       "component": "Users/Index",
       "props": { "users": ["Alice", "Bob"] },
       "url": "/users",
       "version": "1.0.0"
     }
     ```
   - Inertia swaps the view component dynamically without a full-page reload!

---

## 3. Asset Versioning & Conflict Handling

When deploying a new frontend bundle, update the asset version:
```go
inertiaEngine.SetVersion("2.0.0")
```

If a user with an older asset version makes a navigation request, GoLaravel automatically responds with:
- **HTTP 409 Conflict**
- `X-Inertia-Location: [current_url]`

This instructs the Inertia client to perform a full-page browser reload, cleanly loading the latest compiled JavaScript/CSS bundle.
