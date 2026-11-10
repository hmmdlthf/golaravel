package database

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/mattn/go-sqlite3"

	gcontext "github.com/golaravel/golaravel/pkg/context"
)

func openSessionTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "session.db")+"?_fk=1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func countItems(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM items`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSessionIsLazyAndCommits(t *testing.T) {
	db := openSessionTestDB(t)
	drv := NewSessionDriver(dialect.SQLite, db)

	inits := 0
	s := NewSession(db, func(ctx context.Context, tx *sql.Tx) error {
		inits++
		return nil
	})
	ctx := WithSession(context.Background(), s)

	if s.Started() {
		t.Fatal("session must not start before the first statement")
	}
	if err := drv.Exec(ctx, `INSERT INTO items (name) VALUES (?)`, []any{"a"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := drv.Exec(ctx, `INSERT INTO items (name) VALUES (?)`, []any{"b"}, nil); err != nil {
		t.Fatal(err)
	}
	if !s.Started() || inits != 1 {
		t.Fatalf("expected one started transaction and one init call, got started=%v inits=%d", s.Started(), inits)
	}

	// visible inside the session
	rows := &entsql.Rows{}
	if err := drv.Query(ctx, `SELECT COUNT(*) FROM items`, []any{}, rows); err != nil {
		t.Fatal(err)
	}
	n, err := entsql.ScanInt(rows)
	_ = rows.Close()
	if err != nil || n != 2 {
		t.Fatalf("expected 2 rows inside the session, got %d (%v)", n, err)
	}

	if err := s.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := countItems(t, db); got != 2 {
		t.Fatalf("expected 2 committed rows, got %d", got)
	}
	if err := drv.Exec(ctx, `INSERT INTO items (name) VALUES ('c')`, []any{}, nil); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("expected ErrSessionClosed after commit, got %v", err)
	}
}

func TestSessionRollback(t *testing.T) {
	db := openSessionTestDB(t)
	drv := NewSessionDriver(dialect.SQLite, db)
	s := NewSession(db, nil)
	ctx := WithSession(context.Background(), s)

	if err := drv.Exec(ctx, `INSERT INTO items (name) VALUES ('a')`, []any{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := countItems(t, db); got != 0 {
		t.Fatalf("expected rollback to discard rows, got %d", got)
	}
}

func TestSessionInitFailureAbortsStatement(t *testing.T) {
	db := openSessionTestDB(t)
	drv := NewSessionDriver(dialect.SQLite, db)
	boom := errors.New("boom")
	s := NewSession(db, func(ctx context.Context, tx *sql.Tx) error { return boom })
	ctx := WithSession(context.Background(), s)

	if err := drv.Exec(ctx, `INSERT INTO items (name) VALUES ('a')`, []any{}, nil); !errors.Is(err, boom) {
		t.Fatalf("expected init error, got %v", err)
	}
	if got := countItems(t, db); got != 0 {
		t.Fatalf("expected no rows, got %d", got)
	}
}

func TestSessionNestedTxUsesSavepoints(t *testing.T) {
	db := openSessionTestDB(t)
	drv := NewSessionDriver(dialect.SQLite, db)
	s := NewSession(db, nil)
	ctx := WithSession(context.Background(), s)

	if err := drv.Exec(ctx, `INSERT INTO items (name) VALUES ('outer')`, []any{}, nil); err != nil {
		t.Fatal(err)
	}

	tx, err := drv.Tx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec(ctx, `INSERT INTO items (name) VALUES ('discarded')`, []any{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	tx2, err := drv.Tx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx2.Exec(ctx, `INSERT INTO items (name) VALUES ('kept')`, []any{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatal(err)
	}

	if err := s.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := countItems(t, db); got != 2 {
		t.Fatalf("expected outer + kept rows, got %d", got)
	}
}

func TestSessionDriverWithoutSessionUsesPool(t *testing.T) {
	db := openSessionTestDB(t)
	drv := NewSessionDriver(dialect.SQLite, db)

	if err := drv.Exec(context.Background(), `INSERT INTO items (name) VALUES ('a')`, []any{}, nil); err != nil {
		t.Fatal(err)
	}
	if got := countItems(t, db); got != 1 {
		t.Fatalf("expected autocommit row, got %d", got)
	}

	// WithoutSession escapes an active session
	s := NewSession(db, nil)
	ctx := WithoutSession(WithSession(context.Background(), s))
	if err := drv.Exec(ctx, `INSERT INTO items (name) VALUES ('b')`, []any{}, nil); err != nil {
		t.Fatal(err)
	}
	if s.Started() {
		t.Fatal("WithoutSession must not start the session")
	}
	if got := countItems(t, db); got != 2 {
		t.Fatalf("expected 2 rows, got %d", got)
	}
}

func runSessionMiddleware(t *testing.T, db *sql.DB, drv *SessionDriver, handlerErr error) error {
	t.Helper()
	mw := SessionMiddleware(db, func(c *gcontext.Context) (SessionInit, bool, error) {
		return nil, true, nil
	})
	handler := func(c *gcontext.Context) error {
		if err := drv.Exec(c.Request.Context(), `INSERT INTO items (name) VALUES ('req')`, []any{}, nil); err != nil {
			return err
		}
		return handlerErr
	}

	c := &gcontext.Context{}
	c.Reset(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil))
	c.SetHandlers([]gcontext.HandlerFunc{mw, handler})
	return c.Next()
}

func TestSessionMiddlewareCommitsOnSuccess(t *testing.T) {
	db := openSessionTestDB(t)
	drv := NewSessionDriver(dialect.SQLite, db)
	if err := runSessionMiddleware(t, db, drv, nil); err != nil {
		t.Fatal(err)
	}
	if got := countItems(t, db); got != 1 {
		t.Fatalf("expected committed row, got %d", got)
	}
}

func TestSessionMiddlewareRollsBackOnError(t *testing.T) {
	db := openSessionTestDB(t)
	drv := NewSessionDriver(dialect.SQLite, db)
	want := errors.New("handler failed")
	if err := runSessionMiddleware(t, db, drv, want); !errors.Is(err, want) {
		t.Fatalf("expected handler error, got %v", err)
	}
	if got := countItems(t, db); got != 0 {
		t.Fatalf("expected rollback, got %d rows", got)
	}
}
