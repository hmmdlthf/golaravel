package golaravel

import (
	stdContext "context"
	"time"

	"github.com/golaravel/golaravel/pkg/auth"
	"github.com/golaravel/golaravel/pkg/broadcast"
	"github.com/golaravel/golaravel/pkg/cache"
	"github.com/golaravel/golaravel/pkg/config"
	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/events"
	"github.com/golaravel/golaravel/pkg/golaravel"
	"github.com/golaravel/golaravel/pkg/hash"
	"github.com/golaravel/golaravel/pkg/mail"
	"github.com/golaravel/golaravel/pkg/notification"
	"github.com/golaravel/golaravel/pkg/privacy"
	"github.com/golaravel/golaravel/pkg/queue"
	"github.com/golaravel/golaravel/pkg/router"
	"github.com/golaravel/golaravel/pkg/schedule"
	"github.com/golaravel/golaravel/pkg/session"
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
	ErrorBag        = validation.ErrorBag
	Job             = queue.Job
	Message         = mail.Message
	SoftDeleteMixin = privacy.SoftDeleteMixin
	TenantMixin     = privacy.TenantMixin
	SessionState    = session.Session
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

	// Cache provides access to the in-memory/Redis caching layer, tagged cache, and atomic locks
	Cache = struct {
		Get             func(key string) (any, bool)
		GetString       func(key string, defaultVal ...string) string
		GetInt          func(key string, defaultVal ...int) int
		GetBool         func(key string, defaultVal ...bool) bool
		Put             func(key string, val any, ttl time.Duration) error
		Add             func(key string, val any, ttl time.Duration) (bool, error)
		Increment       func(key string, value ...int64) (int64, error)
		Decrement       func(key string, value ...int64) (int64, error)
		Remember        func(key string, ttl time.Duration, fn func() (any, error)) (any, error)
		RememberForever func(key string, fn func() (any, error)) (any, error)
		Pull            func(key string, defaultVal ...any) any
		Forget          func(key string) error
		Flush           func() error
		Tags            func(names ...string) *cache.TaggedStore
		Lock            func(name string, duration time.Duration, owner ...string) *cache.AtomicLock
	}{
		Get:             cache.Get,
		GetString:       cache.GetString,
		GetInt:          cache.GetInt,
		GetBool:         cache.GetBool,
		Put:             cache.Put,
		Add:             cache.Add,
		Increment:       cache.Increment,
		Decrement:       cache.Decrement,
		Remember:        cache.Remember,
		RememberForever: cache.RememberForever,
		Pull:            cache.Pull,
		Forget:          cache.Forget,
		Flush:           cache.Flush,
		Tags:            cache.Tags,
		Lock:            cache.Lock,
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
		User       func(c *context.Context) auth.Authenticatable
		Claims     func(c *context.Context) *auth.JWTClaims
		Define     func(ability string, fn auth.GateFunc)
		Allows     func(ability string, user auth.Authenticatable, args ...any) bool
		TOTP       func(opts ...auth.TOTPOptions) *auth.TOTPManager
		MultiGuard func(guards ...auth.GuardFunc) context.HandlerFunc
	}{
		User:       auth.User,
		Claims:     auth.Claims,
		Define:     auth.Define,
		Allows:     auth.Allows,
		TOTP:       auth.NewTOTPManager,
		MultiGuard: auth.MultiGuard,
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

	// Notification provides access to multi-channel notifications (mail, database, broadcast, slack, sms)
	Notification = struct {
		Send       func(ctx stdContext.Context, notifiables []notification.Notifiable, n notification.Notification) error
		SendOne    func(ctx stdContext.Context, notifiable notification.Notifiable, n notification.Notification) error
		SendNow    func(ctx stdContext.Context, notifiables []notification.Notifiable, n notification.Notification) error
		SendNowOne func(ctx stdContext.Context, notifiable notification.Notifiable, n notification.Notification) error
		OnDemand   func(channel, destination string) *notification.AnonymousNotifiable
	}{
		Send:       notification.Send,
		SendOne:    notification.SendOne,
		SendNow:    notification.SendNow,
		SendNowOne: notification.SendNowOne,
		OnDemand:   notification.OnDemand,
	}

	// Privacy provides access to multi-tenancy isolation and soft delete scopes
	Privacy = struct {
		WithTenantID     func(ctx stdContext.Context, tenantID string) stdContext.Context
		TenantFromContext func(ctx stdContext.Context) (string, bool)
		MustTenantFromContext func(ctx stdContext.Context) string
		WithBypassTenancy func(ctx stdContext.Context) stdContext.Context
		WithTrashed      func(ctx stdContext.Context) stdContext.Context
		OnlyTrashed      func(ctx stdContext.Context) stdContext.Context
		WithForceDelete  func(ctx stdContext.Context) stdContext.Context
		TenantMiddleware func(resolvers ...privacy.TenantResolver) context.HandlerFunc
	}{
		WithTenantID:          privacy.WithTenantID,
		TenantFromContext:     privacy.TenantFromContext,
		MustTenantFromContext: privacy.MustTenantFromContext,
		WithBypassTenancy:     privacy.WithBypassTenancy,
		WithTrashed:           privacy.WithTrashed,
		OnlyTrashed:           privacy.OnlyTrashed,
		WithForceDelete:       privacy.WithForceDelete,
		TenantMiddleware:      privacy.TenantMiddleware,
	}

	// Session provides access to stateful sessions, flash messages, and CSRF protection
	Session = struct {
		Get          func(c *context.Context, key string, defaultVal ...any) any
		GetString    func(c *context.Context, key string, defaultVal ...string) string
		Put          func(c *context.Context, key string, value any)
		Flash        func(c *context.Context, key string, value any)
		Has          func(c *context.Context, key string) bool
		Token        func(c *context.Context) string
		FromContext  func(c *context.Context) *session.Session
		StartSession func(manager ...*session.Manager) context.HandlerFunc
		VerifyCSRF   func() context.HandlerFunc
	}{
		Get:          session.Get,
		GetString:    session.GetString,
		Put:          session.Put,
		Flash:        session.Flash,
		Has:          session.Has,
		Token:        session.Token,
		FromContext:  session.FromContext,
		StartSession: session.StartSession,
		VerifyCSRF:   session.VerifyCSRFToken,
	}
)
