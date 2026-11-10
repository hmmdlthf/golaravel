package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	gcontext "github.com/golaravel/golaravel/pkg/context"
)

// SessionInit runs once, right after a Session's transaction begins. Use it to
// set transaction-scoped state such as a role or tenant variable for row level
// security, e.g. on PostgreSQL:
//
//	func(ctx context.Context, tx *sql.Tx) error {
//	    _, err := tx.ExecContext(ctx,
//	        "SELECT set_config('role', 'app_tenant', true), set_config('app.current_tenant_id', $1, true)", tenantID)
//	    return err
//	}
type SessionInit func(ctx context.Context, tx *sql.Tx) error

// Session is a request-scoped database transaction that starts lazily on the
// first statement. Requests that never touch the database never open one.
//
// Attach it to a context with WithSession; every statement issued through a
// SessionDriver (and therefore every Ent query using that context) then runs
// inside the session's transaction.
type Session struct {
	db   *sql.DB
	opts *sql.TxOptions
	init SessionInit

	mu     sync.Mutex
	tx     *sql.Tx
	closed bool
	spSeq  int
}

// NewSession creates a lazy session on db. init may be nil.
func NewSession(db *sql.DB, init SessionInit, opts ...*sql.TxOptions) *Session {
	s := &Session{db: db, init: init}
	if len(opts) > 0 {
		s.opts = opts[0]
	}
	return s
}

// ErrSessionClosed is returned when a statement is issued on a finished session.
var ErrSessionClosed = errors.New("database: session is already closed")

// Tx returns the session transaction, beginning it (and running the init hook)
// on first use.
func (s *Session) Tx(ctx context.Context) (*sql.Tx, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, ErrSessionClosed
	}
	if s.tx != nil {
		return s.tx, nil
	}

	tx, err := s.db.BeginTx(ctx, s.opts)
	if err != nil {
		return nil, fmt.Errorf("database: failed to begin session transaction: %w", err)
	}
	if s.init != nil {
		if err := s.init(ctx, tx); err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("database: session init failed: %w", err)
		}
	}
	s.tx = tx
	return tx, nil
}

// Started reports whether the session has opened its transaction.
func (s *Session) Started() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tx != nil
}

// Commit commits the session transaction if one was started. It is a no-op
// otherwise. The session cannot be used afterwards.
func (s *Session) Commit() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.tx == nil {
		return nil
	}
	return s.tx.Commit()
}

// Rollback aborts the session transaction if one was started.
func (s *Session) Rollback() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.tx == nil {
		return nil
	}
	if err := s.tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return err
	}
	return nil
}

func (s *Session) nextSavepoint() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spSeq++
	return fmt.Sprintf("gl_sp_%d", s.spSeq)
}

type sessionCtxKey struct{}

// WithSession returns a context carrying the session.
func WithSession(ctx context.Context, s *Session) context.Context {
	return context.WithValue(ctx, sessionCtxKey{}, s)
}

// SessionFromContext returns the session carried by ctx, or nil.
func SessionFromContext(ctx context.Context) *Session {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(sessionCtxKey{}).(*Session)
	return s
}

// WithoutSession returns a context whose statements bypass any session carried
// by ctx and run directly on the connection pool (e.g. platform-level lookups
// made while serving a tenant request).
func WithoutSession(ctx context.Context) context.Context {
	return context.WithValue(ctx, sessionCtxKey{}, (*Session)(nil))
}

// SessionDriver is an Ent SQL driver that routes statements into the Session
// found in the statement's context. Without a session it behaves exactly like
// the wrapped driver.
//
//	db, _ := database.OpenDB(cfg)
//	client := ent.NewClient(ent.Driver(database.NewSessionDriver(dialect.Postgres, db)))
type SessionDriver struct {
	*entsql.Driver
}

// NewSessionDriver wraps db in a session-aware Ent driver.
func NewSessionDriver(dialectName string, db *sql.DB) *SessionDriver {
	return &SessionDriver{Driver: entsql.OpenDB(dialectName, db)}
}

// Exec implements dialect.ExecQuerier.
func (d *SessionDriver) Exec(ctx context.Context, query string, args, v any) error {
	if s := SessionFromContext(ctx); s != nil {
		tx, err := s.Tx(ctx)
		if err != nil {
			return err
		}
		return entsql.Conn{ExecQuerier: tx}.Exec(ctx, query, args, v)
	}
	return d.Driver.Exec(ctx, query, args, v)
}

// Query implements dialect.ExecQuerier.
func (d *SessionDriver) Query(ctx context.Context, query string, args, v any) error {
	if s := SessionFromContext(ctx); s != nil {
		tx, err := s.Tx(ctx)
		if err != nil {
			return err
		}
		return entsql.Conn{ExecQuerier: tx}.Query(ctx, query, args, v)
	}
	return d.Driver.Query(ctx, query, args, v)
}

// Tx implements dialect.Driver. Inside a session it returns a savepoint-backed
// nested transaction, so client.Tx(ctx) composes with the request transaction.
func (d *SessionDriver) Tx(ctx context.Context) (dialect.Tx, error) {
	return d.BeginTx(ctx, nil)
}

// BeginTx starts a transaction with options (ignored for nested transactions).
func (d *SessionDriver) BeginTx(ctx context.Context, opts *entsql.TxOptions) (dialect.Tx, error) {
	s := SessionFromContext(ctx)
	if s == nil {
		return d.Driver.BeginTx(ctx, opts)
	}
	tx, err := s.Tx(ctx)
	if err != nil {
		return nil, err
	}
	name := s.nextSavepoint()
	if _, err := tx.ExecContext(ctx, "SAVEPOINT "+name); err != nil {
		return nil, err
	}
	return &savepointTx{Conn: entsql.Conn{ExecQuerier: tx}, tx: tx, name: name, ctx: ctx}, nil
}

// savepointTx is a nested transaction inside a Session.
type savepointTx struct {
	entsql.Conn
	tx   *sql.Tx
	name string
	ctx  context.Context
	done bool
}

func (t *savepointTx) Commit() error {
	if t.done {
		return sql.ErrTxDone
	}
	t.done = true
	_, err := t.tx.ExecContext(context.WithoutCancel(t.ctx), "RELEASE SAVEPOINT "+t.name)
	return err
}

func (t *savepointTx) Rollback() error {
	if t.done {
		return sql.ErrTxDone
	}
	t.done = true
	_, err := t.tx.ExecContext(context.WithoutCancel(t.ctx), "ROLLBACK TO SAVEPOINT "+t.name)
	return err
}

// SessionResolver decides, per request, whether the request gets a session and
// how its transaction is initialised. Return ok=false for requests that should
// talk to the pool directly.
type SessionResolver func(c *gcontext.Context) (init SessionInit, ok bool, err error)

// SessionMiddleware gives each request a lazy Session: the transaction commits
// when the handler chain returns nil and rolls back on error or panic.
func SessionMiddleware(db *sql.DB, resolve SessionResolver) gcontext.HandlerFunc {
	return func(c *gcontext.Context) (err error) {
		init, ok, rerr := resolve(c)
		if rerr != nil {
			return rerr
		}
		if !ok || c.Request == nil {
			return c.Next()
		}

		s := NewSession(db, init)
		c.Request = c.Request.WithContext(WithSession(c.Request.Context(), s))

		defer func() {
			if r := recover(); r != nil {
				_ = s.Rollback()
				panic(r)
			}
			if err != nil {
				_ = s.Rollback()
				return
			}
			err = s.Commit()
		}()

		return c.Next()
	}
}
