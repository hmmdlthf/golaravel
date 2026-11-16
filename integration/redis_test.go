package integration

import (
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golaravel/golaravel/pkg/queue"
	"github.com/golaravel/golaravel/pkg/ratelimit"
	"github.com/golaravel/golaravel/pkg/redis"
)

func TestRedisClient(t *testing.T) {
	c := redisClient(t)
	key := "it:" + suffix() + ":client"
	t.Cleanup(func() { _, _ = c.Do("DEL", key, key+":n") })

	if _, err := c.Do("SET", key, "v", "PX", "60000"); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := redis.String(c.Do("GET", key)); v != "v" || !ok || err != nil {
		t.Fatalf("GET: %q %v %v", v, ok, err)
	}
	if n, err := redis.Int(c.Do("INCRBY", key+":n", "3")); n != 3 || err != nil {
		t.Fatalf("INCRBY: %d %v", n, err)
	}
	// The compare-and-delete script used to release cache locks.
	script := `if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("del", KEYS[1]) else return 0 end`
	if n, _ := redis.Int(c.Do("EVAL", script, "1", key, "other")); n != 0 {
		t.Fatal("EVAL deleted a key owned by someone else")
	}
	if n, _ := redis.Int(c.Do("EVAL", script, "1", key, "v")); n != 1 {
		t.Fatal("EVAL did not delete the owner's key")
	}
	var seen int
	if err := c.Scan("it:*", func(keys []string) error { seen += len(keys); return nil }); err != nil {
		t.Fatalf("SCAN: %v", err)
	}
	if seen == 0 {
		t.Fatal("SCAN found nothing")
	}
	if _, err := c.Do("NOSUCHCOMMAND"); err == nil {
		t.Fatal("expected a server error reply")
	}
	if _, err := c.Do("PING"); err != nil {
		t.Fatalf("connection unusable after an error reply: %v", err)
	}
}

// The queue and rate limiter have their own Redis clients; check them on a
// real server too.
func TestQueueRedisDriver(t *testing.T) {
	redisClient(t)
	addr := envRedis()
	d, err := queue.NewRedisDriver(queue.RedisConfig{Addr: addr, Prefix: "it:" + suffix() + ":queue:"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	job := &queue.JobPayload{ID: "it-1", Queue: "default", Type: "T", Data: []byte(`{"a":1}`)}
	if err := d.Push("default", job); err != nil {
		t.Fatal(err)
	}
	if n, _ := d.Size("default"); n != 1 {
		t.Fatalf("size: %d", n)
	}
	got, err := d.Pop("default", time.Second)
	if err != nil || got == nil || got.ID != "it-1" {
		t.Fatalf("pop: %+v %v", got, err)
	}
	if err := d.Later("default", 200*time.Millisecond, &queue.JobPayload{ID: "it-2", Queue: "default", Type: "T"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if j, _ := d.Pop("default", 500*time.Millisecond); j != nil {
			if j.ID != "it-2" {
				t.Fatalf("delayed job: %+v", j)
			}
			_ = d.Clear("default")
			return
		}
	}
	t.Fatal("delayed job never became available")
}

func TestQueueRedisDelayedCustomQueue(t *testing.T) {
	redisClient(t)
	d, err := queue.NewRedisDriver(queue.RedisConfig{Addr: envRedis(), Prefix: "it:" + suffix() + ":queue:"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.Later("emails", 100*time.Millisecond, &queue.JobPayload{ID: "mail-1", Type: "T"}); err != nil {
		t.Fatal(err)
	}
	if j, err := d.Pop("emails", 3*time.Second); err != nil || j == nil || j.ID != "mail-1" {
		t.Fatalf("delayed job on a custom queue: %+v %v", j, err)
	}
}

func TestRateLimitRedisStore(t *testing.T) {
	redisClient(t)
	s, err := ratelimit.NewRedisStore(ratelimit.RedisConfig{Addr: envRedis(), Prefix: "it:" + suffix() + ":rl:", Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	testRateLimitStore(t, s)

	// A burst of concurrent hits must never get past the limit.
	var wg sync.WaitGroup
	var allowed int32
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r, err := s.Hit("burst", 10, time.Minute); err == nil && r.Allowed {
				atomic.AddInt32(&allowed, 1)
			}
		}()
	}
	wg.Wait()
	if allowed != 10 {
		t.Fatalf("allowed %d of 50 concurrent hits, want exactly 10", allowed)
	}
	if err := s.Clear(); err != nil {
		t.Fatalf("clear: %v", err)
	}
}

func envRedis() string { return os.Getenv("GOLARAVEL_TEST_REDIS_ADDR") }

func testRateLimitStore(t *testing.T, s ratelimit.Store) {
	t.Helper()
	key := "user:42"
	for i := 1; i <= 3; i++ {
		r, err := s.Hit(key, 3, time.Minute)
		if err != nil || !r.Allowed || r.Remaining != 3-i {
			t.Fatalf("hit %d: %+v %v", i, r, err)
		}
	}
	if r, _ := s.Hit(key, 3, time.Minute); r.Allowed {
		t.Fatalf("4th hit allowed: %+v", r)
	}
	if r, _ := s.Status(key, 3, time.Minute); r.Allowed || r.Remaining != 0 {
		t.Fatalf("status: %+v", r)
	}
	if err := s.Reset(key); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.Hit(key, 3, time.Minute); !r.Allowed {
		t.Fatalf("after reset: %+v", r)
	}
}
