package golaravel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/golaravel/golaravel/pkg/container"
	gcontext "github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/middleware"
	"github.com/golaravel/golaravel/pkg/router"
	gltest "github.com/golaravel/golaravel/pkg/testing"
)

// ServiceProvider defines the lifecycle contract for modular framework packages.
type ServiceProvider interface {
	Register(c *container.Container)
	Boot(app *Engine) error
}

// Engine is the central GoLaravel application kernel.
type Engine struct {
	*router.Router
	Container   *container.Container
	pool        sync.Pool
	providers   []ServiceProvider
	middlewares []gcontext.HandlerFunc
	maxBody     int64
	bodyTimeout time.Duration
	noOverride  bool
}

// MethodOverride turns form method spoofing on or off (on by default). A
// POST with a "_method" form field (see view.Method) or an
// X-HTTP-Method-Override header of PUT, PATCH or DELETE is routed as that
// method, so HTML forms can reach those routes. Other methods are ignored.
func (app *Engine) MethodOverride(enabled bool) *Engine {
	app.noOverride = !enabled
	return app
}

// overrideMethod applies method spoofing before routing.
func overrideMethod(r *http.Request) {
	if r.Method != http.MethodPost {
		return
	}
	m := r.Header.Get("X-HTTP-Method-Override")
	if m == "" {
		ct := r.Header.Get("Content-Type")
		if strings.HasPrefix(ct, "application/x-www-form-urlencoded") || strings.HasPrefix(ct, "multipart/form-data") {
			if strings.HasPrefix(ct, "multipart/") {
				_ = r.ParseMultipartForm(32 << 20)
			} else {
				_ = r.ParseForm()
			}
			m = r.PostFormValue("_method")
		}
	}
	switch strings.ToUpper(strings.TrimSpace(m)) {
	case http.MethodPut, http.MethodPatch, http.MethodDelete:
		r.Method = strings.ToUpper(strings.TrimSpace(m))
	}
}

// DefaultBodyReadTimeout limits how long reading a request body may take
// unless BodyReadTimeout changes it, so a client trickling a body cannot
// hold a connection (and its goroutine) forever.
const DefaultBodyReadTimeout = 2 * time.Minute

// BodyReadTimeout sets how long the client has to send a request body,
// counted from when the handler chain starts. Upgrade requests
// (WebSockets) and requests without a body are not affected, and the
// deadline is cleared once the body has been read, so slow handlers and
// SSE streams keep working. d <= 0 removes it. Use
// middleware.BodyReadTimeout to change it for one route (large uploads).
func (app *Engine) BodyReadTimeout(d time.Duration) *Engine {
	app.bodyTimeout = d
	return app
}

// DefaultMaxBodyBytes caps request bodies unless MaxBodyBytes changes it.
// It matches the 32 MiB multipart parsing limit.
const DefaultMaxBodyBytes int64 = 32 << 20

// MaxBodyBytes sets the largest request body the application reads; bigger
// bodies fail to read (BindJSON / form parsing return an error) instead of
// filling memory. n <= 0 removes the limit.
func (app *Engine) MaxBodyBytes(n int64) *Engine {
	app.maxBody = n
	return app
}

// New creates a bare GoLaravel application without default middlewares.
func New() *Engine {
	app := &Engine{
		Router:      router.New(),
		Container:   container.New(),
		maxBody:     DefaultMaxBodyBytes,
		bodyTimeout: DefaultBodyReadTimeout,
	}

	app.pool.New = func() any {
		return gcontext.NewContext()
	}

	// Register the container and engine itself into DI
	container.Instance(app.Container, app)

	return app
}

// Default creates a GoLaravel application with production-grade defaults:
// Recovery middleware, Logger middleware, and SecureHeaders middleware.
func Default() *Engine {
	app := New()
	app.Use(middleware.Recovery())
	app.Use(middleware.SecureHeaders())
	return app
}

// RegisterProvider registers a service provider into the application lifecycle.
func (app *Engine) RegisterProvider(sp ServiceProvider) {
	sp.Register(app.Container)
	app.providers = append(app.providers, sp)
}

// Boot boots all registered service providers.
func (app *Engine) Boot() error {
	for _, sp := range app.providers {
		if err := sp.Boot(app); err != nil {
			return fmt.Errorf("app: failed to boot service provider %T: %w", sp, err)
		}
	}
	return nil
}

// ServeHTTP implements http.Handler with zero-allocation context pooling.
func (app *Engine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Acquire pooled Context
	if app.maxBody > 0 && r.Body != nil && r.Body != http.NoBody {
		r.Body = http.MaxBytesReader(w, r.Body, app.maxBody)
	}
	if app.bodyTimeout > 0 && r.Body != nil && r.Body != http.NoBody && r.ContentLength != 0 && r.Header.Get("Upgrade") == "" {
		ctl := http.NewResponseController(w)
		if ctl.SetReadDeadline(time.Now().Add(app.bodyTimeout)) == nil {
			r.Body = &deadlineBody{ReadCloser: r.Body, ctl: ctl}
		}
	}
	if !app.noOverride {
		overrideMethod(r)
	}
	c := app.pool.Get().(*gcontext.Context)
	c.Reset(w, r)
	c.Container = app.Container.Scoped()

	// Zero-allocation Radix tree route matching
	handlers := app.Router.Match(r.Method, r.URL.Path, c)
	if handlers != nil {
		c.SetHandlers(handlers)
	} else {
		c.SetHandlers([]gcontext.HandlerFunc{app.Router.NotFound})
	}

	// Execute handler pipeline
	_ = c.Next()

	// Recycle Context back to pool
	app.pool.Put(c)
}

// Test creates an HTTP test harness client directly against this Engine with zero network overhead.
func (app *Engine) Test(t testing.TB) *gltest.TestClient {
	return gltest.New(t, app)
}

// Run starts the HTTP server with graceful shutdown handling.
func (app *Engine) Run(addr string) error {
	if err := app.Boot(); err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           app,
		ReadHeaderTimeout: 5 * time.Second,
		MaxHeaderBytes:    1 << 20,
		IdleTimeout:       60 * time.Second,
	}

	// Channel to listen for OS interrupt / termination signals
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	serverErr := make(chan error, 1)
	go func() {
		slog.Info("GoLaravel server listening", "address", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return err
	case sig := <-quit:
		slog.Info("Shutdown signal received", "signal", sig.String())
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			slog.Error("Graceful shutdown failed", "error", err)
			return err
		}
		slog.Info("GoLaravel server gracefully stopped")
		return nil
	}
}

// deadlineBody clears the body read deadline once the body has been read
// to the end, so it never fires during a long handler (net/http keeps
// reading the connection after the body to notice clients that go away,
// and a timeout there would cancel the request context). On a timeout the
// deadline stays, so net/http's attempt to drain the rest of the body also
// fails fast and the connection is closed.
type deadlineBody struct {
	io.ReadCloser
	ctl  *http.ResponseController
	done bool
}

func (b *deadlineBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF && !b.done {
		b.done = true
		_ = b.ctl.SetReadDeadline(time.Time{})
	}
	return n, err
}
