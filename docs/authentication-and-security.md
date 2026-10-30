# Authentication & Security 🔒

GoLaravel includes multi-guard authentication (JWT Bearer tokens and Session cookies), modern cryptographic hashing (Argon2id and Bcrypt), authorization Gates & Policies, and production security middlewares.

---

## 1. Password Hashing (`pkg/hash`)

GoLaravel supports both **Argon2id** and **Bcrypt**:

```go
import "github.com/golaravel/golaravel/pkg/hash"

// Hash a password (defaults to Bcrypt with cost 10)
hashed, err := hash.Make("secretPassword123")

// Hash with Argon2id
argonHashed, err := hash.Argon2Make("secretPassword123")

// Verify password against either Bcrypt or Argon2id automatically:
valid := hash.Check("secretPassword123", hashed) // true
```

---

## 2. JWT Bearer Tokens (`pkg/auth`)

Ideal for Flutter clients (Mobile/Desktop/Web) and single-page applications:

```go
import "github.com/golaravel/golaravel/pkg/auth"

jwtManager := auth.NewJWTManager("your-32-byte-secret-key-goes-here", 24*time.Hour)

// Generating a token on login:
token, err := jwtManager.GenerateToken(fmt.Sprint(user.ID), map[string]any{
	"role": user.Role,
	"name": user.Name,
})

// Protecting routes with the BearerGuard:
api := app.Group("/api", auth.BearerGuard(jwtManager))
api.GET("/profile", func(c *golaravel.Context) error {
	user := auth.User(c)
	claims := auth.Claims(c)
	return c.JSON(200, map[string]any{
		"user_id": user.GetAuthIdentifier(),
		"role":    claims.Custom["role"],
	})
})
```

---

## 3. Authorization Gates & Policies

Define fine-grained permission checks using Gates:

```go
// Define a gate
auth.Define("edit-invoice", func(user auth.Authenticatable, args ...any) bool {
	if len(args) == 0 {
		return false
	}
	inv := args[0].(*app.Invoice)
	return inv.OwnerID == user.GetAuthIdentifier()
})

// Check permission inside a handler:
func UpdateInvoice(c *golaravel.Context) error {
	user := auth.User(c)
	if auth.Denies("edit-invoice", user, currentInvoice) {
		return c.String(403, "Forbidden")
	}
	// ...
}
```

Generate a policy using Artisan:
```bash
artisan make:policy InvoicePolicy
```

---

## 4. Built-in Security Middlewares

```go
import "github.com/golaravel/golaravel/pkg/middleware"

// Global recovery from panics (prevents crashes, logs stack trace, returns 500)
app.Use(middleware.Recovery())

// Secure HTTP Headers (HSTS, X-Content-Type-Options: nosniff, X-Frame-Options)
app.Use(middleware.SecureHeaders())

// Cross-Origin Resource Sharing (CORS)
app.Use(middleware.CORS(middleware.DefaultCORS()))

// Double Submit Cookie CSRF verification
app.Use(middleware.CSRF())

// In-Memory Token Bucket Rate Limiting (e.g. 60 requests/minute per IP)
app.Use(middleware.RateLimiter(60))
```
