package golaravel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
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
	"github.com/golaravel/golaravel/pkg/storage"
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

// overrideMethod applies method spoofing before routing. multipartMethod
// is the "_method" peekMultipartMethod found in a multipart body.
func overrideMethod(r *http.Request, multipartMethod string) {
	if r.Method != http.MethodPost {
		return
	}
	m := r.Header.Get("X-HTTP-Method-Override")
	if m == "" {
		ct := r.Header.Get("Content-Type")
		switch {
		case strings.HasPrefix(ct, "multipart/form-data"):
			m = multipartMethod
		case strings.HasPrefix(ct, "application/x-www-form-urlencoded"):
			_ = r.ParseForm()
			m = r.PostFormValue("_method")
		}
	}
	switch strings.ToUpper(strings.TrimSpace(m)) {
	case http.MethodPut, http.MethodPatch, http.MethodDelete:
		r.Method = strings.ToUpper(strings.TrimSpace(m))
	}
}

// methodPeekBytes is how much of a multipart body is read to look for a
// "_method" field; forms put it before their files.
const methodPeekBytes = 64 << 10

// peekMultipartMethod reads the leading text fields of a multipart body,
// up to the first file or methodPeekBytes, looking for "_method", then
// puts the bytes back so handlers read the whole body. Large uploads are
// never parsed here, so routes can still change their body limit.
func peekMultipartMethod(r *http.Request, ct string) string {
	if r.Body == nil || r.Body == http.NoBody {
		return ""
	}
	_, params, err := mime.ParseMediaType(ct)
	if err != nil || params["boundary"] == "" {
		return ""
	}
	var buf bytes.Buffer
	mr := multipart.NewReader(io.TeeReader(io.LimitReader(r.Body, methodPeekBytes), &buf), params["boundary"])
	method := ""
	for {
		part, err := mr.NextPart()
		if err != nil || part.FileName() != "" {
			break
		}
		if part.FormName() == "_method" {
			b, _ := io.ReadAll(io.LimitReader(part, 16))
			method = string(b)
			break
		}
		if _, err := io.Copy(io.Discard, part); err != nil {
			break
		}
	}
	r.Body = replayBody{Reader: io.MultiReader(bytes.NewReader(buf.Bytes()), r.Body), Closer: r.Body}
	return method
}

type replayBody struct {
	io.Reader
	io.Closer
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
	if app.bodyTimeout > 0 && r.Body != nil && r.Body != http.NoBody && r.ContentLength != 0 && r.Header.Get("Upgrade") == "" {
		ctl := http.NewResponseController(w)
		if ctl.SetReadDeadline(time.Now().Add(app.bodyTimeout)) == nil {
			r.Body = &deadlineBody{ReadCloser: r.Body, ctl: ctl}
		}
	}
	// The limit can change per route (middleware.MaxBodySize), so it is a
	// LimitedBody rather than http.MaxBytesReader.
	// A multipart "_method" is looked for first, so the replayed bytes
	// count towards the limit too.
	peeked := ""
	if !app.noOverride && r.Method == http.MethodPost && r.Header.Get("X-HTTP-Method-Override") == "" &&
		strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		peeked = peekMultipartMethod(r, r.Header.Get("Content-Type"))
	}
	var limited *middleware.LimitedBody
	if r.Body != nil && r.Body != http.NoBody {
		limited = middleware.NewLimitedBody(r.Body, app.maxBody)
		r.Body = limited
	}
	if !app.noOverride {
		overrideMethod(r, peeked)
	}
	// Acquire pooled Context
	c := app.pool.Get().(*gcontext.Context)
	c.Reset(w, r)
	c.Container = app.Container.Scoped()
	if limited != nil {
		c.Set(middleware.BodyLimitKey, limited)
	}

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

// ServeStorage mounts the local storage disks at their URL paths
// ("/storage" by default) so the links storage.URL and
// storage.TemporaryURL build work: files on the "public" disk are served
// to anyone, files on other disks only through valid signed links, and
// PresignedPutURL links accept direct uploads. See storage.Handler for
// the rules. Call it after registering any extra local disks.
//
//	app := golaravel.Default()
//	app.ServeStorage()
func (app *Engine) ServeStorage() *Engine {
	h := storage.Handler()
	serve := func(c *gcontext.Context) error {
		h.ServeHTTP(&c.Response, c.Request)
		return nil
	}
	for _, prefix := range storage.MountPaths() {
		app.GET(prefix+"/*filepath", serve)
		app.HEAD(prefix+"/*filepath", serve)
		app.PUT(prefix+"/*filepath", serve)
	}
	return app
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
