package context

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

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
	before  []func()
}

// Reset resets the ResponseWriter with a new http.ResponseWriter.
func (rw *ResponseWriter) Reset(w http.ResponseWriter) {
	rw.ResponseWriter = w
	rw.status = http.StatusOK
	rw.size = 0
	rw.written = false
	rw.before = rw.before[:0]
}

// Before registers a function to be executed just before response headers are written.
func (rw *ResponseWriter) Before(fn func()) {
	rw.before = append(rw.before, fn)
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
		for _, fn := range rw.before {
			fn()
		}
		rw.before = nil
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
	index    int

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
	for c.index < len(c.handlers) {
		if err := c.handlers[c.index](c); err != nil {
			return err
		}
		c.index++
	}
	return nil
}

// Abort prevents pending handlers in the chain from being called.
func (c *Context) Abort() {
	c.index = len(c.handlers)
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
	return c.index >= len(c.handlers)
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

// defaultTrustedProxies are the peers whose forwarding headers are believed
// by default: loopback and private networks, i.e. a reverse proxy on the same
// host or inside the same private network.
var defaultTrustedProxies = []string{
	"127.0.0.0/8", "::1/128",
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7",
}

var trustedProxies atomic.Pointer[[]netip.Prefix]

func init() {
	if err := SetTrustedProxies(defaultTrustedProxies...); err != nil {
		panic(err)
	}
}

// SetTrustedProxies replaces the networks (CIDRs or single IPs) whose
// X-Forwarded-For / X-Real-IP headers ClientIP believes. Call it with no
// arguments to ignore forwarding headers entirely. The default is loopback
// and private networks. Add your load balancer's or CDN's ranges when it
// connects from public addresses.
func SetTrustedProxies(cidrs ...string) error {
	prefixes := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if !strings.Contains(c, "/") {
			addr, err := netip.ParseAddr(c)
			if err != nil {
				return fmt.Errorf("context: trusted proxy %q: %w", c, err)
			}
			prefixes = append(prefixes, netip.PrefixFrom(addr.Unmap(), addr.Unmap().BitLen()))
			continue
		}
		p, err := netip.ParsePrefix(c)
		if err != nil {
			return fmt.Errorf("context: trusted proxy %q: %w", c, err)
		}
		prefixes = append(prefixes, p.Masked())
	}
	trustedProxies.Store(&prefixes)
	return nil
}

func isTrustedProxy(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, p := range *trustedProxies.Load() {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

func parseIP(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	if h, _, err := net.SplitHostPort(s); err == nil {
		s = h
	}
	s = strings.Trim(s, "[]")
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

// ClientIP returns the client's IP address. X-Forwarded-For and X-Real-IP
// are only believed when the direct peer (RemoteAddr) is a trusted proxy
// (see SetTrustedProxies); X-Forwarded-For is then read from the right,
// skipping trusted proxies, so a client cannot spoof its address by sending
// the header itself.
func (c *Context) ClientIP() string {
	remote, ok := parseIP(c.Request.RemoteAddr)
	if !ok {
		host := c.Request.RemoteAddr
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		return host
	}
	if !isTrustedProxy(remote) {
		return remote.String()
	}
	if xff := c.Request.Header.Values("X-Forwarded-For"); len(xff) > 0 {
		hops := strings.Split(strings.Join(xff, ","), ",")
		client := remote
		for i := len(hops) - 1; i >= 0; i-- {
			addr, ok := parseIP(hops[i])
			if !ok {
				break
			}
			client = addr
			if !isTrustedProxy(addr) {
				break
			}
		}
		return client.String()
	}
	if xri, ok := parseIP(c.Request.Header.Get("X-Real-IP")); ok {
		return xri.String()
	}
	return remote.String()
}

// Cookie retrieves the named cookie from the HTTP request.
func (c *Context) Cookie(name string) (string, error) {
	cookie, err := c.Request.Cookie(name)
	if err != nil {
		return "", err
	}
	return cookie.Value, nil
}

// SetCookie adds a Set-Cookie header to the HTTP response.
func (c *Context) SetCookie(name, value string, maxAge int, path, domain string, secure, httpOnly bool) {
	if path == "" {
		path = "/"
	}
	http.SetCookie(c.Response.ResponseWriter, &http.Cookie{
		Name:     name,
		Value:    value,
		MaxAge:   maxAge,
		Path:     path,
		Domain:   domain,
		Secure:   secure,
		HttpOnly: httpOnly,
		SameSite: http.SameSiteLaxMode,
	})
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

// Translator defines the interface for localizing messages within context.
type Translator interface {
	Get(key string, replace ...map[string]any) string
	GetForLocale(key string, locale string, replace ...map[string]any) string
	Choice(key string, number int, replace ...map[string]any) string
	ChoiceForLocale(key string, locale string, number int, replace ...map[string]any) string
	GetLocale() string
	IsRTL(locale ...string) bool
	Direction(locale ...string) string
}

var (
	globalTranslatorMu sync.RWMutex
	globalTranslator   Translator
)

// SetTranslator sets the global translator used by context localization helpers.
func SetTranslator(t Translator) {
	globalTranslatorMu.Lock()
	defer globalTranslatorMu.Unlock()
	globalTranslator = t
}

// GetTranslator returns the global translator.
func GetTranslator() Translator {
	globalTranslatorMu.RLock()
	defer globalTranslatorMu.RUnlock()
	return globalTranslator
}

// SetLocale sets the locale string for the current request context.
func (c *Context) SetLocale(locale string) {
	c.Set("__locale", locale)
}

// GetLocale returns the locale string for the current request, or the default locale if unset.
func (c *Context) GetLocale() string {
	if val, ok := c.Get("__locale"); ok {
		if s, ok := val.(string); ok && s != "" {
			return s
		}
	}
	if t := GetTranslator(); t != nil {
		return t.GetLocale()
	}
	return "en"
}

// Trans translates the given message key for the current request's locale.
func (c *Context) Trans(key string, replace ...map[string]any) string {
	t := GetTranslator()
	if t == nil {
		return key
	}
	return t.GetForLocale(key, c.GetLocale(), replace...)
}

// TransChoice translates with pluralization for the current request's locale.
func (c *Context) TransChoice(key string, number int, replace ...map[string]any) string {
	t := GetTranslator()
	if t == nil {
		return key
	}
	return t.ChoiceForLocale(key, c.GetLocale(), number, replace...)
}

// IsRTL reports whether the current request's locale is Right-to-Left (e.g. Arabic, Persian, Urdu, Hebrew).
func (c *Context) IsRTL() bool {
	t := GetTranslator()
	if t == nil {
		return false
	}
	return t.IsRTL(c.GetLocale())
}

// Direction returns "rtl" or "ltr" for the current request's locale.
// Perfect for HTML/Templ layouts: <html lang={ c.GetLocale() } dir={ c.Direction() }>
func (c *Context) Direction() string {
	t := GetTranslator()
	if t == nil {
		return "ltr"
	}
	return t.Direction(c.GetLocale())
}

// FormValue returns the form value for the provided key.
func (c *Context) FormValue(name string) string {
	return c.Request.FormValue(name)
}

// FormFile returns the first file for the provided form key.
func (c *Context) FormFile(name string) (*multipart.FileHeader, error) {
	if c.Request.MultipartForm == nil {
		if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
			return nil, err
		}
	}
	_, fh, err := c.Request.FormFile(name)
	return fh, err
}

// SaveUploadedFile uploads the form file to specific dst.
func (c *Context) SaveUploadedFile(file *multipart.FileHeader, dst string) error {
	src, err := file.Open()
	if err != nil {
		return err
	}
	defer src.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, src)
	return err
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
