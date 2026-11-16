package session

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	gcontext "github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/database"
	"github.com/golaravel/golaravel/pkg/redis"
	"github.com/golaravel/golaravel/pkg/redis/redistest"
)

func newSQLDriver(t *testing.T) (*SQLDriver, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	d, err := NewSQLDriver(db, database.SQLite)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := d.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate must be idempotent: %v", err)
	}
	return d, db
}

func newRedisDriver(t *testing.T) *RedisDriver {
	t.Helper()
	srv := redistest.Start(t)
	c, err := redis.New(redis.Config{Addr: srv.Addr()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return NewRedisDriver(c)
}

func exerciseDriver(t *testing.T, d Driver) {
	t.Helper()
	id := generateSessionID()
	if _, err := d.Read(id); err != ErrSessionNotFound {
		t.Fatalf("missing session: %v", err)
	}
	if err := d.Write(id, []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := d.Write(id, []byte(`{"a":2}`)); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if b, err := d.Read(id); err != nil || string(b) != `{"a":2}` {
		t.Fatalf("read: %q %v", b, err)
	}
	if err := d.Destroy(id); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Read(id); err != ErrSessionNotFound {
		t.Fatalf("after destroy: %v", err)
	}
}

func TestSQLDriver(t *testing.T) {
	d, db := newSQLDriver(t)
	exerciseDriver(t, d)

	// Idle sessions expire on read and are removed by GC.
	d.setLifetime(time.Hour)
	old := generateSessionID()
	_ = d.Write(old, []byte("x"))
	_, _ = db.Exec("UPDATE sessions SET last_activity = ? WHERE id = ?", time.Now().Add(-2*time.Hour).Unix(), old)
	if _, err := d.Read(old); err != ErrSessionNotFound {
		t.Fatalf("expired session readable: %v", err)
	}
	if err := d.GC(time.Hour); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&n)
	if n != 0 {
		t.Fatalf("GC left %d rows", n)
	}
	if _, err := NewSQLDriver(db, database.SQLite, SQLDriverOptions{Table: "x; DROP TABLE sessions"}); err == nil {
		t.Fatal("unsafe table name accepted")
	}
}

func TestRedisDriver(t *testing.T) {
	exerciseDriver(t, newRedisDriver(t))
}

// A session written through one Manager is visible to another Manager (a
// second instance) sharing the store and key.
func TestSharedStoreAcrossManagers(t *testing.T) {
	sqlD, _ := newSQLDriver(t)
	for name, drv := range map[string]Driver{"sql": sqlD, "redis": newRedisDriver(t)} {
		cfg := Config{SecretKey: "shared-secret-key-for-two-instances", Driver: drv, Lifetime: time.Hour}
		a, b := NewManager(cfg), NewManager(cfg)

		rec := httptest.NewRecorder()
		c := &gcontext.Context{}
		c.Reset(rec, httptest.NewRequest("GET", "/", nil))
		s := a.Start(c)
		s.Put("user_id", "42")
		if err := a.Save(c, s); err != nil {
			t.Fatalf("%s save: %v", name, err)
		}
		req := httptest.NewRequest("GET", "/", nil)
		for _, ck := range rec.Result().Cookies() {
			req.AddCookie(ck)
		}
		c2 := &gcontext.Context{}
		c2.Reset(httptest.NewRecorder(), req)
		if got := b.Start(c2).GetString("user_id"); got != "42" {
			t.Fatalf("%s: second instance sees %q", name, got)
		}
	}
}
