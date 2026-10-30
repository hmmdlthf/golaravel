package context

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"

	"github.com/a-h/templ"
	"github.com/goccy/go-json"
	"github.com/golaravel/golaravel/pkg/container"
)

// MaxParams defines the maximum number of route parameters supported without heap allocation.
const MaxParams = 16

// Param represents a single key-value URL parameter.
type Param struct {
	Key   string
	Value string
}

// HandlerFunc defines the signature of a GoLaravel request handler or middleware.
type HandlerFunc func(c *Context) error

// ResponseWriter wraps http.ResponseWriter to track response status and bytes written.
type ResponseWriter struct {
	http.ResponseWriter
	status  int
	size    int64
	written bool
}

// Reset resets the ResponseWriter with a new http.ResponseWriter.
func (rw *ResponseWriter) Reset(w http.ResponseWriter) {
	rw.ResponseWriter = w
	rw.status = http.StatusOK
	rw.size = 0
	rw.written = false
}

// Status returns the HTTP response status code.
func (rw *ResponseWriter) Status() int {
	return rw.status
}

// Size returns the count of bytes written to the client.
func (rw *ResponseWriter) Size() int64 {
	return rw.size
}

// Written reports whether the HTTP headers have been written.
func (rw *ResponseWriter) Written() bool {
	return rw.written
}

// WriteHeader sends the HTTP status code to the client.
func (rw *ResponseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.status = code
		rw.written = true
		rw.ResponseWriter.WriteHeader(code)
	}
}

// Write writes data to the HTTP response connection.
func (rw *ResponseWriter) Write(b []byte) (int, error) {
	if !rw.written {
		rw.WriteHeader(rw.status)
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.size += int64(n)
	return n, err
}

// Context represents the context of the current HTTP request.
// It is recycled via sync.Pool to achieve zero heap allocations on the hot path.
type Context struct {
	Request  *http.Request
	Response ResponseWriter

	// Inline parameter storage to avoid slice heap allocation
	params     [MaxParams]Param
	paramCount int

	// Middleware execution pipeline
	handlers []HandlerFunc
	index    int8

	// Scoped container for request-level DI
	Container *container.Container

	// Custom store for request lifecycle values
	store map[string]any

	// Cached parsed query values
	queryCache url.Values
}

var (
	// BufferPool pools byte slices/buffers for streaming serialization
	bufferPool = sync.Pool{
		New: func() any {
			b := make([]byte, 0, 4096)
			return &b
		},
	}
)

// NewContext creates a new uninitialized Context.
func NewContext() *Context {
	return &Context{
		index: -1,
	}
}

// Reset re-initializes the Context for a new incoming request.
func (c *Context) Reset(w http.ResponseWriter, r *http.Request) {
	c.Request = r
	c.Response.Reset(w)
	c.paramCount = 0
	c.index = -1
	c.handlers = nil
	c.Container = nil
	c.queryCache = nil

	// Clear store map without reallocating
	if c.store != nil {
		for k := range c.store {
			delete(c.store, k)
		}
	}
}

// SetHandlers assigns the middleware and handler execution chain.
func (c *Context) SetHandlers(handlers []HandlerFunc) {
	c.handlers = handlers
}

// Next executes the next handler or middleware in the pipeline.
func (c *Context) Next() error {
	c.index++
	for c.index < int8(len(c.handlers)) {
		if err := c.handlers[c.index](c); err != nil {
			return err
		}
		c.index++
	}
	return nil
}

// Abort prevents pending handlers in the chain from being called.
func (c *Context) Abort() {
	c.index = int8(len(c.handlers))
}

// AbortWithStatus aborts the pipeline and writes the specified HTTP status code.
func (c *Context) AbortWithStatus(code int) {
	c.Abort()
	c.Response.WriteHeader(code)
}

// AbortWithError aborts the pipeline and returns the given error.
func (c *Context) AbortWithError(code int, err error) error {
	c.AbortWithStatus(code)
	return err
}

// IsAborted returns true if the current request has been aborted.
func (c *Context) IsAborted() bool {
	return c.index >= int8(len(c.handlers))
}

// Context returns the standard Go request context.
func (c *Context) Context() context.Context {
	return c.Request.Context()
}

// AddParam adds a key-value parameter into the inline parameter buffer.
func (c *Context) AddParam(key, value string) {
	if c.paramCount < MaxParams {
		c.params[c.paramCount] = Param{Key: key, Value: value}
		c.paramCount++
	}
}

// Param returns the value of a route parameter by key, or empty string.
func (c *Context) Param(key string) string {
	for i := 0; i < c.paramCount; i++ {
		if c.params[i].Key == key {
			return c.params[i].Value
		}
	}
	return ""
}

// Query returns the value of a URL query string parameter.
func (c *Context) Query(key string) string {
	if c.queryCache == nil {
		c.queryCache = c.Request.URL.Query()
	}
	return c.queryCache.Get(key)
}

// DefaultQuery returns the query value or the fallback default if empty.
func (c *Context) DefaultQuery(key, defaultValue string) string {
	val := c.Query(key)
	if val == "" {
		return defaultValue
	}
	return val
}

// Header returns the value of a request header.
func (c *Context) Header(key string) string {
	return c.Request.Header.Get(key)
}

// SetHeader sets a response header.
func (c *Context) SetHeader(key, value string) {
	c.Response.Header().Set(key, value)
}

// Set stores a key-value pair in the request context store.
func (c *Context) Set(key string, val any) {
	if c.store == nil {
		c.store = make(map[string]any, 4)
	}
	c.store[key] = val
}

// Get retrieves a key-value pair from the request context store.
func (c *Context) Get(key string) (any, bool) {
	if c.store == nil {
		return nil, false
	}
	val, ok := c.store[key]
	return val, ok
}

// SetModel stores a resolved route model in the request context.
func (c *Context) SetModel(name string, model any) {
	c.Set("__model:"+name, model)
}

// Model retrieves a resolved route model from the request context.
func (c *Context) Model(name string) any {
	val, _ := c.Get("__model:" + name)
	return val
}

// GetModel retrieves a strongly-typed resolved route model from the request context.
func GetModel[T any](c *Context, name string) (T, bool) {
	val, ok := c.Get("__model:" + name)
	if !ok || val == nil {
		var zero T
		return zero, false
	}
	typed, ok := val.(T)
	return typed, ok
}

// MustGetModel retrieves a strongly-typed resolved route model from the request context or panics.
func MustGetModel[T any](c *Context, name string) T {
	m, ok := GetModel[T](c, name)
	if !ok {
		panic(fmt.Sprintf("context: model [%s] not found in request context", name))
	}
	return m
}

// BindJSON deserializes the JSON request body into the provided struct pointer
// using ultra-fast zero-reflection parsing (goccy/go-json).
func (c *Context) BindJSON(obj any) error {
	if c.Request.Body == nil {
		return fmt.Errorf("empty request body")
	}
	decoder := json.NewDecoder(c.Request.Body)
	return decoder.Decode(obj)
}

// JSON serializes the given object into fast JSON and writes it to the response.
func (c *Context) JSON(status int, v any) error {
	c.SetHeader("Content-Type", "application/json; charset=utf-8")
	c.Response.WriteHeader(status)

	// Stream directly to response writer using high-performance goccy/go-json encoder
	return json.NewEncoder(&c.Response).Encode(v)
}

// InertiaRenderer defines an interface for rendering Inertia.js responses.
type InertiaRenderer interface {
	Render(c *Context, status int, component string, props map[string]any) error
}

var globalInertiaRenderer InertiaRenderer

// SetInertiaRenderer sets the global Inertia adapter renderer.
func SetInertiaRenderer(r InertiaRenderer) {
	globalInertiaRenderer = r
}

// Inertia renders an Inertia.js component with props.
func (c *Context) Inertia(status int, component string, props map[string]any) error {
	if globalInertiaRenderer == nil {
		return fmt.Errorf("inertia renderer not configured. Call inertia.New() and register it")
	}
	return globalInertiaRenderer.Render(c, status, component, props)
}

// Templ renders a templ.Component directly to the HTTP response.
// templ compiles templates into pure Go code, achieving zero-reflection and zero-allocation streaming.
func (c *Context) Templ(status int, cmp templ.Component) error {
	c.SetHeader("Content-Type", "text/html; charset=utf-8")
	c.Response.WriteHeader(status)
	return cmp.Render(c.Request.Context(), &c.Response)
}

// String writes a plain text string to the HTTP response.
func (c *Context) String(status int, format string, values ...any) error {
	c.SetHeader("Content-Type", "text/plain; charset=utf-8")
	c.Response.WriteHeader(status)
	if len(values) > 0 {
		_, err := fmt.Fprintf(&c.Response, format, values...)
		return err
	}
	_, err := io.WriteString(&c.Response, format)
	return err
}

// HTML writes raw HTML string to the HTTP response.
func (c *Context) HTML(status int, html string) error {
	c.SetHeader("Content-Type", "text/html; charset=utf-8")
	c.Response.WriteHeader(status)
	_, err := io.WriteString(&c.Response, html)
	return err
}

// Bytes writes raw byte data to the HTTP response with the specified content type.
func (c *Context) Bytes(status int, contentType string, data []byte) error {
	c.SetHeader("Content-Type", contentType)
	c.Response.WriteHeader(status)
	_, err := c.Response.Write(data)
	return err
}

// NoContent sends an HTTP response with no body.
func (c *Context) NoContent(status int) error {
	c.Response.WriteHeader(status)
	return nil
}

// Redirect sends an HTTP redirect status.
func (c *Context) Redirect(status int, location string) error {
	c.SetHeader("Location", location)
	c.Response.WriteHeader(status)
	return nil
}
