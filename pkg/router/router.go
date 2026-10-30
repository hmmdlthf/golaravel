package router

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/golaravel/golaravel/pkg/context"
)

// Route represents a registered route with metadata.
type Route struct {
	Method   string
	Path     string
	Handlers []context.HandlerFunc
	name     string
	router   *Router
}

// Name sets the route name for reverse URL generation (e.g. "users.show").
func (r *Route) Name(name string) *Route {
	r.name = name
	if r.router != nil && name != "" {
		r.router.namedRoutes[name] = r
	}
	return r
}

// GetName returns the route name.
func (r *Route) GetName() string {
	return r.name
}

// Router is an ultra-high performance Radix-tree HTTP router.
type Router struct {
	trees                map[string]*node
	namedRoutes          map[string]*Route
	middlewares          []context.HandlerFunc
	NotFound             context.HandlerFunc
	routesList           []*Route
	modelBinders         map[string]ModelBinder
	fieldBinders         map[string]ModelBinder
	scopedBinders        map[string]ScopedModelBinder
	modelNotFoundHandler context.HandlerFunc
}

// New creates a new Router instance.
func New() *Router {
	return &Router{
		trees:         make(map[string]*node),
		namedRoutes:   make(map[string]*Route),
		modelBinders:  make(map[string]ModelBinder),
		fieldBinders:  make(map[string]ModelBinder),
		scopedBinders: make(map[string]ScopedModelBinder),
		NotFound: func(c *context.Context) error {
			return c.String(http.StatusNotFound, "404 Not Found")
		},
	}
}

// Use adds global middlewares to the router.
func (r *Router) Use(middlewares ...context.HandlerFunc) {
	r.middlewares = append(r.middlewares, middlewares...)
}

// Handle registers a new request handle with the given path and method.
func (r *Router) Handle(method, path string, handlers ...context.HandlerFunc) *Route {
	if len(path) == 0 || path[0] != '/' {
		panic(fmt.Sprintf("path must begin with '/' in path '%s'", path))
	}
	if len(handlers) == 0 {
		panic("there must be at least one handler")
	}

	// Parse route model bindings (e.g. {invoice:token} or {user}) and normalize to :param
	normalizedPath, bindings := parseBindings(path)
	path = normalizedPath

	// Auto-attach model binding interceptor middleware if path contains bindings
	var routeMiddlewares []context.HandlerFunc
	if len(bindings) > 0 {
		routeMiddlewares = append(routeMiddlewares, r.createModelBindingMiddleware(bindings))
	}

	// Combine router middlewares with route middlewares and handlers
	combinedHandlers := make([]context.HandlerFunc, 0, len(r.middlewares)+len(routeMiddlewares)+len(handlers))
	combinedHandlers = append(combinedHandlers, r.middlewares...)
	combinedHandlers = append(combinedHandlers, routeMiddlewares...)
	combinedHandlers = append(combinedHandlers, handlers...)

	root := r.trees[method]
	if root == nil {
		root = new(node)
		r.trees[method] = root
	}
	root.addRoute(path, combinedHandlers)

	route := &Route{
		Method:   method,
		Path:     path,
		Handlers: combinedHandlers,
		router:   r,
	}
	r.routesList = append(r.routesList, route)
	return route
}

// GET registers a new GET route.
func (r *Router) GET(path string, handlers ...context.HandlerFunc) *Route {
	return r.Handle(http.MethodGet, path, handlers...)
}

// POST registers a new POST route.
func (r *Router) POST(path string, handlers ...context.HandlerFunc) *Route {
	return r.Handle(http.MethodPost, path, handlers...)
}

// PUT registers a new PUT route.
func (r *Router) PUT(path string, handlers ...context.HandlerFunc) *Route {
	return r.Handle(http.MethodPut, path, handlers...)
}

// PATCH registers a new PATCH route.
func (r *Router) PATCH(path string, handlers ...context.HandlerFunc) *Route {
	return r.Handle(http.MethodPatch, path, handlers...)
}

// DELETE registers a new DELETE route.
func (r *Router) DELETE(path string, handlers ...context.HandlerFunc) *Route {
	return r.Handle(http.MethodDelete, path, handlers...)
}

// OPTIONS registers a new OPTIONS route.
func (r *Router) OPTIONS(path string, handlers ...context.HandlerFunc) *Route {
	return r.Handle(http.MethodOptions, path, handlers...)
}

// HEAD registers a new HEAD route.
func (r *Router) HEAD(path string, handlers ...context.HandlerFunc) *Route {
	return r.Handle(http.MethodHead, path, handlers...)
}

// Title-case aliases for ergonomic route definition
func (r *Router) Get(path string, handlers ...context.HandlerFunc) *Route     { return r.GET(path, handlers...) }
func (r *Router) Post(path string, handlers ...context.HandlerFunc) *Route    { return r.POST(path, handlers...) }
func (r *Router) Put(path string, handlers ...context.HandlerFunc) *Route     { return r.PUT(path, handlers...) }
func (r *Router) Patch(path string, handlers ...context.HandlerFunc) *Route   { return r.PATCH(path, handlers...) }
func (r *Router) Delete(path string, handlers ...context.HandlerFunc) *Route  { return r.DELETE(path, handlers...) }
func (r *Router) Options(path string, handlers ...context.HandlerFunc) *Route { return r.OPTIONS(path, handlers...) }
func (r *Router) Head(path string, handlers ...context.HandlerFunc) *Route    { return r.HEAD(path, handlers...) }

// Any registers a route for all common HTTP methods.
func (r *Router) Any(path string, handlers ...context.HandlerFunc) {
	methods := []string{
		http.MethodGet, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions, http.MethodHead,
	}
	for _, m := range methods {
		r.Handle(m, path, handlers...)
	}
}

// Group creates a new RouteGroup with shared prefix and middleware.
func (r *Router) Group(prefix string, middlewares ...context.HandlerFunc) *RouteGroup {
	return &RouteGroup{
		router:      r,
		prefix:      prefix,
		middlewares: middlewares,
	}
}

// Match searches for the route matching the HTTP method and path,
// populating parameters directly onto the Context with zero heap allocations.
func (r *Router) Match(method, path string, c *context.Context) []context.HandlerFunc {
	if root := r.trees[method]; root != nil {
		handlers := root.getValue(path, c)
		if handlers != nil {
			return handlers
		}
	}
	// Fallback for HEAD requests to match GET routes
	if method == http.MethodHead {
		if root := r.trees[http.MethodGet]; root != nil {
			handlers := root.getValue(path, c)
			if handlers != nil {
				return handlers
			}
		}
	}
	return nil
}

// URL generates a URL from a named route with parameter substitution.
// Example: router.URL("users.show", "id", "123") -> "/users/123"
func (r *Router) URL(name string, params ...string) (string, error) {
	route, ok := r.namedRoutes[name]
	if !ok {
		return "", fmt.Errorf("route '%s' not found", name)
	}

	if len(params)%2 != 0 {
		return "", fmt.Errorf("params must be key-value pairs")
	}

	path := route.Path
	for i := 0; i < len(params); i += 2 {
		k := ":" + params[i]
		v := params[i+1]
		path = strings.ReplaceAll(path, k, v)
	}
	return path, nil
}

// Routes returns all registered routes.
func (r *Router) Routes() []*Route {
	return r.routesList
}

// normalizePath converts `{id}` syntax to `:id` for high speed Radix tree matching.
func normalizePath(path string) string {
	if !strings.Contains(path, "{") {
		return path
	}
	var sb strings.Builder
	sb.Grow(len(path))
	inBrace := false
	for i := 0; i < len(path); i++ {
		ch := path[i]
		if ch == '{' {
			inBrace = true
			sb.WriteByte(':')
		} else if ch == '}' && inBrace {
			inBrace = false
		} else {
			sb.WriteByte(ch)
		}
	}
	return sb.String()
}
