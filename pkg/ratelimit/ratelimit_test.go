package ratelimit_test

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/golaravel"
	"github.com/golaravel/golaravel/pkg/ratelimit"
)

func TestMemoryStore_SlidingWindowPrecision(t *testing.T) {
	store := ratelimit.NewMemoryStore()
	defer store.Close()

	key := "test:client:1"
	limit := 3
	window := 100 * time.Millisecond

	// Attempt 1: OK
	res1, err := store.Hit(key, limit, window)
	if err != nil || !res1.Allowed || res1.Remaining != 2 {
		t.Fatalf("attempt 1 failed: allowed=%v, remaining=%d, err=%v", res1.Allowed, res1.Remaining, err)
	}

	// Attempt 2: OK
	res2, err := store.Hit(key, limit, window)
	if err != nil || !res2.Allowed || res2.Remaining != 1 {
		t.Fatalf("attempt 2 failed: allowed=%v, remaining=%d, err=%v", res2.Allowed, res2.Remaining, err)
	}

	// Attempt 3: OK (Exhausts limit)
	res3, err := store.Hit(key, limit, window)
	if err != nil || !res3.Allowed || res3.Remaining != 0 {
		t.Fatalf("attempt 3 failed: allowed=%v, remaining=%d, err=%v", res3.Allowed, res3.Remaining, err)
	}

	// Attempt 4: Blocked (429)
	res4, err := store.Hit(key, limit, window)
	if err != nil || res4.Allowed || res4.Remaining != 0 {
		t.Fatalf("attempt 4 should be rejected: allowed=%v, remaining=%d", res4.Allowed, res4.Remaining)
	}
	if res4.RetryAfter <= 0 {
		t.Fatalf("expected RetryAfter > 0, got %v", res4.RetryAfter)
	}

	// Check status without hitting
	stat, err := store.Status(key, limit, window)
	if err != nil || stat.Allowed {
		t.Fatalf("expected Status to show blocked: allowed=%v", stat.Allowed)
	}

	// Wait for window to slide out
	time.Sleep(120 * time.Millisecond)

	// Attempt 5: Window slid forward, attempt allowed again
	res5, err := store.Hit(key, limit, window)
	if err != nil || !res5.Allowed || res5.Remaining != 2 {
		t.Fatalf("attempt 5 after window slide should be allowed: allowed=%v, remaining=%d", res5.Allowed, res5.Remaining)
	}

	// Reset
	if err := store.Reset(key); err != nil {
		t.Fatalf("Reset failed: %v", err)
	}
	resAfterReset, _ := store.Status(key, limit, window)
	if resAfterReset.Remaining != limit {
		t.Fatalf("expected full remaining after reset, got %d", resAfterReset.Remaining)
	}
}

func TestLimit_FluentConstructors(t *testing.T) {
	sec := ratelimit.PerSecond(10)
	if sec.MaxAttempts != 10 || sec.Decay != 1*time.Second {
		t.Errorf("unexpected PerSecond: %+v", sec)
	}

	min := ratelimit.PerMinute(60).By("custom-ip")
	if min.MaxAttempts != 60 || min.Decay != 1*time.Minute || min.Key != "custom-ip" {
		t.Errorf("unexpected PerMinute: %+v", min)
	}

	hr := ratelimit.PerHour(100)
	if hr.MaxAttempts != 100 || hr.Decay != 1*time.Hour {
		t.Errorf("unexpected PerHour: %+v", hr)
	}

	day := ratelimit.PerDay(1000)
	if day.MaxAttempts != 1000 || day.Decay != 24*time.Hour {
		t.Errorf("unexpected PerDay: %+v", day)
	}

	unlimited := ratelimit.None()
	if !unlimited.Unlimited {
		t.Errorf("expected None() to be unlimited")
	}
}

func TestNamedLimiter_TieredThrottling(t *testing.T) {
	ratelimit.ClearNamedLimiters()
	ratelimit.SetStore(ratelimit.NewMemoryStore())

	// Register tiered rate limiter
	ratelimit.For("tier-api", func(c *context.Context) *ratelimit.Limit {
		tier := c.Header("X-User-Tier")
		userID := c.Header("X-User-ID")

		switch tier {
		case "enterprise":
			return ratelimit.None()
		case "pro":
			return ratelimit.PerMinute(100).By(userID)
		default:
			return ratelimit.PerMinute(2).By(c.ClientIP())
		}
	})

	app := golaravel.New()
	app.GET("/data", ratelimit.Throttle("tier-api"), func(c *context.Context) error {
		return c.String(http.StatusOK, "success")
	})

	// 1. Free tier client: allowed 2 requests, 3rd blocked
	for i := 1; i <= 2; i++ {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/data", nil)
		req.RemoteAddr = "192.168.1.10:1234"
		app.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("free tier request %d failed with code %d", i, w.Code)
		}
	}

	// 3rd request blocked with 429
	w3 := httptest.NewRecorder()
	req3, _ := http.NewRequest("GET", "/data", nil)
	req3.RemoteAddr = "192.168.1.10:1234"
	app.ServeHTTP(w3, req3)
	if w3.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 Too Many Requests for free tier, got %d", w3.Code)
	}
	if w3.Header().Get("Retry-After") == "" {
		t.Errorf("expected Retry-After header on 429 response")
	}

	// 2. Enterprise tier client: unlimited
	for i := 1; i <= 10; i++ {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/data", nil)
		req.Header.Set("X-User-Tier", "enterprise")
		req.RemoteAddr = "192.168.1.20:1234"
		app.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("enterprise request %d should never be blocked, got %d", i, w.Code)
		}
	}
}

func TestMiddleware_ThrottleHeadersAndInline(t *testing.T) {
	ratelimit.SetStore(ratelimit.NewMemoryStore())

	app := golaravel.New()
	// Inline 3 requests per 500ms
	app.GET("/inline", ratelimit.ThrottleMax(3, 500*time.Millisecond), func(c *context.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	for i := 1; i <= 3; i++ {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/inline", nil)
		req.RemoteAddr = "10.0.0.1:4444"
		app.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("request %d failed: code %d", i, w.Code)
		}

		limitHdr := w.Header().Get("X-RateLimit-Limit")
		remHdr := w.Header().Get("X-RateLimit-Remaining")
		resetHdr := w.Header().Get("X-RateLimit-Reset")

		if limitHdr != "3" {
			t.Errorf("expected X-RateLimit-Limit 3, got %s", limitHdr)
		}
		expectedRem := strconv.Itoa(3 - i)
		if remHdr != expectedRem {
			t.Errorf("expected X-RateLimit-Remaining %s, got %s", expectedRem, remHdr)
		}
		if resetHdr == "" {
			t.Errorf("expected X-RateLimit-Reset to be populated")
		}
	}

	// 4th request: 429
	w4 := httptest.NewRecorder()
	req4, _ := http.NewRequest("GET", "/inline", nil)
	req4.RemoteAddr = "10.0.0.1:4444"
	app.ServeHTTP(w4, req4)
	if w4.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for 4th request, got %d", w4.Code)
	}
	if w4.Header().Get("X-RateLimit-Remaining") != "0" {
		t.Errorf("expected X-RateLimit-Remaining 0, got %s", w4.Header().Get("X-RateLimit-Remaining"))
	}
}

func TestProgrammatic_AttemptAndReset(t *testing.T) {
	ratelimit.SetStore(ratelimit.NewMemoryStore())

	key := "auth:login:test@example.com"
	max := 2
	window := 200 * time.Millisecond

	var executedCount int
	task := func() error {
		executedCount++
		return nil
	}

	// Attempt 1: succeeds
	if err := ratelimit.Attempt(key, max, window, task); err != nil {
		t.Fatalf("attempt 1 error: %v", err)
	}

	// Attempt 2: succeeds
	if err := ratelimit.Attempt(key, max, window, task); err != nil {
		t.Fatalf("attempt 2 error: %v", err)
	}

	// Attempt 3: fails with ErrTooManyAttempts
	if err := ratelimit.Attempt(key, max, window, task); err != ratelimit.ErrTooManyAttempts {
		t.Fatalf("expected ErrTooManyAttempts, got: %v", err)
	}

	if executedCount != 2 {
		t.Fatalf("expected task executed 2 times, got %d", executedCount)
	}

	// TooManyAttempts check
	if !ratelimit.TooManyAttempts(key, max, window) {
		t.Fatalf("expected TooManyAttempts to be true")
	}

	// Reset
	if err := ratelimit.Reset(key); err != nil {
		t.Fatalf("Reset error: %v", err)
	}

	// Attempt 4: succeeds after reset
	if err := ratelimit.Attempt(key, max, window, task); err != nil {
		t.Fatalf("attempt after reset failed: %v", err)
	}
	if executedCount != 3 {
		t.Fatalf("expected executedCount 3, got %d", executedCount)
	}
}

// Mock Redis RESP Server Test for RedisStore
func startMockRedisLimiterServer(t *testing.T) (string, func()) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start mock redis: %v", err)
	}

	var mu sync.Mutex
	zsets := make(map[string]map[string]int64)
	closed := int32(0)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				if atomic.LoadInt32(&closed) == 1 {
					return
				}
				continue
			}

			go func(c net.Conn) {
				defer c.Close()
				reader := bufio.NewReader(c)
				writer := bufio.NewWriter(c)

				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					line = strings.TrimSpace(line)
					if !strings.HasPrefix(line, "*") {
						continue
					}

					numArgs, err := strconv.Atoi(line[1:])
					if err != nil {
						return
					}

					args := make([]string, numArgs)
					for i := 0; i < numArgs; i++ {
						header, err := reader.ReadString('\n')
						if err != nil {
							return
						}
						header = strings.TrimSpace(header)
						argLen, _ := strconv.Atoi(header[1:])
						buf := make([]byte, argLen+2)
						_, err = io.ReadFull(reader, buf)
						if err != nil {
							return
						}
						args[i] = string(buf[:argLen])
					}

					cmd := strings.ToUpper(args[0])
					mu.Lock()
					switch cmd {
					case "PING":
						writer.WriteString("+PONG\r\n")
					case "ZREMRANGEBYSCORE":
						key := args[1]
						maxScore, _ := strconv.ParseInt(args[3], 10, 64)
						var deleted int64
						if zs, ok := zsets[key]; ok {
							for m, s := range zs {
								if s <= maxScore {
									delete(zs, m)
									deleted++
								}
							}
						}
						writer.WriteString(fmt.Sprintf(":%d\r\n", deleted))
					case "ZCARD":
						key := args[1]
						count := int64(len(zsets[key]))
						writer.WriteString(fmt.Sprintf(":%d\r\n", count))
					case "ZRANGE":
						key := args[1]
						zs := zsets[key]
						if len(zs) == 0 {
							writer.WriteString("*0\r\n")
						} else {
							var minMember string
							var minScore int64 = 1<<63 - 1
							for m, s := range zs {
								if s < minScore {
									minScore = s
									minMember = m
								}
							}
							writer.WriteString("*2\r\n")
							writer.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(minMember), minMember))
							scoreStr := strconv.FormatInt(minScore, 10)
							writer.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(scoreStr), scoreStr))
						}
					case "ZADD":
						key := args[1]
						score, _ := strconv.ParseInt(args[2], 10, 64)
						member := args[3]
						if zsets[key] == nil {
							zsets[key] = make(map[string]int64)
						}
						zsets[key][member] = score
						writer.WriteString(":1\r\n")
					case "EXPIRE":
						writer.WriteString(":1\r\n")
					case "EVAL":
						// The Hit script: trim, count, add when under the limit.
						key := args[3]
						cutoff, _ := strconv.ParseInt(args[4], 10, 64)
						now, _ := strconv.ParseInt(args[5], 10, 64)
						limit, _ := strconv.Atoi(args[6])
						zs := zsets[key]
						if zs == nil {
							zs = map[string]int64{}
							zsets[key] = zs
						}
						for m, sc := range zs {
							if sc <= cutoff {
								delete(zs, m)
							}
						}
						oldest := ""
						var minScore int64 = 1<<63 - 1
						for _, sc := range zs {
							if sc < minScore {
								minScore = sc
								oldest = strconv.FormatInt(sc, 10)
							}
						}
						count := len(zs)
						allowed := 0
						if count < limit {
							zs[args[7]] = now
							count++
							allowed = 1
						}
						writer.WriteString(fmt.Sprintf("*3\r\n:%d\r\n:%d\r\n$%d\r\n%s\r\n", allowed, count, len(oldest), oldest))
					case "ZREM":
						n := 0
						if zs := zsets[args[1]]; zs != nil {
							if _, ok := zs[args[2]]; ok {
								delete(zs, args[2])
								n = 1
							}
						}
						writer.WriteString(fmt.Sprintf(":%d\r\n", n))
					case "SCAN":
						writer.WriteString("*2\r\n$1\r\n0\r\n")
						writer.WriteString(fmt.Sprintf("*%d\r\n", len(zsets)))
						for k := range zsets {
							writer.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(k), k))
						}
					case "DEL":
						for _, k := range args[1:] {
							delete(zsets, k)
						}
						writer.WriteString(":1\r\n")
					case "KEYS":
						writer.WriteString(fmt.Sprintf("*%d\r\n", len(zsets)))
						for k := range zsets {
							writer.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(k), k))
						}
					default:
						writer.WriteString("+OK\r\n")
					}
					mu.Unlock()
					writer.Flush()
				}
			}(conn)
		}
	}()

	cleanup := func() {
		atomic.StoreInt32(&closed, 1)
		ln.Close()
	}

	return ln.Addr().String(), cleanup
}

func TestRedisStore_SlidingWindow(t *testing.T) {
	addr, cleanup := startMockRedisLimiterServer(t)
	defer cleanup()

	cfg := ratelimit.RedisConfig{
		Addr:    addr,
		Prefix:  "test:rl:",
		Timeout: 2 * time.Second,
	}

	redisStore, err := ratelimit.NewRedisStore(cfg)
	if err != nil {
		t.Fatalf("failed to init RedisStore: %v", err)
	}

	key := "redis:user:42"
	limit := 2
	window := 200 * time.Millisecond

	// Hit 1
	r1, err := redisStore.Hit(key, limit, window)
	if err != nil || !r1.Allowed || r1.Remaining != 1 {
		t.Fatalf("redis hit 1 failed: allowed=%v, remaining=%d, err=%v", r1.Allowed, r1.Remaining, err)
	}

	// Hit 2
	r2, err := redisStore.Hit(key, limit, window)
	if err != nil || !r2.Allowed || r2.Remaining != 0 {
		t.Fatalf("redis hit 2 failed: allowed=%v, remaining=%d, err=%v", r2.Allowed, r2.Remaining, err)
	}

	// Hit 3: blocked
	r3, err := redisStore.Hit(key, limit, window)
	if err != nil || r3.Allowed || r3.Remaining != 0 {
		t.Fatalf("redis hit 3 should be blocked: allowed=%v, remaining=%d, err=%v", r3.Allowed, r3.Remaining, err)
	}

	// Status
	stat, err := redisStore.Status(key, limit, window)
	if err != nil || stat.Allowed {
		t.Fatalf("expected status blocked, got: allowed=%v", stat.Allowed)
	}

	// Reset
	if err := redisStore.Reset(key); err != nil {
		t.Fatalf("redis Reset failed: %v", err)
	}

	rAfterReset, err := redisStore.Hit(key, limit, window)
	if err != nil || !rAfterReset.Allowed {
		t.Fatalf("redis hit after reset failed: %v", err)
	}

	// Clear
	if err := redisStore.Clear(); err != nil {
		t.Fatalf("redis Clear failed: %v", err)
	}
}

func TestRedisStore_ConcurrentHitsNeverExceedLimit(t *testing.T) {
	addr, cleanup := startMockRedisLimiterServer(t)
	defer cleanup()
	store, err := ratelimit.NewRedisStore(ratelimit.RedisConfig{Addr: addr, Prefix: "test:rl:", Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if got := ConcurrentAllowed(t, store, 50, 10); got != 10 {
		t.Fatalf("allowed %d of 50 concurrent hits, want exactly 10", got)
	}
}

// ConcurrentAllowed fires n concurrent hits on one key with the given
// limit and returns how many were allowed.
func ConcurrentAllowed(t *testing.T, s ratelimit.Store, n, limit int) int {
	t.Helper()
	var wg sync.WaitGroup
	var allowed int32
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.Hit("burst", limit, time.Minute)
			if err != nil {
				t.Error(err)
				return
			}
			if r.Allowed {
				atomic.AddInt32(&allowed, 1)
			}
		}()
	}
	wg.Wait()
	return int(atomic.LoadInt32(&allowed))
}
