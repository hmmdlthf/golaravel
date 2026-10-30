package inertia

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"sync"

	"github.com/golaravel/golaravel/pkg/context"
)

// Header constants defined by the Inertia.js protocol.
const (
	HeaderInertia         = "X-Inertia"
	HeaderInertiaVersion  = "X-Inertia-Version"
	HeaderInertiaLocation = "X-Inertia-Location"
)

// Page represents the Inertia page object payload.
type Page struct {
	Component string         `json:"component"`
	Props     map[string]any `json:"props"`
	URL       string         `json:"url"`
	Version   string         `json:"version"`
}

// Engine manages Inertia configuration and shared properties.
type Engine struct {
	version     string
	rootView    func(c *context.Context, page *Page) error
	sharedProps map[string]any
	mu          sync.RWMutex
}

// New creates a new Inertia engine instance.
func New(version string) *Engine {
	e := &Engine{
		version:     version,
		sharedProps: make(map[string]any),
	}
	// Default root HTML renderer
	e.rootView = DefaultRootView
	return e
}

// SetVersion sets the asset version string for cache busting.
func (e *Engine) SetVersion(v string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.version = v
}

// Version returns the current asset version.
func (e *Engine) Version() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.version
}

// Share sets a globally shared prop for all Inertia responses (e.g. auth user, flash messages).
func (e *Engine) Share(key string, value any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sharedProps[key] = value
}

// SetRootView sets a custom root layout renderer.
func (e *Engine) SetRootView(fn func(c *context.Context, page *Page) error) {
	e.rootView = fn
}

// Render produces an Inertia response (either JSON for X-Inertia visits or full HTML for browser first-loads).
func (e *Engine) Render(c *context.Context, status int, component string, props map[string]any) error {
	// Merge shared props with handler props
	e.mu.RLock()
	finalProps := make(map[string]any, len(e.sharedProps)+len(props))
	for k, v := range e.sharedProps {
		finalProps[k] = v
	}
	e.mu.RUnlock()

	for k, v := range props {
		finalProps[k] = v
	}

	page := &Page{
		Component: component,
		Props:     finalProps,
		URL:       c.Request.URL.RequestURI(),
		Version:   e.Version(),
	}

	isInertia := c.Header(HeaderInertia) == "true"

	// Handle version mismatch for GET requests
	if isInertia && c.Request.Method == http.MethodGet {
		clientVersion := c.Header(HeaderInertiaVersion)
		if clientVersion != "" && clientVersion != page.Version {
			c.SetHeader(HeaderInertiaLocation, page.URL)
			return c.NoContent(http.StatusConflict)
		}
	}

	// Inertia JSON visit response
	if isInertia {
		c.SetHeader(HeaderInertia, "true")
		c.SetHeader("Vary", "Accept")
		return c.JSON(status, page)
	}

	// Full-page initial HTML response
	c.Response.WriteHeader(status)
	return e.rootView(c, page)
}

// DefaultRootView renders the standard HTML document housing the Inertia root element.
func DefaultRootView(c *context.Context, page *Page) error {
	pageJSON, err := json.Marshal(page)
	if err != nil {
		return fmt.Errorf("inertia: failed to serialize page object: %w", err)
	}

	escapedJSON := html.EscapeString(string(pageJSON))

	htmlDoc := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>GoLaravel Inertia App</title>
</head>
<body>
    <div id="app" data-page="%s"></div>
</body>
</html>`, escapedJSON)

	return c.HTML(c.Response.Status(), htmlDoc)
}
