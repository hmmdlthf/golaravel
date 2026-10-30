package golaravel

import (
	stdContext "context"

	"github.com/golaravel/golaravel/pkg/auth"
	"github.com/golaravel/golaravel/pkg/broadcast"
	"github.com/golaravel/golaravel/pkg/cache"
	"github.com/golaravel/golaravel/pkg/config"
	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/events"
	"github.com/golaravel/golaravel/pkg/golaravel"
	"github.com/golaravel/golaravel/pkg/hash"
	"github.com/golaravel/golaravel/pkg/mail"
	"github.com/golaravel/golaravel/pkg/queue"
	"github.com/golaravel/golaravel/pkg/router"
	"github.com/golaravel/golaravel/pkg/schedule"
	"github.com/golaravel/golaravel/pkg/storage"
	"github.com/golaravel/golaravel/pkg/validation"
)

// Re-export core types for top-level framework ergonomics
type (
	Engine      = golaravel.Engine
	Context     = context.Context
	HandlerFunc = context.HandlerFunc
	Route       = router.Route
	RouteGroup  = router.RouteGroup
	Validator   = validation.Validator
	FormRequest = validation.FormRequest
	ErrorBag    = validation.ErrorBag
	Job         = queue.Job
	Message     = mail.Message
)

// New initializes a bare GoLaravel application.
func New() *Engine {
	return golaravel.New()
}

// Default creates a GoLaravel application with production middlewares (Recovery, SecureHeaders, etc.).
func Default() *Engine {
	return golaravel.Default()
}

// BindModel registers an explicit model binder with compile-time type T.
func BindModel[T any](app *Engine, param string, fn func(ctx stdContext.Context, val string) (T, error)) {
	router.BindModel(app.Router, param, fn)
}

// BindFieldModel registers an explicit model binder for a custom field/predicate.
func BindFieldModel[T any](app *Engine, param, field string, fn func(ctx stdContext.Context, val string) (T, error)) {
	router.BindFieldModel(app.Router, param, field, fn)
}

// BindScopedModel registers a child model binder scoped to a parent entity.
func BindScopedModel[TChild any, TParent any](app *Engine, childParam, parentParam string, fn func(ctx stdContext.Context, val string, parent TParent) (TChild, error)) {
	router.BindScopedModel(app.Router, childParam, parentParam, fn)
}

// With wraps a handler that receives one resolved model.
func With[T any](param string, handler func(c *context.Context, model T) error) context.HandlerFunc {
	return router.With(param, handler)
}

// With2 wraps a handler that receives two resolved models (e.g. parent and child).
func With2[T1 any, T2 any](p1, p2 string, handler func(c *context.Context, m1 T1, m2 T2) error) context.HandlerFunc {
	return router.With2(p1, p2, handler)
}

// Facade shortcuts for rapid developer ergonomics
var (
	// Config provides access to environment and application settings
	Config = struct {
		Get         func(key, defaultValue string) string
		GetInt      func(key string, defaultValue int) int
		GetBool     func(key string, defaultValue bool) bool
		LoadEnv     func(filenames ...string) error
	}{
		Get:     config.Get,
		GetInt:  config.GetInt,
		GetBool: config.GetBool,
		LoadEnv: config.LoadEnv,
	}

	// Cache provides access to the in-memory/Redis caching layer
	Cache = struct {
		Get      func(key string) (any, bool)
		GetString func(key string) string
		Put      func(key string, val any, ttl ...any)
		Remember func(key string, ttl ...any)
		Forget   func(key string)
		Flush    func()
	}{
		Get:       cache.Get,
		GetString: cache.GetString,
		Forget:    cache.Forget,
		Flush:     cache.Flush,
	}

	// Queue provides access to background job dispatching
	Queue = struct {
		Push func(job queue.Job) error
	}{
		Push: queue.Push,
	}

	// Events provides access to event dispatching
	Events = struct {
		Listen        func(eventName string, listener events.Listener)
		Dispatch      func(eventName string, payload any) error
		DispatchAsync func(eventName string, payload any)
	}{
		Listen:        events.Listen,
		Dispatch:      events.Dispatch,
		DispatchAsync: events.DispatchAsync,
	}

	// Storage provides access to the filesystem
	Storage = struct {
		Put    func(path string, contents []byte) error
		Get    func(path string) ([]byte, error)
		Exists func(path string) bool
		Delete func(path string) error
	}{
		Put:    storage.Put,
		Get:    storage.Get,
		Exists: storage.Exists,
		Delete: storage.Delete,
	}

	// Mail provides access to email sending
	Mail = struct {
		NewMessage func() *mail.Message
		Send       func(msg *mail.Message) error
	}{
		NewMessage: mail.NewMessage,
		Send:       mail.Send,
	}

	// Hash provides access to password hashing
	Hash = struct {
		Make  func(password string) (string, error)
		Check func(password, hash string) bool
	}{
		Make:  hash.Make,
		Check: hash.Check,
	}

	// Schedule provides access to fluent task scheduling
	Schedule = struct {
		Call func(action func(ctx stdContext.Context) error) *schedule.ScheduledTask
	}{
		Call: schedule.Call,
	}

	// Auth provides access to authentication helpers
	Auth = struct {
		User   func(c *context.Context) auth.Authenticatable
		Claims func(c *context.Context) *auth.JWTClaims
		Define func(ability string, fn auth.GateFunc)
		Allows func(ability string, user auth.Authenticatable, args ...any) bool
	}{
		User:   auth.User,
		Claims: auth.Claims,
		Define: auth.Define,
		Allows: auth.Allows,
	}

	// Broadcast provides access to real-time WebSockets & SSE broadcasting
	Broadcast = struct {
		Broadcast        func(channel, event string, data map[string]any)
		Subscribe        func(channel string) (<-chan *broadcast.Message, func())
		WebSocketHandler func(h ...*broadcast.Hub) context.HandlerFunc
		SSEHandler       func(h ...*broadcast.Hub) context.HandlerFunc
	}{
		Broadcast:        broadcast.Broadcast,
		Subscribe:        broadcast.Subscribe,
		WebSocketHandler: broadcast.WebSocketHandler,
		SSEHandler:       broadcast.SSEHandler,
	}
)
