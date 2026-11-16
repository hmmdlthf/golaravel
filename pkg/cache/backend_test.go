package cache

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/golaravel/golaravel/pkg/database"
	"github.com/golaravel/golaravel/pkg/redis"
	"github.com/golaravel/golaravel/pkg/redis/redistest"
)

// sharedStores returns two Stores sharing one backend (two app instances).
func sharedStores(t *testing.T) map[string][2]Store {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	s1, b1, err := NewSQLStore(db, database.SQLite)
	if err != nil {
		t.Fatal(err)
	}
	if err := b1.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	s2, _, _ := NewSQLStore(db, database.SQLite)

	srv := redistest.Start(t)
	c, err := redis.New(redis.Config{Addr: srv.Addr()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return map[string][2]Store{
		"sql":   {s1, s2},
		"redis": {NewRedisStore(c), NewRedisStore(c)},
	}
}

func TestBackendStoresValuesAndTTL(t *testing.T) {
	for name, pair := range sharedStores(t) {
		a, b := pair[0], pair[1]
		_ = a.Put("str", "hello", 0)
		_ = a.Put("num", 42, 0)
		_ = a.Put("flag", true, 0)
		_ = a.Put("obj", map[string]any{"n": 1, "s": "x"}, 0)
		if b.GetString("str") != "hello" || b.GetInt("num") != 42 || !b.GetBool("flag") {
			t.Fatalf("%s: typed getters across instances failed", name)
		}
		if v, _ := b.Get("num"); v != int64(42) {
			t.Fatalf("%s: whole numbers decode as int64, got %T", name, v)
		}
		if m, _ := b.Get("obj"); m.(map[string]any)["n"] != int64(1) {
			t.Fatalf("%s: nested values: %v", name, m)
		}

		_ = a.Put("short", "x", 30*time.Millisecond)
		if _, ok := b.Get("short"); !ok {
			t.Fatalf("%s: value missing before TTL", name)
		}
		time.Sleep(60 * time.Millisecond)
		if _, ok := b.Get("short"); ok {
			t.Fatalf("%s: value survived its TTL", name)
		}

		if ok, _ := a.Add("once", 1, 0); !ok {
			t.Fatalf("%s: first Add", name)
		}
		if ok, _ := b.Add("once", 2, 0); ok {
			t.Fatalf("%s: second Add must fail", name)
		}
		_ = a.Put("exp", 1, 20*time.Millisecond)
		time.Sleep(40 * time.Millisecond)
		if ok, _ := b.Add("exp", 2, 0); !ok {
			t.Fatalf("%s: Add over an expired key", name)
		}

		if n, _ := a.Increment("ctr"); n != 1 {
			t.Fatalf("%s: increment from empty: %d", name, n)
		}
		if n, _ := b.Increment("ctr", 5); n != 6 {
			t.Fatalf("%s: increment: %d", name, n)
		}
		if n, _ := a.Decrement("ctr", 2); n != 4 || b.GetInt("ctr") != 4 {
			t.Fatalf("%s: decrement: %d", name, n)
		}

		calls := 0
		fn := func() (any, error) { calls++; return "computed", nil }
		v1, _ := a.Remember("rem", time.Minute, fn)
		v2, _ := b.Remember("rem", time.Minute, fn)
		if v1 != "computed" || v2 != "computed" || calls != 1 {
			t.Fatalf("%s: remember: %v %v calls=%d", name, v1, v2, calls)
		}
		if b.Pull("rem") != "computed" {
			t.Fatalf("%s: pull", name)
		}
		if _, ok := a.Get("rem"); ok {
			t.Fatalf("%s: pull did not delete", name)
		}

		_ = a.Forget("str")
		if _, ok := b.Get("str"); ok {
			t.Fatalf("%s: forget", name)
		}
		_ = b.Flush()
		if _, ok := a.Get("num"); ok {
			t.Fatalf("%s: flush", name)
		}
	}
}

func TestBackendStoresSharedTags(t *testing.T) {
	for name, pair := range sharedStores(t) {
		a, b := pair[0], pair[1]
		_ = a.Tags("users").Put("u1", "alice", 0)
		_ = a.Tags("posts").Put("p1", "hello", 0)
		if v, ok := b.Tags("users").Get("u1"); !ok || v != "alice" {
			t.Fatalf("%s: tagged read on other instance: %v %v", name, v, ok)
		}
		_ = b.Tags("users").Flush()
		if _, ok := a.Tags("users").Get("u1"); ok {
			t.Fatalf("%s: tag flush on one instance must reach the other", name)
		}
		if _, ok := a.Tags("posts").Get("p1"); !ok {
			t.Fatalf("%s: flushing users removed posts", name)
		}
	}
}

func TestBackendStoresLocks(t *testing.T) {
	for name, pair := range sharedStores(t) {
		a, b := pair[0], pair[1]
		l1 := a.Lock("job", time.Minute, "owner-1")
		l2 := b.Lock("job", time.Minute, "owner-2")
		if ok, err := l1.Acquire(); !ok || err != nil {
			t.Fatalf("%s: acquire: %v %v", name, ok, err)
		}
		if ok, _ := l2.Acquire(); ok {
			t.Fatalf("%s: second owner acquired a held lock", name)
		}
		if ok, _ := a.Lock("job", time.Minute, "owner-1").Acquire(); !ok {
			t.Fatalf("%s: same owner must refresh", name)
		}
		if err := l2.Release(); !errors.Is(err, ErrLockNotOwned) {
			t.Fatalf("%s: release by non-owner: %v", name, err)
		}
		if err := l1.Release(); err != nil {
			t.Fatalf("%s: release: %v", name, err)
		}
		if ok, _ := l2.Acquire(); !ok {
			t.Fatalf("%s: acquire after release", name)
		}
		short := b.Lock("short", 20*time.Millisecond, "x")
		_, _ = short.Acquire()
		time.Sleep(40 * time.Millisecond)
		if ok, _ := a.Lock("short", time.Minute, "y").Acquire(); !ok {
			t.Fatalf("%s: expired lock not reclaimable", name)
		}
	}
}

func TestSQLStorePrune(t *testing.T) {
	db, _ := sql.Open("sqlite3", filepath.Join(t.TempDir(), "p.db"))
	defer db.Close()
	s, b, _ := NewSQLStore(db, database.SQLite)
	_ = b.Migrate(context.Background())
	_ = s.Put("gone", 1, 10*time.Millisecond)
	_ = s.Put("kept", 1, 0)
	time.Sleep(30 * time.Millisecond)
	if err := b.Prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = db.QueryRow("SELECT COUNT(*) FROM cache").Scan(&n)
	if n != 1 {
		t.Fatalf("prune left %d rows, want 1", n)
	}
	if _, _, err := NewSQLStore(db, database.SQLite, SQLOptions{Table: "cache; --"}); err == nil {
		t.Fatal("unsafe table name accepted")
	}
}

func stampede(t *testing.T, name string, a, b Store) {
	t.Helper()
	var calls int32
	var wg sync.WaitGroup
	results := make([]any, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		s := a
		if i%2 == 1 {
			s = b
		}
		go func(i int) {
			defer wg.Done()
			results[i], _ = s.Remember("hot", time.Minute, func() (any, error) {
				atomic.AddInt32(&calls, 1)
				time.Sleep(100 * time.Millisecond)
				return "fresh", nil
			})
		}(i)
	}
	wg.Wait()
	if calls != 1 {
		t.Errorf("%s: Remember computed %d times for 20 concurrent callers, want 1", name, calls)
	}
	for i, r := range results {
		if r != "fresh" {
			t.Errorf("%s: caller %d got %v", name, i, r)
		}
	}
}

func TestRememberStampedeProtection(t *testing.T) {
	m := NewMemoryStore()
	stampede(t, "memory", m, m)
	for name, pair := range sharedStores(t) {
		stampede(t, name, pair[0], pair[1])
	}
}

func TestPullIsAtomicAcrossInstances(t *testing.T) {
	for name, pair := range sharedStores(t) {
		_ = pair[0].Put("ticket", "only-once", 0)
		var got int32
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			s := pair[i%2]
			go func() {
				defer wg.Done()
				if s.Pull("ticket") != nil {
					atomic.AddInt32(&got, 1)
				}
			}()
		}
		wg.Wait()
		if got != 1 {
			t.Errorf("%s: %d callers pulled the value, want exactly 1", name, got)
		}
	}
}
