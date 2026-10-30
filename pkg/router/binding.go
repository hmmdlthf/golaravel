package router

import (
	stdctx "context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/golaravel/golaravel/pkg/context"
)

// ErrModelNotFound is returned when a route model binder cannot locate the requested entity.
var ErrModelNotFound = errors.New("model not found")

// ModelBinder resolves an entity given the parameter value and request context.
type ModelBinder func(ctx stdctx.Context, paramValue string) (any, error)

// ScopedModelBinder resolves a child entity scoped to its parent entity.
type ScopedModelBinder func(ctx stdctx.Context, paramValue string, parent any) (any, error)

// paramBinding stores binding metadata extracted from a route path (e.g. {invoice:token}).
type paramBinding struct {
	ParamName   string
	Field       string
	ParentParam string
}

// Bind registers an explicit model binder for a given route parameter (e.g. "user", "invoice").
func (r *Router) Bind(param string, binder ModelBinder) {
	if r.modelBinders == nil {
		r.modelBinders = make(map[string]ModelBinder)
	}
	r.modelBinders[param] = binder
}

// BindField registers an explicit model binder for a parameter with a custom key (e.g. "invoice", "token").
func (r *Router) BindField(param string, field string, binder ModelBinder) {
	if r.fieldBinders == nil {
		r.fieldBinders = make(map[string]ModelBinder)
	}
	key := param + ":" + field
	r.fieldBinders[key] = binder
}

// BindScoped registers a child model binder scoped to a parent model.
func (r *Router) BindScoped(childParam, parentParam string, binder ScopedModelBinder) {
	if r.scopedBinders == nil {
		r.scopedBinders = make(map[string]ScopedModelBinder)
	}
	key := parentParam + "->" + childParam
	r.scopedBinders[key] = binder
}

// SetModelNotFoundHandler customizes the response when a bound model is not found.
func (r *Router) SetModelNotFoundHandler(handler context.HandlerFunc) {
	r.modelNotFoundHandler = handler
}

// BindModel registers a strongly-typed model binder using Go generics.
func BindModel[T any](r *Router, param string, fn func(ctx stdctx.Context, val string) (T, error)) {
	r.Bind(param, func(ctx stdctx.Context, val string) (any, error) {
		return fn(ctx, val)
	})
}

// BindFieldModel registers a strongly-typed model binder for a custom field/predicate using Go generics.
func BindFieldModel[T any](r *Router, param, field string, fn func(ctx stdctx.Context, val string) (T, error)) {
	r.BindField(param, field, func(ctx stdctx.Context, val string) (any, error) {
		return fn(ctx, val)
	})
}

// BindScopedModel registers a strongly-typed child model binder scoped to a parent entity.
func BindScopedModel[TChild any, TParent any](r *Router, childParam, parentParam string, fn func(ctx stdctx.Context, val string, parent TParent) (TChild, error)) {
	r.BindScoped(childParam, parentParam, func(ctx stdctx.Context, val string, parent any) (any, error) {
		p, ok := parent.(TParent)
		if !ok {
			return nil, fmt.Errorf("expected parent type %T, got %T", *new(TParent), parent)
		}
		return fn(ctx, val, p)
	})
}

// With creates a type-safe handler receiving one resolved model.
func With[T any](param string, handler func(c *context.Context, model T) error) context.HandlerFunc {
	return func(c *context.Context) error {
		m, ok := context.GetModel[T](c, param)
		if !ok {
			return c.String(http.StatusNotFound, "Resource not found for parameter [%s]", param)
		}
		return handler(c, m)
	}
}

// With2 creates a type-safe handler receiving two resolved models (e.g. parent and child).
func With2[T1 any, T2 any](p1, p2 string, handler func(c *context.Context, m1 T1, m2 T2) error) context.HandlerFunc {
	return func(c *context.Context) error {
		m1, ok1 := context.GetModel[T1](c, p1)
		m2, ok2 := context.GetModel[T2](c, p2)
		if !ok1 || !ok2 {
			return c.String(http.StatusNotFound, "Resource not found")
		}
		return handler(c, m1, m2)
	}
}

// With3 creates a type-safe handler receiving three resolved models.
func With3[T1 any, T2 any, T3 any](p1, p2, p3 string, handler func(c *context.Context, m1 T1, m2 T2, m3 T3) error) context.HandlerFunc {
	return func(c *context.Context) error {
		m1, ok1 := context.GetModel[T1](c, p1)
		m2, ok2 := context.GetModel[T2](c, p2)
		m3, ok3 := context.GetModel[T3](c, p3)
		if !ok1 || !ok2 || !ok3 {
			return c.String(http.StatusNotFound, "Resource not found")
		}
		return handler(c, m1, m2, m3)
	}
}

// parseBindings parses route path segments to detect parameter bindings (e.g. {invoice:token} or :user).
func parseBindings(rawPath string) (normalizedPath string, bindings []paramBinding) {
	segments := strings.Split(rawPath, "/")
	var lastParam string

	for i, seg := range segments {
		if seg == "" {
			continue
		}

		// Handle {param} and {param:field}
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			inner := seg[1 : len(seg)-1]
			var paramName, field string
			if parts := strings.SplitN(inner, ":", 2); len(parts) == 2 {
				paramName = parts[0]
				field = parts[1]
			} else {
				paramName = inner
			}

			bindings = append(bindings, paramBinding{
				ParamName:   paramName,
				Field:       field,
				ParentParam: lastParam,
			})
			lastParam = paramName
			segments[i] = ":" + paramName
		} else if strings.HasPrefix(seg, ":") {
			inner := seg[1:]
			var paramName, field string
			if parts := strings.SplitN(inner, ":", 2); len(parts) == 2 {
				paramName = parts[0]
				field = parts[1]
			} else {
				paramName = inner
			}

			bindings = append(bindings, paramBinding{
				ParamName:   paramName,
				Field:       field,
				ParentParam: lastParam,
			})
			lastParam = paramName
			segments[i] = ":" + paramName
		}
	}

	return strings.Join(segments, "/"), bindings
}

// createModelBindingMiddleware generates an interceptor for a route if bindings are present.
func (r *Router) createModelBindingMiddleware(bindings []paramBinding) context.HandlerFunc {
	return func(c *context.Context) error {
		for _, b := range bindings {
			val := c.Param(b.ParamName)
			if val == "" {
				continue
			}

			var model any
			var err error

			// Check scoped binder if parent is present
			if b.ParentParam != "" && r.scopedBinders != nil {
				scopedKey := b.ParentParam + "->" + b.ParamName
				if sb, exists := r.scopedBinders[scopedKey]; exists {
					parentModel := c.Model(b.ParentParam)
					if parentModel != nil {
						model, err = sb(c.Request.Context(), val, parentModel)
						if err != nil || model == nil {
							return r.handleModelNotFound(c, b.ParamName, val, err)
						}
						c.SetModel(b.ParamName, model)
						continue
					}
				}
			}

			// Check custom field binder (e.g. invoice:token)
			if b.Field != "" && r.fieldBinders != nil {
				fieldKey := b.ParamName + ":" + b.Field
				if fb, exists := r.fieldBinders[fieldKey]; exists {
					model, err = fb(c.Request.Context(), val)
					if err != nil || model == nil {
						return r.handleModelNotFound(c, b.ParamName, val, err)
					}
					c.SetModel(b.ParamName, model)
					continue
				}
			}

			// Fallback to standard parameter binder
			if r.modelBinders != nil {
				if mb, exists := r.modelBinders[b.ParamName]; exists {
					model, err = mb(c.Request.Context(), val)
					if err != nil || model == nil {
						return r.handleModelNotFound(c, b.ParamName, val, err)
					}
					c.SetModel(b.ParamName, model)
					continue
				}
			}
		}

		return c.Next()
	}
}

func (r *Router) handleModelNotFound(c *context.Context, param, val string, err error) error {
	c.Abort()

	if r.modelNotFoundHandler != nil {
		return r.modelNotFoundHandler(c)
	}

	// Default intelligent 404 response
	if strings.Contains(c.Request.Header.Get("Accept"), "application/json") || strings.HasPrefix(c.Request.URL.Path, "/api/") {
		return c.JSON(http.StatusNotFound, map[string]any{
			"message": fmt.Sprintf("No query results for model [%s] with key [%s]", param, val),
			"status":  http.StatusNotFound,
		})
	}

	return c.String(http.StatusNotFound, "404 Not Found: [%s] '%s' does not exist", param, val)
}
