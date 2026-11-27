package features_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golaravel/golaravel/pkg/cache"
	"github.com/golaravel/golaravel/pkg/database"
	"github.com/golaravel/golaravel/pkg/events"
	"github.com/golaravel/golaravel/pkg/features"
)

// countingDB counts SELECTs against tenant_settings.
type countingDB struct {
	*sql.DB
	selects atomic.Int32
}

func (c *countingDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if strings.HasPrefix(q, "SELECT name, value") {
		c.selects.Add(1)
	}
	return c.DB.QueryContext(ctx, q, args...)
}

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+filepath.Join(t.TempDir(), "f.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func newManager(t *testing.T, db features.ExecQuerier, opts features.Options, defs ...features.Definition) *features.Manager {
	t.Helper()
	opts.DB, opts.Dialect = db, database.SQLite
	if opts.Events == nil {
		opts.Events = events.NewDispatcher()
	}
	m, err := features.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Register(defs...); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMemoLoadsOncePerRequest(t *testing.T) {
	db := &countingDB{DB: openDB(t)}
	d := newDefs()
	m := newManager(t, db, features.Options{}, d.taxMode, d.recurring, d.branches)

	ctx := features.WithMemo(tenantCtx("acme"))
	for range 5 {
		_ = d.taxMode.Value(ctx)
		_ = features.Enabled(ctx, d.recurring)
		_, _ = m.All(ctx)
	}
	if n := db.selects.Load(); n != 1 {
		t.Fatalf("memo: %d selects, want 1", n)
	}
	// A write through the same context clears the memo.
	if err := d.taxMode.Set(ctx, "none"); err != nil {
		t.Fatal(err)
	}
	if v := d.taxMode.Value(ctx); v != "none" {
		t.Fatalf("after write = %q", v)
	}
	// Without a memo, every read queries.
	plain := tenantCtx("acme")
	before := db.selects.Load()
	_ = d.taxMode.Value(plain)
	_ = d.taxMode.Value(plain)
	if n := db.selects.Load() - before; n != 2 {
		t.Fatalf("no memo: %d selects, want 2", n)
	}
}

func TestSharedCacheAcrossManagers(t *testing.T) {
	raw := openDB(t)
	shared := cache.NewMemoryStore()
	dbA, dbB := &countingDB{DB: raw}, &countingDB{DB: raw}
	// Two app instances: separate managers and definitions, one cache.
	a, b := newDefs(), newDefs()
	newManager(t, dbA, features.Options{Cache: shared}, a.taxMode)
	newManager(t, dbB, features.Options{Cache: shared}, b.taxMode)

	ctx := tenantCtx("acme")
	for range 3 {
		_ = a.taxMode.Value(ctx)
		_ = b.taxMode.Value(ctx)
	}
	if n := dbA.selects.Load() + dbB.selects.Load(); n != 1 {
		t.Fatalf("cache: %d selects, want 1", n)
	}
	if err := a.taxMode.Set(ctx, "exclusive"); err != nil {
		t.Fatal(err)
	}
	if v := b.taxMode.Value(ctx); v != "exclusive" {
		t.Fatalf("other instance sees %q after write", v)
	}
	// Tenants are cached separately.
	if v := b.taxMode.Value(tenantCtx("globex")); v != "inclusive" {
		t.Fatalf("globex = %q", v)
	}
}

func TestSQLCacheBackend(t *testing.T) {
	raw := openDB(t)
	store, backend, err := cache.NewSQLStore(raw, database.SQLite, cache.SQLOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := newDefs()
	db := &countingDB{DB: raw}
	newManager(t, db, features.Options{Cache: store}, d.taxMode, d.theme, d.branches)
	ctx := tenantCtx("acme")
	if err := d.theme.Set(ctx, map[string]string{"color": "red"}); err != nil {
		t.Fatal(err)
	}
	if err := d.branches.Set(ctx, 3); err != nil {
		t.Fatal(err)
	}
	// Values round-trip through the JSON-encoding backend.
	for range 3 {
		if d.theme.Value(ctx)["color"] != "red" || d.branches.Value(ctx) != 3 {
			t.Fatal("values lost through SQL cache")
		}
	}
	// One load for the Set calls' "old value" read, one after the second write.
	if n := db.selects.Load(); n > 3 {
		t.Fatalf("%d selects, cache not used", n)
	}
}

// staleCache simulates a reader that loaded values before a write but
// stores them after the write's invalidation.
func TestStaleFillIsNotServed(t *testing.T) {
	raw := openDB(t)
	shared := cache.NewMemoryStore()
	d := newDefs()
	newManager(t, raw, features.Options{Cache: shared}, d.taxMode)
	ctx := tenantCtx("acme")

	// Cache the current (default) values at version 0 key, then write.
	_ = d.taxMode.Value(ctx)
	if err := d.taxMode.Set(ctx, "none"); err != nil {
		t.Fatal(err)
	}
	// A late filler puts old data under the version it read (0).
	_ = shared.Put("features:tenant_settings:d:acme:0", map[string]string{}, 0)
	if v := d.taxMode.Value(ctx); v != "none" {
		t.Fatalf("stale fill served: %q", v)
	}
}

type brokenCache struct{ cache.Store }

func (brokenCache) Get(string) (any, bool) { return nil, false }
func (brokenCache) Remember(string, time.Duration, func() (any, error)) (any, error) {
	return nil, errors.New("cache down")
}
func (brokenCache) Increment(string, ...int64) (int64, error) { return 0, errors.New("cache down") }

func TestCacheFailureFallsBackToDatabase(t *testing.T) {
	d := newDefs()
	newManager(t, openDB(t), features.Options{Cache: brokenCache{}}, d.taxMode)
	ctx := tenantCtx("acme")
	if err := d.taxMode.Set(ctx, "none"); err != nil {
		t.Fatalf("write with cache down: %v", err)
	}
	if v, err := d.taxMode.Get(ctx); err != nil || v != "none" {
		t.Fatalf("read with cache down = %q, %v", v, err)
	}
}

func TestChangedEvent(t *testing.T) {
	disp := events.NewDispatcher()
	var got []features.Changed
	disp.Listen(features.EventChanged, func(p any) error {
		got = append(got, p.(features.Changed))
		return errors.New("listener failure must not fail the write")
	})
	d := newDefs()
	newManager(t, openDB(t), features.Options{Events: disp,
		Actor: func(context.Context) string { return "admin-7" }}, d.taxMode)
	ctx := tenantCtx("acme")

	if err := d.taxMode.Set(ctx, "exclusive"); err != nil {
		t.Fatal(err)
	}
	if err := d.taxMode.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("%d events", len(got))
	}
	set, reset := got[0], got[1]
	if set.Tenant != "acme" || set.Key != "tax.mode" || set.Old != "inclusive" || set.New != "exclusive" ||
		set.WasOverridden || !set.Overridden || set.By != "admin-7" || set.At.IsZero() {
		t.Fatalf("set event = %+v", set)
	}
	if reset.Old != "exclusive" || reset.New != "inclusive" || !reset.WasOverridden || reset.Overridden {
		t.Fatalf("reset event = %+v", reset)
	}
}
