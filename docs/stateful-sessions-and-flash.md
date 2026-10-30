# Stateful Sessions & Flash Messages 🍪 (PRD §3.3)

GoLaravel provides a high-performance, encrypted stateful session engine matching Laravel's session developer ergonomics. It supports **AES-256-GCM authenticated encryption**, multiple storage drivers (Cookie, Memory, File), automatic **one-request Flash message lifecycle aging**, and unified **session-backed CSRF protection**.

---

## 1. Quickstart & Middleware Registration

To enable stateful sessions across your web routes, register the `session.StartSession()` middleware on your application or route group:

```go
import (
	"github.com/golaravel/golaravel/pkg/golaravel"
	"github.com/golaravel/golaravel/pkg/session"
)

app := golaravel.New()

// Register session middleware globally or on web route group:
app.Use(session.StartSession())

// Optional: verify CSRF tokens using the session's token:
app.Use(session.VerifyCSRFToken())
```

---

## 2. Reading & Writing Session Data

Inside any HTTP controller or handler, interact with the session directly via `session.FromContext(c)` or package helpers:

```go
// 1. Storing data in session:
s := session.FromContext(c)
s.Put("user_id", "42")
s.Put("cart_count", 5)

// Package-level shortcut:
session.Put(c, "role", "admin")

// 2. Reading data from session:
userID := s.GetString("user_id") // "42"
cartCount := s.GetInt("cart_count") // 5
isAdmin := s.GetBool("is_admin", false) // false if missing

// 3. Checking existence:
if s.Has("user_id") {
    // Key exists and is not nil
}

// 4. Push to slice:
s.Push("recent_items", "item_123")

// 5. Pull (get and delete):
promoCode := s.Pull("promo_code")

// 6. Forget or Flush:
s.Forget("cart_count") // deletes specific key
s.Flush()              // clears entire session
```

---

## 3. Flash Messages (Alerts Across Redirects)

Flash messages are stored for the **immediate subsequent request only**, and automatically purged thereafter. This is the classic Laravel pattern for notifications after form submissions and redirects:

### Storing a Flash Message:
```go
func (ctrl *InvoiceController) Store(c *context.Context) error {
    // ... create invoice ...
    
    // Flash a success notification for the next page load:
    session.Flash(c, "success", "Invoice #INV-2026-001 created successfully!")
    
    return c.Redirect(http.StatusSeeOther, "/invoices")
}
```

### Displaying Flash Messages in Templ Views:
In the redirected `/invoices` handler or Templ component:

```go
func (ctrl *InvoiceController) Index(c *context.Context) error {
    flashMsg := session.GetString(c, "success")
    // flashMsg contains "Invoice #INV-2026-001 created successfully!"
    
    return c.Templ(http.StatusOK, views.InvoiceList(invoices, flashMsg))
}
```

### Preserving Flash Data:
- `s.Now(key, val)`: Flash available **only** for the current request.
- `s.Keep("success")`: Preserves a specific flash key for an additional request cycle.
- `s.Reflash()`: Preserves **all** flash messages for another request cycle.

---

## 4. Session Drivers

GoLaravel supports multiple session drivers configured via `session.Config`:

| Driver | Storage Location | Best For |
|---|---|---|
| **CookieDriver** | Encrypted client-side HTTP cookie | Stateless backend servers, micro-SSRs, zero server memory/disk usage |
| **MemoryDriver** (default) | Server memory with TTL | Rapid development, fast single-node instances |
| **FileDriver** | Server disk (`storage/framework/sessions`) | Persistent state without requiring Redis |

### Configuring Drivers:
```go
// 1. Stateless Encrypted Cookie Driver:
cookieManager := session.NewManager(session.Config{
    Driver:     session.NewCookieDriver(),
    CookieName: "golaravel_session",
    SecretKey:  "your-32-byte-secret-encryption-key!",
    Lifetime:   120 * time.Minute,
})

// 2. Persistent File Driver:
fileDriver, _ := session.NewFileDriver("storage/framework/sessions")
fileManager := session.NewManager(session.Config{
    Driver:    fileDriver,
    SecretKey: "your-32-byte-secret-encryption-key!",
})

app.Use(session.StartSession(cookieManager))
```

---

## 5. Security & CSRF Integration

### Authenticated AES-256-GCM Encryption
All session cookies are encrypted with **AES-256-GCM authenticated encryption**. Any client-side tampering, forgery, or corruption immediately fails authentication tag verification, causing the session to be safely discarded.

### Session Fixation Protection:
Regenerate the session ID whenever a user logs in or changes authentication state:
```go
s := session.FromContext(c)
_ = s.Regenerate() // Generates a fresh 40-character random session ID
```

### Unified CSRF Protection:
Each session maintains a cryptographically secure 40-character CSRF token (`_token`):

```go
// Retrieve the CSRF token in handlers or view components:
token := session.Token(c)

// Enforce CSRF verification on POST, PUT, PATCH, DELETE:
app.Use(session.VerifyCSRFToken())
```
Clients can send the token via:
- HTTP Header: `X-CSRF-TOKEN` or `X-XSRF-TOKEN`
- Form Input: `<input type="hidden" name="_token" value="..." />`
