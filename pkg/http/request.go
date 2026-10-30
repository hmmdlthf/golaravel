package glhttp

import (
	"net/http"
	"strings"

	"github.com/goccy/go-json"
)

// Request encapsulates an outbound HTTP request for inspection and assertions.
type Request struct {
	raw  *http.Request
	body []byte
}

// NewRequest creates a Request wrapper.
func NewRequest(raw *http.Request, body []byte) *Request {
	return &Request{
		raw:  raw,
		body: body,
	}
}

// Raw returns the underlying *http.Request.
func (r *Request) Raw() *http.Request {
	return r.raw
}

// Method returns the HTTP method (e.g. GET, POST).
func (r *Request) Method() string {
	if r.raw == nil {
		return ""
	}
	return r.raw.Method
}

// URL returns the full request URL string.
func (r *Request) URL() string {
	if r.raw == nil || r.raw.URL == nil {
		return ""
	}
	return r.raw.URL.String()
}

// Header returns the value of the specified request header.
func (r *Request) Header(key string) string {
	if r.raw == nil || r.raw.Header == nil {
		return ""
	}
	return r.raw.Header.Get(key)
}

// Headers returns the full request headers map.
func (r *Request) Headers() http.Header {
	if r.raw == nil {
		return nil
	}
	return r.raw.Header
}

// HasHeader checks if the header exists, and optionally verifies its value.
func (r *Request) HasHeader(key string, expectedValue ...string) bool {
	val := r.Header(key)
	if val == "" {
		return false
	}
	if len(expectedValue) > 0 && expectedValue[0] != "" {
		return val == expectedValue[0]
	}
	return true
}

// Body returns the request payload bytes.
func (r *Request) Body() []byte {
	return r.body
}

// BodyString returns the request payload as a string.
func (r *Request) BodyString() string {
	return string(r.body)
}

// JSON deserializes the request body into dest.
func (r *Request) JSON(dest any) error {
	return json.Unmarshal(r.body, dest)
}

// IsJson returns true if the request Content-Type indicates JSON.
func (r *Request) IsJson() bool {
	ct := r.Header("Content-Type")
	return strings.Contains(ct, "application/json")
}
