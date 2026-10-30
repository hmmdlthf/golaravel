package golaravel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
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
}

// New creates a bare GoLaravel application without default middlewares.
func New() *Engine {
	app := &Engine{
		Router:    router.New(),
		Container: container.New(),
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
