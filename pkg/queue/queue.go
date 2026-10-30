package queue

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"sync"
	"time"

	"github.com/goccy/go-json"
)

// Queue coordinates job dispatching, driver backends, DLQ stores, and worker pools.
type Queue struct {
	mu         sync.RWMutex
	driver     Driver
	dlq        FailedJobStore
	metrics    *Metrics
	pool       *WorkerPool
	maxTries   int
	defaultQ   string
	types      map[string]reflect.Type
}

// Global default queue
var globalQueue = NewDefaultQueue()

// NewDefaultQueue creates a configured Queue instance from environment variables.
func NewDefaultQueue() *Queue {
	var driver Driver
	var dlq FailedJobStore

	connection := os.Getenv("QUEUE_CONNECTION")
	if connection == "redis" || os.Getenv("REDIS_HOST") != "" {
		addr := os.Getenv("REDIS_HOST")
		if addr == "" {
			addr = "127.0.0.1"
		}
		port := os.Getenv("REDIS_PORT")
		if port == "" {
			port = "6379"
		}
		cfg := RedisConfig{
			Addr:     addr + ":" + port,
			Password: os.Getenv("REDIS_PASSWORD"),
			Prefix:   "golaravel:queue:",
		}
		if rd, err := NewRedisDriver(cfg); err == nil {
			driver = rd
			dlq = NewRedisFailedJobStore(cfg)
		}
	}

	if driver == nil {
		driver = NewMemoryDriver()
		dlq = NewMemoryFailedJobStore()
	}

	metrics := NewMetrics()
	pool := NewWorkerPool(driver, dlq, metrics, []string{"high", "default", "low"}, 2)

	return &Queue{
		driver:   driver,
		dlq:      dlq,
		metrics:  metrics,
		pool:     pool,
		maxTries: 3,
		defaultQ: "default",
		types:    make(map[string]reflect.Type),
	}
}

// NewQueue creates a custom Queue instance.
// Accepts:
// - NewQueue() -> default in-memory driver & DLQ
// - NewQueue(capacity int) -> in-memory driver
// - NewQueue(driver Driver, dlq ...FailedJobStore) -> custom driver & DLQ
func NewQueue(args ...any) *Queue {
	var driver Driver = NewMemoryDriver()
	var dlqStore FailedJobStore = NewMemoryFailedJobStore()

	if len(args) > 0 {
		switch v := args[0].(type) {
		case Driver:
			driver = v
			if len(args) > 1 {
				if d, ok := args[1].(FailedJobStore); ok {
					dlqStore = d
				}
			}
		case int:
			// Backward-compatible capacity argument
			driver = NewMemoryDriver()
		}
	}

	metrics := NewMetrics()
	pool := NewWorkerPool(driver, dlqStore, metrics, []string{"high", "default", "low"}, 2)

	return &Queue{
		driver:   driver,
		dlq:      dlqStore,
		metrics:  metrics,
		pool:     pool,
		maxTries: 3,
		defaultQ: "default",
		types:    make(map[string]reflect.Type),
	}
}

// RegisterJobType registers a Job struct type for automatic deserialization.
func (q *Queue) RegisterJobType(job Job) {
	q.mu.Lock()
	defer q.mu.Unlock()

	t := reflect.TypeOf(job)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	q.types[t.Name()] = t
	q.pool.RegisterJobType(job)
}

// Push serializes and enqueues a job for immediate background processing.
func (q *Queue) Push(job Job) error {
	return q.OnQueue("").Push(job)
}

// Later enqueues a job with a time delay before execution.
func (q *Queue) Later(delay time.Duration, job Job) error {
	return q.OnQueue("").Later(delay, job)
}

// OnQueue starts a fluent dispatch builder targeting a specific queue name.
func (q *Queue) OnQueue(queueName string) *PendingDispatch {
	target := queueName
	if target == "" {
		target = q.defaultQ
	}
	return &PendingDispatch{
		queue:     q,
		queueName: target,
		maxTries:  q.maxTries,
	}
}

// StartWorkers launches the worker pool.
func (q *Queue) StartWorkers(ctx context.Context, concurrency int, queues ...string) {
	q.mu.Lock()
	if concurrency > 0 {
		q.pool.concurrency = concurrency
	}
	if len(queues) > 0 {
		q.pool.queues = queues
	}
	q.mu.Unlock()

	q.pool.Start(ctx)
}

// Stop gracefully terminates all active workers.
func (q *Queue) Stop() {
	q.pool.Stop()
}

// Driver returns the underlying queue driver.
func (q *Queue) Driver() Driver {
	return q.driver
}

// DLQ returns the Dead-Letter Queue failed job store.
func (q *Queue) DLQ() FailedJobStore {
	return q.dlq
}

// Metrics returns the queue telemetry tracker.
func (q *Queue) Metrics() *Metrics {
	return q.metrics
}

// SetDriver swaps the queue driver.
func (q *Queue) SetDriver(d Driver) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.driver = d
	q.pool.driver = d
}

// SetDLQ swaps the DLQ store.
func (q *Queue) SetDLQ(dlq FailedJobStore) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.dlq = dlq
	q.pool.dlq = dlq
}

// RetryFailed re-queues a failed job by ID.
func (q *Queue) RetryFailed(id string) error {
	failed, err := q.dlq.Find(id)
	if err != nil {
		return err
	}

	payload := &JobPayload{
		ID:          fmt.Sprintf("retry_%d_%s", time.Now().UnixNano(), failed.ID),
		Queue:       failed.Queue,
		Type:        failed.Type,
		Data:        []byte(failed.Payload),
		Attempts:    0,
		MaxTries:    q.maxTries,
		CreatedAt:   time.Now(),
		AvailableAt: time.Now(),
	}

	if err := q.driver.Push(failed.Queue, payload); err != nil {
		return err
	}

	return q.dlq.Delete(id)
}

// RetryAllFailed re-queues all jobs from the Dead-Letter Queue.
func (q *Queue) RetryAllFailed() (int, error) {
	jobs, err := q.dlq.All(500, 0)
	if err != nil {
		return 0, err
	}

	retried := 0
	for _, j := range jobs {
		if err := q.RetryFailed(j.ID); err == nil {
			retried++
		}
	}
	return retried, nil
}

// PendingDispatch provides a chainable builder for dispatching jobs.
type PendingDispatch struct {
	queue     *Queue
	queueName string
	delay     time.Duration
	maxTries  int
}

// Delay sets the delay before the job becomes available for execution.
func (d *PendingDispatch) Delay(delay time.Duration) *PendingDispatch {
	d.delay = delay
	return d
}

// MaxTries sets the maximum retry attempts for this job.
func (d *PendingDispatch) MaxTries(tries int) *PendingDispatch {
	d.maxTries = tries
	return d
}

// Push serializes and sends the job to the queue.
func (d *PendingDispatch) Push(job Job) error {
	t := reflect.TypeOf(job)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	typeName := t.Name()

	d.queue.RegisterJobType(job)

	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("queue: failed to serialize job: %w", err)
	}

	targetQueue := d.queueName
	if qable, ok := job.(Queueable); ok && qable.Queue() != "" {
		targetQueue = qable.Queue()
	}

	delay := d.delay
	if dable, ok := job.(Delayable); ok && dable.Delay() > 0 {
		delay = dable.Delay()
	}

	maxTries := d.maxTries
	if rable, ok := job.(Retryable); ok && rable.MaxTries() > 0 {
		maxTries = rable.MaxTries()
	}

	payload := &JobPayload{
		ID:          fmt.Sprintf("job_%d", time.Now().UnixNano()),
		Queue:       targetQueue,
		Type:        typeName,
		Data:        data,
		Attempts:    0,
		MaxTries:    maxTries,
		CreatedAt:   time.Now(),
		AvailableAt: time.Now().Add(delay),
	}

	if delay > 0 {
		return d.queue.driver.Later(targetQueue, delay, payload)
	}
	return d.queue.driver.Push(targetQueue, payload)
}

// Later enqueues a job with a delay.
func (d *PendingDispatch) Later(delay time.Duration, job Job) error {
	d.delay = delay
	return d.Push(job)
}

// Global Facade Shortcuts

func DefaultQueue() *Queue {
	return globalQueue
}

func Push(job Job) error {
	return globalQueue.Push(job)
}

func Later(delay time.Duration, job Job) error {
	return globalQueue.Later(delay, job)
}

func OnQueue(queueName string) *PendingDispatch {
	return globalQueue.OnQueue(queueName)
}

func RegisterJobType(job Job) {
	globalQueue.RegisterJobType(job)
}

func StartWorkers(ctx context.Context, concurrency int, queues ...string) {
	globalQueue.StartWorkers(ctx, concurrency, queues...)
}

func Stop() {
	globalQueue.Stop()
}
