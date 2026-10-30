package router

import (
	"path"
	"strings"

	"github.com/golaravel/golaravel/pkg/context"
)

// RouteRegistrar defines the common interface for registering routes on a Router or RouteGroup.
type RouteRegistrar interface {
	Group(prefix string, middlewares ...context.HandlerFunc) *RouteGroup
	GET(path string, handlers ...context.HandlerFunc) *Route
	POST(path string, handlers ...context.HandlerFunc) *Route
	PUT(path string, handlers ...context.HandlerFunc) *Route
	PATCH(path string, handlers ...context.HandlerFunc) *Route
	DELETE(path string, handlers ...context.HandlerFunc) *Route

	Get(path string, handlers ...context.HandlerFunc) *Route
	Post(path string, handlers ...context.HandlerFunc) *Route
	Put(path string, handlers ...context.HandlerFunc) *Route
	Patch(path string, handlers ...context.HandlerFunc) *Route
	Delete(path string, handlers ...context.HandlerFunc) *Route
}

// RouteGroup represents a collection of routes sharing a path prefix and middleware chain.
type RouteGroup struct {
	router      *Router
	prefix      string
	middlewares []context.HandlerFunc
}

// Use appends middlewares to the group.
func (rg *RouteGroup) Use(middlewares ...context.HandlerFunc) {
	rg.middlewares = append(rg.middlewares, middlewares...)
}

// Group creates a nested route group.
func (rg *RouteGroup) Group(subPrefix string, middlewares ...context.HandlerFunc) *RouteGroup {
	prefix := rg.calculatePath(subPrefix)
	combined := make([]context.HandlerFunc, 0, len(rg.middlewares)+len(middlewares))
	combined = append(combined, rg.middlewares...)
	combined = append(combined, middlewares...)

	return &RouteGroup{
		router:      rg.router,
		prefix:      prefix,
		middlewares: combined,
	}
}

// Handle registers a route inside this group.
func (rg *RouteGroup) Handle(method, relativePath string, handlers ...context.HandlerFunc) *Route {
	fullPath := rg.calculatePath(relativePath)
	combined := make([]context.HandlerFunc, 0, len(rg.middlewares)+len(handlers))
	combined = append(combined, rg.middlewares...)
	combined = append(combined, handlers...)

	return rg.router.Handle(method, fullPath, combined...)
}

// GET registers a GET route within this group.
func (rg *RouteGroup) GET(path string, handlers ...context.HandlerFunc) *Route {
	return rg.Handle("GET", path, handlers...)
}

// POST registers a POST route within this group.
func (rg *RouteGroup) POST(path string, handlers ...context.HandlerFunc) *Route {
	return rg.Handle("POST", path, handlers...)
}

// PUT registers a PUT route within this group.
func (rg *RouteGroup) PUT(path string, handlers ...context.HandlerFunc) *Route {
	return rg.Handle("PUT", path, handlers...)
}

// PATCH registers a PATCH route within this group.
func (rg *RouteGroup) PATCH(path string, handlers ...context.HandlerFunc) *Route {
	return rg.Handle("PATCH", path, handlers...)
}

// DELETE registers a DELETE route within this group.
func (rg *RouteGroup) DELETE(path string, handlers ...context.HandlerFunc) *Route {
	return rg.Handle("DELETE", path, handlers...)
}

// OPTIONS registers an OPTIONS route within this group.
func (rg *RouteGroup) OPTIONS(path string, handlers ...context.HandlerFunc) *Route {
	return rg.Handle("OPTIONS", path, handlers...)
}

func (rg *RouteGroup) Get(path string, handlers ...context.HandlerFunc) *Route    { return rg.GET(path, handlers...) }
func (rg *RouteGroup) Post(path string, handlers ...context.HandlerFunc) *Route   { return rg.POST(path, handlers...) }
func (rg *RouteGroup) Put(path string, handlers ...context.HandlerFunc) *Route    { return rg.PUT(path, handlers...) }
func (rg *RouteGroup) Patch(path string, handlers ...context.HandlerFunc) *Route  { return rg.PATCH(path, handlers...) }
func (rg *RouteGroup) Delete(path string, handlers ...context.HandlerFunc) *Route { return rg.DELETE(path, handlers...) }

func (rg *RouteGroup) calculatePath(relativePath string) string {
	if relativePath == "" {
		return rg.prefix
	}
	final := path.Join(rg.prefix, relativePath)
	// Preserve trailing slash if original relativePath had one
	if strings.HasSuffix(relativePath, "/") && !strings.HasSuffix(final, "/") {
		final += "/"
	}
	return final
}
