# Fluent HTTP Client & Mocking Engine (pkg/http) (PRD §7) 🌐

GoLaravel features an expressive, fluent HTTP client modeled directly after Laravel's `Http` facade (`Illuminate\Support\Facades\Http`), engineered with Go's compiled speed, zero external dependencies, and robust testing capabilities.

It eliminates verbose `net/http` boilerplate and brings automatic JSON serialization, retry backoffs, timeouts, multipart file attachments, and a comprehensive **testing mocking engine (`Http.Fake()`)** into a unified API.

---

## 🚀 Key Capabilities

- **Fluent Request Chaining**: `Http.WithToken()`, `Http.WithHeaders()`, `Http.Timeout()`, `Http.Retry()`.
- **Automatic Content Negotiation**: `Http.AsJson()`, `Http.AsForm()`, and `Http.Attach()` for multi-part file uploads.
- **Rich Response Inspection**: `resp.Successful()`, `resp.Created()`, `resp.NotFound()`, `resp.JSON(&dest)`, and `resp.Throw()`.
- **Automated Retries**: Exponential backoff and retry conditions for transient network faults and 5xx server errors.
- **Zero-Network Mocking (`Http.Fake()`)**:
  - Stub by exact URL or wildcard patterns (`https://api.stripe.com/*`).
  - Stub via dynamic callback inspect handlers.
  - FIFO response sequences (`Http.Sequence().Push(200).Push(500)`).
- **Test Assertions**: `Http.AssertSent()`, `Http.AssertSentCount()`, `Http.AssertNotSent()`, and `Http.AssertNothingSent()`.

---

## 🛠️ Making HTTP Requests

### 1. Basic Requests & JSON Deserialization

```go
import "github.com/golaravel/golaravel"

// Simple GET
resp, err := golaravel.Http.Get("https://jsonplaceholder.typicode.com/posts/1")
if err != nil || !resp.Successful() {
    return fmt.Errorf("request failed: %v", err)
}

type Post struct {
    ID    int    `json:"id"`
    Title string `json:"title"`
}

var post Post
_ = resp.JSON(&post)
```

### 2. POST with Authentication & Headers

```go
resp, err := golaravel.Http.
    WithToken("sk_test_12345").
    WithHeader("X-Idempotency-Key", "uuid-v4-abc").
    Post("https://api.stripe.com/v1/charges", map[string]any{
        "amount":   2500,
        "currency": "usd",
    })

if resp.Created() {
    log.Println("Charge created:", resp.String())
}
```

### 3. Automatic Retries & Timeout

Retry failed requests on 5xx errors or network drops:

```go
resp, err := golaravel.Http.
    Timeout(5 * time.Second).
    Retry(3, 100 * time.Millisecond). // Retry up to 3 times with 100ms backoff
    Get("https://api.flaky-service.com/status")

// Throws an error if status code >= 400
if err := resp.Throw(); err != nil {
    return err
}
```

### 4. Multipart File Uploads (`Attach`)

```go
pdfBytes, _ := os.ReadFile("invoice.pdf")

resp, err := golaravel.Http.
    Attach("document", pdfBytes, "invoice.pdf", "application/pdf").
    Post("https://api.cloudstorage.com/v1/files")
```

---

## 🧪 Testing & Mocking (`Http.Fake()`)

When writing tests, use `Http.Fake()` to stub outbound network requests with zero external calls:

### 1. Wildcard URL Pattern Stubbing

```go
func TestPaymentGateway(t *testing.T) {
    defer golaravel.Http.Reset()

    // Stub all Stripe charge endpoints with 200 OK mock response
    golaravel.Http.Fake("https://api.stripe.com/v1/*", golaravel.Http.Response(map[string]any{
        "id":     "ch_101",
        "status": "succeeded",
    }, 200))

    // Execute application code that performs outbound HTTP call
    charge, err := ProcessCustomerPayment(customerID, 50.00)

    // Assertions
    golaravel.Http.AssertSentCount(1)
    golaravel.Http.AssertSent(func(req *glhttp.Request) bool {
        return req.Method() == "POST" && strings.Contains(req.URL(), "/charges")
    })
}
```

### 2. Response Sequences

Simulate endpoints that transition states or intermittently fail:

```go
// First request returns 202 Accepted, second request returns 200 OK Completed
seq := golaravel.Http.Sequence().
    Push(map[string]string{"status": "queued"}, 202).
    Push(map[string]string{"status": "completed"}, 200)

golaravel.Http.Fake("https://api.transcoder.com/jobs/*", seq)

resp1, _ := golaravel.Http.Get("https://api.transcoder.com/jobs/1") // 202 Accepted
resp2, _ := golaravel.Http.Get("https://api.transcoder.com/jobs/1") // 200 OK
```

### 3. Dynamic Callback Mocking

```go
golaravel.Http.Fake(func(req *glhttp.Request) (*glhttp.Response, error) {
    if req.HasHeader("Authorization", "Bearer invalid") {
        return golaravel.Http.Response("Unauthorized", 401), nil
    }
    return golaravel.Http.Response(map[string]string{"user": "Alice"}, 200), nil
})
```

---

## 📋 Response Status Methods

| Method | Status Code | Description |
|---|---|---|
| `resp.Successful()` | 200-299 | True if HTTP status is 2xx |
| `resp.OK()` | 200 | True if status is 200 OK |
| `resp.Created()` | 201 | True if status is 201 Created |
| `resp.Accepted()` | 202 | True if status is 202 Accepted |
| `resp.NoContent()` | 204 | True if status is 204 No Content |
| `resp.Redirect()` | 300-399 | True if HTTP status is 3xx redirect |
| `resp.Failed()` | 400-599 | True if HTTP status is 4xx or 5xx |
| `resp.ClientError()` | 400-499 | True if HTTP status is 4xx |
| `resp.ServerError()` | 500-599 | True if HTTP status is 5xx |
| `resp.NotFound()` | 404 | True if status is 404 Not Found |
| `resp.Unauthorized()` | 401 | True if status is 401 Unauthorized |
| `resp.Forbidden()` | 403 | True if status is 403 Forbidden |
| `resp.Unprocessable()`| 422 | True if status is 422 Unprocessable Entity |
| `resp.Throw()` | ≥ 400 | Returns formatted `*HTTPError` if failed |
