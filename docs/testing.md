# Full-Stack HTTP Testing Harness & Assertion Toolkit (pkg/testing) (PRD §5) 🧪

GoLaravel provides a fluent, high-speed HTTP testing harness modeled after Laravel's expressive testing ergonomics (`$this->get('/users')->assertStatus(200)`), re-engineered for Go.

Instead of spinning up slow TCP sockets or managing external test containers, GoLaravel tests execute directly against your application's `http.Handler` pipeline in memory. A full end-to-end HTTP request with route model binding, middleware authentication, JSON parsing, and response rendering runs in **< 100 microseconds**.

---

## 🚀 Key Capabilities

- **Zero-Network Overhead**: Direct in-memory request execution via `httptest.ResponseRecorder` and `app.ServeHTTP`.
- **Fluent Request Builder**: Effortlessly chain headers, JWT bearer tokens, cookies, forms, JSON payloads, and multipart file uploads.
- **Expressive Status Assertions**: `AssertOK()`, `AssertCreated()`, `AssertRedirect(path)`, `AssertUnprocessable()`, `AssertNotFound()`, `AssertUnauthorized()`, etc.
- **Deep JSON Assertions**:
  - `AssertJsonPath("user.name", "Alice")` with dot-delimited path navigation.
  - `AssertJsonPath("token", func(token string) bool { ... })` with predicate functions.
  - `AssertJsonCount(2, "user.roles")` for array length assertions.
  - `AssertJsonValidationErrors("email", "password")` for Laravel 422 error bag validation.
- **HTML & Text Assertions**: `AssertSee()`, `AssertDontSee()`, `AssertSeeInOrder()`, and `AssertContentType()`.
- **Debugging & Inspection**: Chainable `res.Dump()` and `res.DumpBody()` logging status, headers, and payload to standard test output.

---

## 🛠️ Quick Start

Initialize the testing harness using `app.Test(t)` or `golaravel.Test(t, app)`:

```go
package main_test

import (
    "testing"
    "github.com/golaravel/golaravel"
)

func TestUserEndpoint(t *testing.T) {
    app := setupApp() // returns *golaravel.Engine

    app.Test(t).
        Get("/api/health").
        AssertOK().
        AssertJsonPath("status", "healthy")
}
```

---

## 📝 Fluent Request Building

### 1. JSON APIs & Headers

```go
res := app.Test(t).
    WithToken(jwtToken).                            // Authorization: Bearer <token>
    WithHeader("X-Tenant-ID", "tenant_marketing").  // Custom header
    PostJSON("/api/campaigns", map[string]any{
        "name":     "Black Friday 2026",
        "budget":   50000.00,
        "channels": []string{"email", "sms"},
    })

res.AssertCreated().
    AssertJsonPath("name", "Black Friday 2026").
    AssertJsonPath("budget", 50000.00).
    AssertJsonCount(2, "channels")
```

### 2. Authentication & Credentials

```go
// Bearer JWT Token
client.WithToken("eyJh...").Get("/api/profile").AssertOK()

// Basic Auth
client.WithBasicAuth("admin", "secret123").Get("/admin").AssertOK()

// Cookies
client.WithCookie("session_id", "sess_101").Get("/dashboard").AssertOK()
```

### 3. Form & Multipart File Uploads

```go
documentBytes := []byte("%PDF-1.4 Mock Contract Content")

res := app.Test(t).
    Request("POST", "/contracts/upload").
    WithForm(map[string]string{
        "title": "Master Services Agreement",
    }).
    WithFile("document", "msa.pdf", documentBytes, "application/pdf").
    Send()

res.AssertOK().
    AssertJsonPath("title", "Master Services Agreement").
    AssertJsonPath("filename", "msa.pdf").
    AssertJsonPath("size", len(documentBytes))
```

---

## 🔍 Assertions Catalog

### HTTP Status Assertions

| Method | Description |
|---|---|
| `AssertStatus(code)` | Asserts response matches exact HTTP status code |
| `AssertOK()` | Asserts status code is **200 OK** |
| `AssertCreated()` | Asserts status code is **201 Created** |
| `AssertAccepted()` | Asserts status code is **202 Accepted** |
| `AssertNoContent()` | Asserts status code is **204 No Content** |
| `AssertFound()` | Asserts status code is **302 Found** |
| `AssertMovedPermanently()` | Asserts status code is **301 Moved Permanently** |
| `AssertRedirect(target...)` | Asserts redirect (3xx) and optionally verifies `Location` header |
| `AssertBadRequest()` | Asserts status code is **400 Bad Request** |
| `AssertUnauthorized()` | Asserts status code is **401 Unauthorized** |
| `AssertForbidden()` | Asserts status code is **403 Forbidden** |
| `AssertNotFound()` | Asserts status code is **404 Not Found** |
| `AssertMethodNotAllowed()` | Asserts status code is **405 Method Not Allowed** |
| `AssertUnprocessable()` | Asserts status code is **422 Unprocessable Entity** |
| `AssertServerError()` | Asserts status code is **500 Internal Server Error** |
| `AssertSuccessful()` | Asserts status code is in the **2xx** range |
| `AssertClientError()` | Asserts status code is in the **4xx** range |

### JSON & Error Bag Assertions

```go
// 1. Partial subset matching
res.AssertJson(map[string]any{
    "active": true,
    "user": map[string]any{
        "role": "admin",
    },
})

// 2. Exact match
res.AssertExactJson(expectedFullObject)

// 3. Dot-delimited path assertions
res.AssertJsonPath("data.0.id", 101)
res.AssertJsonPath("data.0.status", "paid")
res.AssertJsonMissingPath("data.0.secret_pin")

// 4. Predicate matcher functions
res.AssertJsonPath("token", func(token string) bool {
    return len(token) > 20
})

// 5. Array count
res.AssertJsonCount(5, "data")

// 6. Validation error bags (422)
res.AssertJsonValidationErrors("email", "password")

// 7. Deserialize into struct
var user UserResponse
err := res.DecodeJson(&user)
```

### HTML, View & Text Assertions

```go
// Blade / Templ HTML View Testing
app.Test(t).
    Get("/invoices/token_abc").
    AssertOK().
    AssertContentType("text/html").
    AssertSee("Invoice #INV-2026").
    AssertSeeInOrder("Subtotal", "Tax", "Total").
    AssertDontSee("Fatal Error")
```

### Headers & Cookies

```go
res.AssertHeader("X-Frame-Options", "DENY")
res.AssertHeaderMissing("X-Deprecated-Header")
res.AssertContentType("application/json")
res.AssertCookie("auth_session", "session_token_xyz")
res.AssertCookieMissing("remember_token")

cookie := res.Cookie("auth_session")
```

---

## 🐛 Debugging Test Failures (`Dump`)

To inspect status, headers, and payload during test execution, chain `.Dump()` or `.DumpBody()`:

```go
app.Test(t).
    Get("/api/debug").
    Dump().       // Outputs full status, headers, and body to `go test -v` log
    AssertOK()
```

Output:
```text
--- Response Dump [200] ---
Content-Type: application/json; charset=utf-8
X-Custom-Header: active

{"id": 42, "status": "ok"}
--------------------------
```
