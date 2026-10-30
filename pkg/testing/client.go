package gltest

import (
	"bytes"
	"io"
	"net/http"
	"testing"
)

// TestClient provides a high-level test harness executing requests directly
// against any http.Handler (including GoLaravel Engine) with zero network overhead.
type TestClient struct {
	t              testing.TB
	handler        http.Handler
	defaultHeaders http.Header
	defaultCookies []*http.Cookie
}

// New creates a new TestClient instance.
func New(t testing.TB, handler http.Handler) *TestClient {
	return &TestClient{
		t:              t,
		handler:        handler,
		defaultHeaders: make(http.Header),
	}
}

// WithHeader configures a default header applied to all requests from this client.
func (c *TestClient) WithHeader(key, value string) *TestClient {
	c.defaultHeaders.Set(key, value)
	return c
}

// WithToken sets a default Bearer Authorization token for all requests from this client.
func (c *TestClient) WithToken(token string) *TestClient {
	return c.WithHeader("Authorization", "Bearer "+token)
}

// WithCookie sets a default cookie for all requests from this client.
func (c *TestClient) WithCookie(name, value string) *TestClient {
	c.defaultCookies = append(c.defaultCookies, &http.Cookie{
		Name:  name,
		Value: value,
	})
	return c
}

// Request starts building a custom request with the given method and path.
func (c *TestClient) Request(method, path string) *TestRequestBuilder {
	b := NewRequestBuilder(c, method, path)

	// Copy default headers
	for k, vals := range c.defaultHeaders {
		for _, v := range vals {
			b.headers.Add(k, v)
		}
	}

	// Copy default cookies
	b.cookies = append(b.cookies, c.defaultCookies...)
	return b
}

// Get executes a GET request.
func (c *TestClient) Get(path string) *TestResponse {
	return c.Request(http.MethodGet, path).Send()
}

// Post executes a POST request with optional body payload.
// If body[0] is provided:
// - If string or []byte, it is treated as raw body
// - Otherwise, it is automatically serialized to JSON.
func (c *TestClient) Post(path string, body ...any) *TestResponse {
	req := c.Request(http.MethodPost, path)
	if len(body) > 0 && body[0] != nil {
		applyBody(req, body[0])
	}
	return req.Send()
}

// PostJSON executes a POST request with a JSON payload.
func (c *TestClient) PostJSON(path string, data any) *TestResponse {
	return c.Request(http.MethodPost, path).WithJSON(data).Send()
}

// PostForm executes a POST request with URL-encoded form data.
func (c *TestClient) PostForm(path string, data map[string]string) *TestResponse {
	return c.Request(http.MethodPost, path).WithForm(data).Send()
}

// Put executes a PUT request with optional body payload.
func (c *TestClient) Put(path string, body ...any) *TestResponse {
	req := c.Request(http.MethodPut, path)
	if len(body) > 0 && body[0] != nil {
		applyBody(req, body[0])
	}
	return req.Send()
}

// PutJSON executes a PUT request with a JSON payload.
func (c *TestClient) PutJSON(path string, data any) *TestResponse {
	return c.Request(http.MethodPut, path).WithJSON(data).Send()
}

// Patch executes a PATCH request with optional body payload.
func (c *TestClient) Patch(path string, body ...any) *TestResponse {
	req := c.Request(http.MethodPatch, path)
	if len(body) > 0 && body[0] != nil {
		applyBody(req, body[0])
	}
	return req.Send()
}

// Delete executes a DELETE request with optional body payload.
func (c *TestClient) Delete(path string, body ...any) *TestResponse {
	req := c.Request(http.MethodDelete, path)
	if len(body) > 0 && body[0] != nil {
		applyBody(req, body[0])
	}
	return req.Send()
}

// Head executes a HEAD request.
func (c *TestClient) Head(path string) *TestResponse {
	return c.Request(http.MethodHead, path).Send()
}

// Options executes an OPTIONS request.
func (c *TestClient) Options(path string) *TestResponse {
	return c.Request(http.MethodOptions, path).Send()
}

func applyBody(b *TestRequestBuilder, body any) {
	switch v := body.(type) {
	case string:
		b.body = bytes.NewReader([]byte(v))
	case []byte:
		b.body = bytes.NewReader(v)
	case io.Reader:
		b.body = v
	default:
		b.WithJSON(v)
	}
}
