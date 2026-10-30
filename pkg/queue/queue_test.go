package queue

import (
	"bufio"
	"context"
	"errors"
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

	"github.com/goccy/go-json"
	"github.com/golaravel/golaravel/pkg/golaravel"
)

// Sample Jobs for Testing

type BasicTestJob struct {
	Message string `json:"message"`
}

var (
	basicJobReceived string
	basicJobMu       sync.Mutex
)

func (j *BasicTestJob) Handle(ctx context.Context) error {
	basicJobMu.Lock()
	defer basicJobMu.Unlock()
	basicJobReceived = j.Message
	return nil
}

type PriorityTestJob struct {
	Tag string `json:"tag"`
	Q   string `json:"queue"`
}

func (j *PriorityTestJob) Queue() string {
	return j.Q
}

func (j *PriorityTestJob) Handle(ctx context.Context) error {
	return nil
}

type DelayedTestJob struct {
	D time.Duration `json:"duration"`
}

func (j *DelayedTestJob) Delay() time.Duration {
	return j.D
}

func (j *DelayedTestJob) Handle(ctx context.Context) error {
	return nil
}

var failingJobAttempts int32

type FailingJob struct {
	ID        string `json:"id"`
	FailUntil int32  `json:"fail_until"`
	Tries     int    `json:"tries"`
}

func (j *FailingJob) MaxTries() int {
	if j.Tries > 0 {
		return j.Tries
	}
	return 2
}

func (j *FailingJob) Backoff() time.Duration {
	return 10 * time.Millisecond
}

func (j *FailingJob) Handle(ctx context.Context) error {
	curr := atomic.AddInt32(&failingJobAttempts, 1)
	if curr <= j.FailUntil {
		return errors.New("simulated error")
	}
	return nil
}

type TimeoutJob struct {
	TimeoutDuration time.Duration `json:"timeout_duration"`
}

func (j *TimeoutJob) Timeout() time.Duration {
	return j.TimeoutDuration
}

func (j *TimeoutJob) Handle(ctx context.Context) error {
	select {
	case <-time.After(50 * time.Millisecond):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// -------------------------------------------------------------
// Tests
// -------------------------------------------------------------

func TestMemoryDriver_PushPopSizeClear(t *testing.T) {
	d := NewMemoryDriver()
	payload := &JobPayload{
		ID:        "job_1",
		Queue:     "default",
		Type:      "BasicTestJob",
		Data:      []byte(`{"message":"hello"}`),
		CreatedAt: time.Now(),
	}

	if err := d.Push("default", payload); err != nil {
		t.Fatalf("push failed: %v", err)
	}

	size, err := d.Size("default")
	if err != nil || size != 1 {
		t.Fatalf("expected size 1, got %d (err: %v)", size, err)
	}

	popped, err := d.Pop("default", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("pop failed: %v", err)
	}
	if popped.ID != "job_1" {
		t.Fatalf("expected job_1, got %s", popped.ID)
	}

	sizeAfter, _ := d.Size("default")
	if sizeAfter != 0 {
		t.Fatalf("expected empty queue, got %d", sizeAfter)
	}

	// Test Clear
	_ = d.Push("default", payload)
	if err := d.Clear("default"); err != nil {
		t.Fatalf("clear failed: %v", err)
	}
	sizeCleared, _ := d.Size("default")
	if sizeCleared != 0 {
		t.Fatalf("expected size 0 after clear, got %d", sizeCleared)
	}
}

func TestMemoryDriver_DelayedJobs(t *testing.T) {
	d := NewMemoryDriver()
	payload := &JobPayload{
		ID:    "delayed_1",
		Queue: "default",
		Type:  "DelayedTestJob",
	}

	// Delay 50ms
	if err := d.Later("default", 50*time.Millisecond, payload); err != nil {
		t.Fatalf("later failed: %v", err)
	}

	// Should not be available immediately
	_, err := d.Pop("default", 10*time.Millisecond)
	if !errors.Is(err, ErrQueueEmpty) {
		t.Fatalf("expected ErrQueueEmpty before delay expires, got: %v", err)
	}

	// Pop with enough timeout for ticker to promote it
	popped, err := d.Pop("default", 150*time.Millisecond)
	if err != nil {
		t.Fatalf("expected popped job after delay, got: %v", err)
	}
	if popped.ID != "delayed_1" {
		t.Fatalf("expected delayed_1, got %s", popped.ID)
	}
}

func TestQueue_PushAndWorkerProcessing(t *testing.T) {
	q := NewQueue()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q.StartWorkers(ctx, 2, "default")

	basicJobMu.Lock()
	basicJobReceived = ""
	basicJobMu.Unlock()

	err := q.Push(&BasicTestJob{Message: "hello_worker"})
	if err != nil {
		t.Fatalf("push failed: %v", err)
	}

	// Wait for worker
	time.Sleep(100 * time.Millisecond)

	basicJobMu.Lock()
	received := basicJobReceived
	basicJobMu.Unlock()

	if received != "hello_worker" {
		t.Fatalf("expected 'hello_worker', got '%s'", received)
	}

	q.Stop()
}

func TestQueue_PriorityQueues(t *testing.T) {
	d := NewMemoryDriver()
	dlq := NewMemoryFailedJobStore()
	q := NewQueue(d, dlq)

	var executionOrder []string
	var mu sync.Mutex

	recordJob := func(tag string) {
		mu.Lock()
		defer mu.Unlock()
		executionOrder = append(executionOrder, tag)
	}

	// Create custom handler types
	q.pool.RegisterJobType(&PriorityTestJob{})

	// Populate low first, then high
	_ = q.OnQueue("low").Push(&PriorityTestJob{Tag: "low_job", Q: "low"})
	_ = q.OnQueue("high").Push(&PriorityTestJob{Tag: "high_job", Q: "high"})

	// Intercept execution via driver Pop order check
	p1, err1 := q.pool.driver.Pop("high", 100*time.Millisecond)
	if err1 != nil || p1 == nil {
		t.Fatalf("expected high job first: %v", err1)
	}
	recordJob(p1.Queue)

	p2, err2 := q.pool.driver.Pop("low", 100*time.Millisecond)
	if err2 != nil || p2 == nil {
		t.Fatalf("expected low job second: %v", err2)
	}
	recordJob(p2.Queue)

	mu.Lock()
	first := executionOrder[0]
	second := executionOrder[1]
	mu.Unlock()

	if first != "high" || second != "low" {
		t.Fatalf("expected high then low, got %s then %s", first, second)
	}
}

func TestQueue_RetriesAndDLQ(t *testing.T) {
	d := NewMemoryDriver()
	dlq := NewMemoryFailedJobStore()
	metrics := NewMetrics()
	pool := NewWorkerPool(d, dlq, metrics, []string{"default"}, 1)

	atomic.StoreInt32(&failingJobAttempts, 0)
	job := &FailingJob{
		ID:        "fail_job_1",
		FailUntil: 10, // Always fail
		Tries:     2,  // Max 2 tries
	}

	pool.RegisterJobType(job)

	// Direct pool handleJob test to verify DLQ behavior deterministically
	payload := &JobPayload{
		ID:          "job_fail_1",
		Queue:       "default",
		Type:        "FailingJob",
		Data:        []byte(`{"id":"fail_job_1","fail_until":10,"tries":2}`),
		Attempts:    0,
		MaxTries:    2,
		CreatedAt:   time.Now(),
		AvailableAt: time.Now(),
	}

	// Attempt 1 -> should be requeued
	pool.executeJob(context.Background(), 1, payload)
	if atomic.LoadInt32(&failingJobAttempts) != 1 {
		t.Fatalf("expected 1 attempt, got %d", atomic.LoadInt32(&failingJobAttempts))
	}
	if payload.Attempts != 1 {
		t.Fatalf("expected payload attempts 1, got %d", payload.Attempts)
	}

	// Size in driver should be 1 (re-queued for retry)
	size, _ := d.Size("default")
	if size != 1 {
		t.Fatalf("expected 1 retry job in queue, got %d", size)
	}

	// Pop the retried job (wait for 10ms backoff promotion)
	popped, err := d.Pop("default", 200*time.Millisecond)
	if err != nil || popped == nil {
		t.Fatalf("expected retried job in queue, got err: %v", err)
	}

	// Attempt 2 -> hits max tries (2) -> sent to DLQ
	pool.executeJob(context.Background(), 1, popped)

	count, err := dlq.Count()
	if err != nil || count != 1 {
		t.Fatalf("expected 1 failed job in DLQ, got %d (err: %v)", count, err)
	}

	failedJobs, _ := dlq.All(10, 0)
	if len(failedJobs) != 1 || failedJobs[0].ID != "job_fail_1" {
		t.Fatalf("expected failed job with id job_fail_1, got %+v", failedJobs)
	}
	if !strings.Contains(failedJobs[0].Exception, "simulated error") {
		t.Fatalf("expected exception to contain 'simulated error', got: %s", failedJobs[0].Exception)
	}

	// Test RetryFailed
	q := &Queue{
		driver:   d,
		dlq:      dlq,
		metrics:  metrics,
		pool:     pool,
		maxTries: 3,
		defaultQ: "default",
	}

	if err := q.RetryFailed("job_fail_1"); err != nil {
		t.Fatalf("RetryFailed error: %v", err)
	}

	// DLQ should now be empty
	countAfter, _ := dlq.Count()
	if countAfter != 0 {
		t.Fatalf("expected 0 in DLQ after retry, got %d", countAfter)
	}

	// Driver should have the retried job
	sizeAfter, _ := d.Size("default")
	if sizeAfter != 1 {
		t.Fatalf("expected 1 in queue after retry, got %d", sizeAfter)
	}
}

func TestQueue_DLQFlushAndFind(t *testing.T) {
	dlq := NewMemoryFailedJobStore()

	job := &FailedJob{
		ID:        "failed_123",
		Queue:     "default",
		Type:      "TestJob",
		Payload:   `{}`,
		Exception: "test exception",
		FailedAt:  time.Now(),
	}

	if err := dlq.Log(job); err != nil {
		t.Fatalf("failed to log to DLQ: %v", err)
	}

	found, err := dlq.Find("failed_123")
	if err != nil || found.ID != "failed_123" {
		t.Fatalf("expected to find failed_123, got: %v", err)
	}

	if err := dlq.Flush(); err != nil {
		t.Fatalf("failed to flush DLQ: %v", err)
	}

	count, _ := dlq.Count()
	if count != 0 {
		t.Fatalf("expected 0 failed jobs after flush, got %d", count)
	}
}

// -------------------------------------------------------------
// Mock Redis Server & Redis Driver Tests
// -------------------------------------------------------------

// startMockRedisServer runs a lightweight in-memory mock Redis RESP server for testing
func startMockRedisServer(t *testing.T) (string, func()) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start mock redis listener: %v", err)
	}

	mu := sync.Mutex{}
	lists := make(map[string][]string)
	zsets := make(map[string]map[string]int64)
	hashes := make(map[string]map[string]string)

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
						l, err := reader.ReadString('\n')
						if err != nil {
							return
						}
						l = strings.TrimSpace(l)
						if !strings.HasPrefix(l, "$") {
							return
						}
						argLen, _ := strconv.Atoi(l[1:])
						buf := make([]byte, argLen)
						_, err = io.ReadFull(reader, buf)
						if err != nil {
							return
						}
						// Read trailing \r\n
						_, _ = reader.ReadString('\n')
						args[i] = string(buf)
					}

					if len(args) == 0 {
						continue
					}

					cmd := strings.ToUpper(args[0])
					mu.Lock()

					switch cmd {
					case "AUTH":
						writer.WriteString("+OK\r\n")

					case "LPUSH":
						key := args[1]
						val := args[2]
						lists[key] = append([]string{val}, lists[key]...)
						writer.WriteString(fmt.Sprintf(":%d\r\n", len(lists[key])))

					case "RPOP":
						key := args[1]
						vals := lists[key]
						if len(vals) == 0 {
							writer.WriteString("$-1\r\n")
						} else {
							last := vals[len(vals)-1]
							lists[key] = vals[:len(vals)-1]
							writer.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(last), last))
						}

					case "BRPOP":
						key := args[1]
						vals := lists[key]
						if len(vals) == 0 {
							writer.WriteString("$-1\r\n")
						} else {
							last := vals[len(vals)-1]
							lists[key] = vals[:len(vals)-1]
							// *2 \r\n $keylen \r\n key \r\n $vallen \r\n val \r\n
							writer.WriteString(fmt.Sprintf("*2\r\n$%d\r\n%s\r\n$%d\r\n%s\r\n", len(key), key, len(last), last))
						}

					case "LLEN":
						key := args[1]
						writer.WriteString(fmt.Sprintf(":%d\r\n", len(lists[key])))

					case "DEL":
						key := args[1]
						delete(lists, key)
						delete(zsets, key)
						delete(hashes, key)
						writer.WriteString(":1\r\n")

					case "ZADD":
						key := args[1]
						score, _ := strconv.ParseInt(args[2], 10, 64)
						val := args[3]
						if zsets[key] == nil {
							zsets[key] = make(map[string]int64)
						}
						zsets[key][val] = score
						writer.WriteString(":1\r\n")

					case "ZRANGEBYSCORE":
						key := args[1]
						maxScore, _ := strconv.ParseInt(args[3], 10, 64)
						var matches []string
						for v, s := range zsets[key] {
							if s <= maxScore {
								matches = append(matches, v)
							}
						}
						writer.WriteString(fmt.Sprintf("*%d\r\n", len(matches)))
						for _, m := range matches {
							writer.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(m), m))
						}

					case "ZREM":
						key := args[1]
						val := args[2]
						if zsets[key] != nil {
							delete(zsets[key], val)
						}
						writer.WriteString(":1\r\n")

					case "ZCARD":
						key := args[1]
						writer.WriteString(fmt.Sprintf(":%d\r\n", len(zsets[key])))

					case "HSET":
						key := args[1]
						field := args[2]
						val := args[3]
						if hashes[key] == nil {
							hashes[key] = make(map[string]string)
						}
						hashes[key][field] = val
						writer.WriteString(":1\r\n")

					case "HGETALL":
						key := args[1]
						h := hashes[key]
						writer.WriteString(fmt.Sprintf("*%d\r\n", len(h)*2))
						for k, v := range h {
							writer.WriteString(fmt.Sprintf("$%d\r\n%s\r\n$%d\r\n%s\r\n", len(k), k, len(v), v))
						}

					case "HGET":
						key := args[1]
						field := args[2]
						if hashes[key] != nil && hashes[key][field] != "" {
							val := hashes[key][field]
							writer.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(val), val))
						} else {
							writer.WriteString("$-1\r\n")
						}

					case "HDEL":
						key := args[1]
						field := args[2]
						if hashes[key] != nil {
							delete(hashes[key], field)
						}
						writer.WriteString(":1\r\n")

					case "HLEN":
						key := args[1]
						writer.WriteString(fmt.Sprintf(":%d\r\n", len(hashes[key])))

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

func TestRedisDriver_PushPopSizeClear(t *testing.T) {
	addr, cleanup := startMockRedisServer(t)
	defer cleanup()

	cfg := RedisConfig{
		Addr:   addr,
		Prefix: "test:queue:",
	}
	driver, err := NewRedisDriver(cfg)
	if err != nil {
		t.Fatalf("failed to create redis driver: %v", err)
	}

	payload := &JobPayload{
		ID:        "redis_job_1",
		Queue:     "default",
		Type:      "BasicTestJob",
		Data:      []byte(`{"message":"redis_hello"}`),
		CreatedAt: time.Now(),
	}

	if err := driver.Push("default", payload); err != nil {
		t.Fatalf("redis push failed: %v", err)
	}

	size, err := driver.Size("default")
	if err != nil || size != 1 {
		t.Fatalf("expected size 1, got %d (err: %v)", size, err)
	}

	popped, err := driver.Pop("default", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("redis pop failed: %v", err)
	}
	if popped.ID != "redis_job_1" {
		t.Fatalf("expected redis_job_1, got %s", popped.ID)
	}

	// Test Delayed Jobs (Later)
	if err := driver.Later("default", 10*time.Millisecond, payload); err != nil {
		t.Fatalf("redis later failed: %v", err)
	}

	// Test Clear
	if err := driver.Clear("default"); err != nil {
		t.Fatalf("redis clear failed: %v", err)
	}
}

func TestRedisFailedJobStore(t *testing.T) {
	addr, cleanup := startMockRedisServer(t)
	defer cleanup()

	cfg := RedisConfig{
		Addr:   addr,
		Prefix: "test:dlq:",
	}
	store := NewRedisFailedJobStore(cfg)

	job := &FailedJob{
		ID:        "redis_failed_1",
		Queue:     "high",
		Type:      "TestJob",
		Payload:   `{"test":true}`,
		Exception: "connection timeout",
		FailedAt:  time.Now(),
	}

	if err := store.Log(job); err != nil {
		t.Fatalf("store log failed: %v", err)
	}

	all, err := store.All(10, 0)
	if err != nil || len(all) != 1 {
		t.Fatalf("store all failed: %v, len: %d", err, len(all))
	}
	if all[0].ID != "redis_failed_1" {
		t.Fatalf("expected redis_failed_1, got %s", all[0].ID)
	}

	found, err := store.Find("redis_failed_1")
	if err != nil || found.ID != "redis_failed_1" {
		t.Fatalf("store find failed: %v", err)
	}

	if err := store.Delete("redis_failed_1"); err != nil {
		t.Fatalf("store delete failed: %v", err)
	}

	if err := store.Flush(); err != nil {
		t.Fatalf("store flush failed: %v", err)
	}
}

// -------------------------------------------------------------
// Horizon Worker Dashboard Tests
// -------------------------------------------------------------

func TestWorkerDashboard_HTTPAndAPI(t *testing.T) {
	app := golaravel.New()
	MountDashboard(app, "/queue")

	// 1. Test GET /queue/dashboard UI
	req := httptest.NewRequest("GET", "/queue/dashboard", nil)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200 for dashboard, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "GoLaravel Horizon") {
		t.Fatalf("dashboard HTML does not contain 'GoLaravel Horizon': %s", body)
	}

	// 2. Test GET /queue/api/stats
	req = httptest.NewRequest("GET", "/queue/api/stats", nil)
	w = httptest.NewRecorder()
	app.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200 for stats API, got %d", w.Code)
	}
	var statsResp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &statsResp); err != nil {
		t.Fatalf("failed to decode stats JSON: %v", err)
	}
	if _, ok := statsResp["snapshot"]; !ok {
		t.Fatal("stats response missing 'snapshot'")
	}
	if _, ok := statsResp["queue_sizes"]; !ok {
		t.Fatal("stats response missing 'queue_sizes'")
	}

	// 3. Test GET /queue/api/failed
	req = httptest.NewRequest("GET", "/queue/api/failed", nil)
	w = httptest.NewRecorder()
	app.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200 for failed API, got %d", w.Code)
	}

	// 4. Test POST /queue/api/flush
	req = httptest.NewRequest("POST", "/queue/api/flush", nil)
	w = httptest.NewRecorder()
	app.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200 for flush API, got %d", w.Code)
	}

	// 5. Test POST /queue/api/retry-all
	req = httptest.NewRequest("POST", "/queue/api/retry-all", nil)
	w = httptest.NewRecorder()
	app.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200 for retry-all API, got %d", w.Code)
	}
}

func TestMetrics_Tracking(t *testing.T) {
	m := NewMetrics()
	m.RegisterWorker(1)
	m.SetWorkerBusy(1, "BasicJob")

	snap := m.Snapshot()
	if snap.ActiveWorkers != 1 {
		t.Fatalf("expected 1 active worker, got %d", snap.ActiveWorkers)
	}

	payload := &JobPayload{
		ID:    "job_1",
		Queue: "default",
		Type:  "BasicJob",
	}

	m.RecordSuccess(1, payload, 25*time.Millisecond)
	snap2 := m.Snapshot()
	if snap2.TotalProcessed != 1 {
		t.Fatalf("expected 1 job processed, got %d", snap2.TotalProcessed)
	}

	m.RecordFailure(1, payload, 10*time.Millisecond, errors.New("boom"))
	snap3 := m.Snapshot()
	if snap3.TotalFailed != 1 {
		t.Fatalf("expected 1 job failed, got %d", snap3.TotalFailed)
	}
}
