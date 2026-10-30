package golaravel

import (
	stdContext "context"
	"io"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"github.com/golaravel/golaravel/pkg/auth"
	"github.com/golaravel/golaravel/pkg/auth/oauth"
	"github.com/golaravel/golaravel/pkg/broadcast"
	"github.com/golaravel/golaravel/pkg/cache"
	"github.com/golaravel/golaravel/pkg/config"
	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/events"
	"github.com/golaravel/golaravel/pkg/golaravel"
	"github.com/golaravel/golaravel/pkg/hash"
	glhttp "github.com/golaravel/golaravel/pkg/http"
	"github.com/golaravel/golaravel/pkg/mail"
	"github.com/golaravel/golaravel/pkg/notification"
	"github.com/golaravel/golaravel/pkg/privacy"
	"github.com/golaravel/golaravel/pkg/queue"
	"github.com/golaravel/golaravel/pkg/router"
	"github.com/golaravel/golaravel/pkg/schedule"
	"github.com/golaravel/golaravel/pkg/session"
	"github.com/golaravel/golaravel/pkg/storage"
	gltest "github.com/golaravel/golaravel/pkg/testing"
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
	StorageDriver   = storage.Driver
	TestClient      = gltest.TestClient
	TestResponse    = gltest.TestResponse
	TestRequest     = gltest.TestRequestBuilder
	HTTPResponse    = glhttp.Response
	HTTPRequest     = glhttp.Request
	HTTPClient      = glhttp.Client
)

// Test creates a high-performance HTTP test harness client directly against any http.Handler or GoLaravel Engine.
func Test(t testing.TB, app http.Handler) *gltest.TestClient {
	return gltest.New(t, app)
}

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

	// Queue provides access to background job dispatching, priority queues, and dashboard
	Queue = struct {
		Push           func(job queue.Job) error
		Later          func(delay time.Duration, job queue.Job) error
		OnQueue        func(name string) *queue.PendingDispatch
		RegisterJob    func(job queue.Job)
		StartWorkers   func(ctx stdContext.Context, concurrency int, queues ...string)
		Stop           func()
		Default        func() *queue.Queue
		MountDashboard func(r router.RouteRegistrar, prefix ...string)
	}{
		Push:           queue.Push,
		Later:          queue.Later,
		OnQueue:        queue.OnQueue,
		RegisterJob:    queue.RegisterJobType,
		StartWorkers:   queue.StartWorkers,
		Stop:           queue.Stop,
		Default:        queue.DefaultQueue,
		MountDashboard: queue.MountDashboard,
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

	// Storage provides access to the multi-disk cloud filesystem
	Storage = struct {
		Disk             func(name ...string) storage.Driver
		RegisterDisk     func(name string, d storage.Driver)
		SetDefaultDisk   func(name string)
		Put              func(path string, contents []byte) error
		PutReader        func(path string, r io.Reader) error
		Get              func(path string) ([]byte, error)
		GetStream        func(path string) (io.ReadCloser, error)
		Exists           func(path string) bool
		Delete           func(path string) error
		Copy             func(src, dst string) error
		Move             func(src, dst string) error
		Size             func(path string) (int64, error)
		LastModified     func(path string) (time.Time, error)
		MimeType         func(path string) (string, error)
		URL              func(path string) string
		TemporaryURL     func(path string, ttl time.Duration) (string, error)
		PresignedPutURL  func(path string, ttl time.Duration) (string, error)
		SaveUploadedFile func(file *multipart.FileHeader, dstPath string, disk ...string) error
	}{
		Disk:             storage.Disk,
		RegisterDisk:     storage.RegisterDisk,
		SetDefaultDisk:   storage.SetDefaultDisk,
		Put:              storage.Put,
		PutReader:        storage.PutReader,
		Get:              storage.Get,
		GetStream:        storage.GetStream,
		Exists:           storage.Exists,
		Delete:           storage.Delete,
		Copy:             storage.Copy,
		Move:             storage.Move,
		Size:             storage.Size,
		LastModified:     storage.LastModified,
		MimeType:         storage.MimeType,
		URL:              storage.URL,
		TemporaryURL:     storage.TemporaryURL,
		PresignedPutURL:  storage.PresignedPutURL,
		SaveUploadedFile: storage.SaveUploadedFile,
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

	// Socialite provides access to multi-provider OAuth2 authentication (Laravel Socialite equivalent)
	Socialite = struct {
		Driver   func(name string) oauth.Provider
		Build    func(name string, cfg oauth.Config) oauth.Provider
		Register func(name string, factory oauth.FactoryFunc)
		Mount    func(r router.RouteRegistrar, provider string, cb oauth.CallbackHandler, prefix ...string)
		User     func(c *context.Context, provider string) (*oauth.User, error)
	}{
		Driver:   oauth.Driver,
		Build:    oauth.Build,
		Register: oauth.Register,
		Mount:    oauth.MountRoutes,
		User: func(c *context.Context, provider string) (*oauth.User, error) {
			return oauth.Driver(provider).User(c)
		},
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

	// Http provides access to the fluent outbound HTTP client and mocking engine
	Http = struct {
		Client            func(httpClient ...*http.Client) *glhttp.Client
		WithToken         func(token string, prefix ...string) *glhttp.PendingRequest
		WithBasicAuth     func(username, password string) *glhttp.PendingRequest
		WithHeader        func(key, value string) *glhttp.PendingRequest
		WithHeaders       func(headers map[string]string) *glhttp.PendingRequest
		WithCookie        func(name, value string) *glhttp.PendingRequest
		WithCookies       func(cookies []*http.Cookie) *glhttp.PendingRequest
		WithQuery         func(params map[string]string) *glhttp.PendingRequest
		WithQueryParam    func(key, value string) *glhttp.PendingRequest
		WithContext       func(ctx stdContext.Context) *glhttp.PendingRequest
		Timeout           func(d time.Duration) *glhttp.PendingRequest
		BaseURL           func(baseURL string) *glhttp.PendingRequest
		Accept            func(mimeType string) *glhttp.PendingRequest
		AcceptJson        func() *glhttp.PendingRequest
		ContentType       func(mimeType string) *glhttp.PendingRequest
		AsJson            func() *glhttp.PendingRequest
		AsForm            func() *glhttp.PendingRequest
		AsMultipart       func() *glhttp.PendingRequest
		Attach            func(fieldName string, content []byte, filename string, contentType ...string) *glhttp.PendingRequest
		Retry             func(times int, sleep time.Duration, when ...glhttp.RetryCondition) *glhttp.PendingRequest
		Get               func(url string, query ...map[string]string) (*glhttp.Response, error)
		Post              func(url string, body ...any) (*glhttp.Response, error)
		Put               func(url string, body ...any) (*glhttp.Response, error)
		Patch             func(url string, body ...any) (*glhttp.Response, error)
		Delete            func(url string, body ...any) (*glhttp.Response, error)
		Head              func(url string, query ...map[string]string) (*glhttp.Response, error)
		Send              func(method, url string, body ...any) (*glhttp.Response, error)
		Fake              func(stubs ...any)
		Sequence          func() *glhttp.ResponseSequence
		Response          func(body any, statusAndHeaders ...any) *glhttp.Response
		AssertSent        func(callback func(req *glhttp.Request) bool) error
		AssertSentCount   func(expectedCount int) error
		AssertNotSent     func(callback func(req *glhttp.Request) bool) error
		AssertNothingSent func() error
		Recorded          func() []glhttp.RecordedExchange
		Reset             func()
	}{
		Client:            glhttp.NewClient,
		WithToken:         glhttp.WithToken,
		WithBasicAuth:     glhttp.WithBasicAuth,
		WithHeader:        glhttp.WithHeader,
		WithHeaders:       glhttp.WithHeaders,
		WithCookie:        glhttp.WithCookie,
		WithCookies:       glhttp.WithCookies,
		WithQuery:         glhttp.WithQuery,
		WithQueryParam:    glhttp.WithQueryParam,
		WithContext:       glhttp.WithContext,
		Timeout:           glhttp.Timeout,
		BaseURL:           glhttp.BaseURL,
		Accept:            glhttp.Accept,
		AcceptJson:        glhttp.AcceptJson,
		ContentType:       glhttp.ContentType,
		AsJson:            glhttp.AsJson,
		AsForm:            glhttp.AsForm,
		AsMultipart:       glhttp.AsMultipart,
		Attach:            glhttp.Attach,
		Retry:             glhttp.Retry,
		Get:               glhttp.Get,
		Post:              glhttp.Post,
		Put:               glhttp.Put,
		Patch:             glhttp.Patch,
		Delete:            glhttp.Delete,
		Head:              glhttp.Head,
		Send:              glhttp.Send,
		Fake:              glhttp.Fake,
		Sequence:          glhttp.Sequence,
		Response:          glhttp.MockResponse,
		AssertSent:        glhttp.AssertSent,
		AssertSentCount:   glhttp.AssertSentCount,
		AssertNotSent:     glhttp.AssertNotSent,
		AssertNothingSent: glhttp.AssertNothingSent,
		Recorded:          glhttp.Recorded,
		Reset:             glhttp.Reset,
	}
)
