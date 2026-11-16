package integration

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/golaravel/golaravel/pkg/cache"
	"github.com/golaravel/golaravel/pkg/session"
	"github.com/golaravel/golaravel/pkg/validation"
)

// --- Sessions ---------------------------------------------------------

func exerciseSessionDriver(t *testing.T, d session.Driver) {
	t.Helper()
	id := "itsession" + suffix() + suffix()
	if _, err := d.Read(id); !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("missing session: %v", err)
	}
	payload := []byte(`{"user_id":"42","binary":"\u0000ÿ"}`)
	if err := d.Write(id, payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := d.Write(id, append(payload, ' ')); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	got, err := d.Read(id)
	if err != nil || string(got) != string(payload)+" " {
		t.Fatalf("read: %q %v", got, err)
	}
	if err := d.Destroy(id); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Read(id); !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("after destroy: %v", err)
	}
}

func TestSQLSessionDriver(t *testing.T) {
	for _, b := range sqlBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			table := "it_sessions_" + suffix()
			dropTables(t, b.db, table)
			d, err := session.NewSQLDriver(b.db, b.dialect, session.SQLDriverOptions{Table: table})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if err := d.Migrate(ctx); err != nil {
				t.Fatalf("migrate: %v", err)
			}
			if err := d.Migrate(ctx); err != nil {
				t.Fatalf("migrate twice: %v", err)
			}
			// The manager sets the lifetime; go through it.
			session.NewManager(session.Config{Driver: d, SecretKey: "k", Lifetime: time.Hour})
			exerciseSessionDriver(t, d)

			stale := "itstale" + suffix() + suffix()
			if err := d.Write(stale, []byte("x")); err != nil {
				t.Fatal(err)
			}
			if _, err := b.db.Exec(b.dialect.Rebind(fmt.Sprintf("UPDATE %s SET last_activity = ? WHERE id = ?", table)), time.Now().Add(-2*time.Hour).Unix(), stale); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Read(stale); !errors.Is(err, session.ErrSessionNotFound) {
				t.Fatalf("stale session readable: %v", err)
			}
			if err := d.GC(time.Hour); err != nil {
				t.Fatalf("gc: %v", err)
			}
			var n int
			_ = b.db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&n)
			if n != 0 {
				t.Fatalf("gc left %d rows", n)
			}
		})
	}
}

func TestRedisSessionDriver(t *testing.T) {
	c := redisClient(t)
	d := session.NewRedisDriver(c, session.RedisDriverOptions{Prefix: "it:" + suffix() + ":session:"})
	session.NewManager(session.Config{Driver: d, SecretKey: "k", Lifetime: 300 * time.Millisecond})
	exerciseSessionDriver(t, d)

	id := "itexpiry" + suffix() + suffix()
	_ = d.Write(id, []byte("x"))
	time.Sleep(600 * time.Millisecond)
	if _, err := d.Read(id); !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("session outlived its TTL: %v", err)
	}
}

// --- Cache ------------------------------------------------------------

// exerciseCache runs the Store contract on two stores sharing one backend.
func exerciseCache(t *testing.T, a, b cache.Store) {
	t.Helper()
	_ = a.Put("str", "hello", 0)
	_ = a.Put("num", 42, 0)
	_ = a.Put("flag", true, 0)
	_ = a.Put("obj", map[string]any{"n": 1, "s": "x", "list": []int{1, 2}}, 0)
	if b.GetString("str") != "hello" || b.GetInt("num") != 42 || !b.GetBool("flag") {
		t.Fatal("typed getters across instances")
	}
	if v, _ := b.Get("obj"); v.(map[string]any)["n"] != int64(1) {
		t.Fatalf("nested value: %v", v)
	}

	_ = a.Put("short", "x", 200*time.Millisecond)
	if _, ok := b.Get("short"); !ok {
		t.Fatal("value missing before TTL")
	}
	time.Sleep(400 * time.Millisecond)
	if _, ok := b.Get("short"); ok {
		t.Fatal("value survived its TTL")
	}

	if ok, err := a.Add("once", 1, 0); !ok || err != nil {
		t.Fatalf("first add: %v %v", ok, err)
	}
	if ok, _ := b.Add("once", 2, 0); ok {
		t.Fatal("second add must fail")
	}
	_ = a.Put("exp", 1, 100*time.Millisecond)
	time.Sleep(250 * time.Millisecond)
	if ok, err := b.Add("exp", 2, 0); !ok || err != nil {
		t.Fatalf("add over an expired key: %v %v", ok, err)
	}

	if n, err := a.Increment("ctr"); n != 1 || err != nil {
		t.Fatalf("increment from empty: %d %v", n, err)
	}
	if n, _ := b.Increment("ctr", 5); n != 6 {
		t.Fatalf("increment: %d", n)
	}
	if n, _ := a.Decrement("ctr", 2); n != 4 || b.GetInt("ctr") != 4 {
		t.Fatalf("decrement: %d", n)
	}

	// Concurrent increments from both instances must not lose updates.
	var wg sync.WaitGroup
	var mu sync.Mutex
	var incErrs []error
	for i := 0; i < 20; i++ {
		wg.Add(1)
		s := a
		if i%2 == 1 {
			s = b
		}
		go func() {
			defer wg.Done()
			for attempt := 0; attempt < 20; attempt++ {
				if _, err := s.Increment("race"); err == nil {
					return
				} else if attempt == 19 {
					mu.Lock()
					incErrs = append(incErrs, err)
					mu.Unlock()
				}
				time.Sleep(10 * time.Millisecond)
			}
		}()
	}
	wg.Wait()
	if len(incErrs) > 0 {
		t.Fatalf("concurrent increments failed: %v", incErrs[0])
	}
	if got := a.GetInt("race"); got != 20 {
		t.Fatalf("concurrent increments: got %d, want 20", got)
	}

	calls := 0
	fn := func() (any, error) { calls++; return "computed", nil }
	v1, _ := a.Remember("rem", time.Minute, fn)
	v2, _ := b.Remember("rem", time.Minute, fn)
	if v1 != "computed" || v2 != "computed" || calls != 1 {
		t.Fatalf("remember: %v %v calls=%d", v1, v2, calls)
	}
	if b.Pull("rem") != "computed" {
		t.Fatal("pull")
	}
	if _, ok := a.Get("rem"); ok {
		t.Fatal("pull did not delete")
	}

	_ = a.Tags("users").Put("u1", "alice", 0)
	_ = a.Tags("posts").Put("p1", "hello", 0)
	if v, ok := b.Tags("users").Get("u1"); !ok || v != "alice" {
		t.Fatalf("tagged read: %v %v", v, ok)
	}
	_ = b.Tags("users").Flush()
	if _, ok := a.Tags("users").Get("u1"); ok {
		t.Fatal("tag flush did not reach the other instance")
	}
	if _, ok := a.Tags("posts").Get("p1"); !ok {
		t.Fatal("flushing one tag removed another")
	}

	l1 := a.Lock("job", time.Minute, "owner-1")
	l2 := b.Lock("job", time.Minute, "owner-2")
	if ok, err := l1.Acquire(); !ok || err != nil {
		t.Fatalf("acquire: %v %v", ok, err)
	}
	if ok, _ := l2.Acquire(); ok {
		t.Fatal("second owner acquired a held lock")
	}
	if ok, _ := a.Lock("job", time.Minute, "owner-1").Acquire(); !ok {
		t.Fatal("same owner must refresh")
	}
	forever := a.Lock("forever", 0, "owner-1")
	if ok, _ := forever.Acquire(); !ok {
		t.Fatal("lock without expiry")
	}
	if ok, _ := a.Lock("forever", 0, "owner-1").Acquire(); !ok {
		t.Fatal("same owner must re-acquire a lock without expiry")
	}
	if err := l2.Release(); !errors.Is(err, cache.ErrLockNotOwned) {
		t.Fatalf("release by non-owner: %v", err)
	}
	if err := l1.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if ok, _ := l2.Acquire(); !ok {
		t.Fatal("acquire after release")
	}
	_, _ = b.Lock("short", 100*time.Millisecond, "x").Acquire()
	time.Sleep(250 * time.Millisecond)
	if ok, _ := a.Lock("short", time.Minute, "y").Acquire(); !ok {
		t.Fatal("expired lock not reclaimable")
	}

	_ = a.Forget("str")
	if _, ok := b.Get("str"); ok {
		t.Fatal("forget")
	}
	if err := b.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if _, ok := a.Get("num"); ok {
		t.Fatal("flush")
	}
}

func TestSQLCacheStore(t *testing.T) {
	for _, b := range sqlBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			sfx := suffix()
			opts := cache.SQLOptions{Table: "it_cache_" + sfx, LockTable: "it_cache_locks_" + sfx}
			dropTables(t, b.db, opts.Table, opts.LockTable)
			s1, backend, err := cache.NewSQLStore(b.db, b.dialect, opts)
			if err != nil {
				t.Fatal(err)
			}
			if err := backend.Migrate(context.Background()); err != nil {
				t.Fatalf("migrate: %v", err)
			}
			s2, _, _ := cache.NewSQLStore(b.db, b.dialect, opts)
			exerciseCache(t, s1, s2)

			_ = s1.Put("gone", 1, 50*time.Millisecond)
			time.Sleep(150 * time.Millisecond)
			if err := backend.Prune(context.Background()); err != nil {
				t.Fatalf("prune: %v", err)
			}
		})
	}
}

func TestRedisCacheStore(t *testing.T) {
	c := redisClient(t)
	prefix := "it:" + suffix() + ":cache:"
	a := cache.NewRedisStore(c, cache.RedisOptions{Prefix: prefix})
	b := cache.NewRedisStore(c, cache.RedisOptions{Prefix: prefix})
	other := cache.NewRedisStore(c, cache.RedisOptions{Prefix: "it:" + suffix() + ":other:"})
	_ = other.Put("keep", "me", time.Minute)
	exerciseCache(t, a, b)
	if other.GetString("keep") != "me" {
		t.Fatal("Flush deleted keys outside its prefix")
	}
	_ = other.Flush()
}

// --- Validation database rules -----------------------------------------

func TestValidationDatabaseRules(t *testing.T) {
	for _, b := range sqlBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			table := "it_users_" + suffix()
			dropTables(t, b.db, table)
			if _, err := b.db.Exec(fmt.Sprintf("CREATE TABLE %s (id INTEGER PRIMARY KEY, email VARCHAR(255) NOT NULL)", table)); err != nil {
				t.Fatal(err)
			}
			for i, e := range []string{"ann@example.com", "bob@example.com"} {
				if _, err := b.db.Exec(b.dialect.Rebind(fmt.Sprintf("INSERT INTO %s (id, email) VALUES (?, ?)", table)), i+1, e); err != nil {
					t.Fatal(err)
				}
			}
			chk := validation.SQLChecker(b.db, b.dialect)
			run := func(data map[string]any, rules validation.Rules) validation.ErrorBag {
				return validation.Make(data, rules).DB(chk).Errors()
			}
			if e := run(map[string]any{"email": "ann@example.com"}, validation.Rules{"email": "unique:" + table + ",email"}); len(e["email"]) == 0 {
				t.Fatal("unique should fail for a taken email")
			}
			if e := run(map[string]any{"email": "ann@example.com"}, validation.Rules{"email": "unique:" + table + ",email,1"}); len(e) > 0 {
				t.Fatalf("unique ignoring own row: %v", e)
			}
			if e := run(map[string]any{"email": "new@example.com"}, validation.Rules{"email": "unique:" + table}); len(e) > 0 {
				t.Fatalf("unique for a free email: %v", e)
			}
			if e := run(map[string]any{"user_id": 9}, validation.Rules{"user_id": "exists:" + table + ",id"}); len(e) == 0 {
				t.Fatal("exists should fail for a missing row")
			}
			if e := run(map[string]any{"ids": []any{1, 2}}, validation.Rules{"ids": "exists:" + table + ",id"}); len(e) > 0 {
				t.Fatalf("exists over a list: %v", e)
			}
		})
	}
}
