package glhttp

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/goccy/go-json"
)

// Response wraps an HTTP response with convenient Laravel-style inspection methods.
type Response struct {
	raw      *http.Response
	body     []byte
	duration time.Duration
	request  *Request
}

// NewResponse creates a new Response wrapper.
func NewResponse(raw *http.Response, body []byte, duration time.Duration, req *Request) *Response {
	return &Response{
		raw:      raw,
		body:     body,
		duration: duration,
		request:  req,
	}
}

// Raw returns the underlying *http.Response.
func (r *Response) Raw() *http.Response {
	return r.raw
}

// Request returns the Request that generated this response.
func (r *Response) Request() *Request {
	return r.request
}

// StatusCode returns the HTTP response status code.
func (r *Response) StatusCode() int {
	if r.raw == nil {
		return 0
	}
	return r.raw.StatusCode
}

// Status returns the HTTP status text (e.g. "200 OK").
func (r *Response) Status() string {
	if r.raw == nil {
		return ""
	}
	return r.raw.Status
}

// Body returns the response body as raw bytes.
func (r *Response) Body() []byte {
	return r.body
}

// String returns the response body as a string.
func (r *Response) String() string {
	return string(r.body)
}

// JSON deserializes the response body into the given destination struct or map.
func (r *Response) JSON(dest any) error {
	return json.Unmarshal(r.body, dest)
}

// Header returns the value of the specified response header.
func (r *Response) Header(key string) string {
	if r.raw == nil || r.raw.Header == nil {
		return ""
	}
	return r.raw.Header.Get(key)
}

// Headers returns the full response headers map.
func (r *Response) Headers() http.Header {
	if r.raw == nil {
		return nil
	}
	return r.raw.Header
}

// Cookies returns all cookies sent by the response.
func (r *Response) Cookies() []*http.Cookie {
	if r.raw == nil {
		return nil
	}
	return r.raw.Cookies()
}

// Cookie retrieves a cookie by name, or nil if not present.
func (r *Response) Cookie(name string) *http.Cookie {
	for _, c := range r.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// Duration returns the total roundtrip duration for this request.
func (r *Response) Duration() time.Duration {
	return r.duration
}

// --- Status Predicate Helpers ---

// Successful returns true if the status code is 2xx.
func (r *Response) Successful() bool {
	code := r.StatusCode()
	return code >= 200 && code < 300
}

// OK returns true if the status code is 200 OK.
func (r *Response) OK() bool {
	return r.StatusCode() == http.StatusOK
}

// Created returns true if the status code is 201 Created.
func (r *Response) Created() bool {
	return r.StatusCode() == http.StatusCreated
}

// Accepted returns true if the status code is 202 Accepted.
func (r *Response) Accepted() bool {
	return r.StatusCode() == http.StatusAccepted
}

// NoContent returns true if the status code is 204 No Content.
func (r *Response) NoContent() bool {
	return r.StatusCode() == http.StatusNoContent
}

// Redirect returns true if the status code is a 3xx redirect.
func (r *Response) Redirect() bool {
	code := r.StatusCode()
	return code >= 300 && code < 400
}

// Failed returns true if the status code is 4xx or 5xx.
func (r *Response) Failed() bool {
	code := r.StatusCode()
	return code >= 400
}

// ClientError returns true if the status code is 4xx.
func (r *Response) ClientError() bool {
	code := r.StatusCode()
	return code >= 400 && code < 500
}

// ServerError returns true if the status code is 5xx.
func (r *Response) ServerError() bool {
	code := r.StatusCode()
	return code >= 500
}

// Unauthorized returns true if the status code is 401 Unauthorized.
func (r *Response) Unauthorized() bool {
	return r.StatusCode() == http.StatusUnauthorized
}

// Forbidden returns true if the status code is 403 Forbidden.
func (r *Response) Forbidden() bool {
	return r.StatusCode() == http.StatusForbidden
}

// NotFound returns true if the status code is 404 Not Found.
func (r *Response) NotFound() bool {
	return r.StatusCode() == http.StatusNotFound
}

// Unprocessable returns true if the status code is 422 Unprocessable Entity.
func (r *Response) Unprocessable() bool {
	return r.StatusCode() == http.StatusUnprocessableEntity
}

// --- Error Handling & Throw ---

// HTTPError represents an error returned when Throw() is called on a failed response.
type HTTPError struct {
	StatusCode int
	Status     string
	Body       string
	Response   *Response
}

func (e *HTTPError) Error() string {
	bodySnippet := e.Body
	if len(bodySnippet) > 200 {
		bodySnippet = bodySnippet[:200] + "..."
	}
	bodySnippet = strings.TrimSpace(bodySnippet)
	return fmt.Sprintf("HTTP request failed with status %d (%s): %s", e.StatusCode, e.Status, bodySnippet)
}

// Throw returns an HTTPError if the response was not successful (>= 400), or nil otherwise.
func (r *Response) Throw() error {
	if r.Failed() {
		return &HTTPError{
			StatusCode: r.StatusCode(),
			Status:     r.Status(),
			Body:       r.String(),
			Response:   r,
		}
	}
	return nil
}

// OnError executes the provided callback if the request returned a client or server error (>= 400).
func (r *Response) OnError(callback func(resp *Response)) *Response {
	if r.Failed() && callback != nil {
		callback(r)
	}
	return r
}
