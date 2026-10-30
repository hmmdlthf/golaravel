package queue

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"time"

	"github.com/goccy/go-json"
)

// WorkerPool manages concurrent worker goroutines consuming jobs from queues.
type WorkerPool struct {
	driver      Driver
	dlq         FailedJobStore
	metrics     *Metrics
	types       map[string]reflect.Type
	typesMu     sync.RWMutex
	queues      []string // Priority order
	concurrency int
	stopChan    chan struct{}
	wg          sync.WaitGroup
	running     bool
	mu          sync.Mutex
}

// NewWorkerPool creates a new WorkerPool.
func NewWorkerPool(driver Driver, dlq FailedJobStore, metrics *Metrics, queues []string, concurrency int) *WorkerPool {
	if len(queues) == 0 {
		queues = []string{"default"}
	}
	if concurrency <= 0 {
		concurrency = 2
	}
	if metrics == nil {
		metrics = NewMetrics()
	}

	return &WorkerPool{
		driver:      driver,
		dlq:         dlq,
		metrics:     metrics,
		types:       make(map[string]reflect.Type),
		queues:      queues,
		concurrency: concurrency,
		stopChan:    make(chan struct{}),
	}
}

// RegisterJobType registers a Job struct type for deserialization.
func (wp *WorkerPool) RegisterJobType(job Job) {
	wp.typesMu.Lock()
	defer wp.typesMu.Unlock()
	t := reflect.TypeOf(job)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	wp.types[t.Name()] = t
}

// Start launches worker goroutines.
func (wp *WorkerPool) Start(ctx context.Context) {
	wp.mu.Lock()
	if wp.running {
		wp.mu.Unlock()
		return
	}
	wp.running = true
	wp.stopChan = make(chan struct{})
	wp.mu.Unlock()

	for i := 1; i <= wp.concurrency; i++ {
		wp.metrics.RegisterWorker(i)
		wp.wg.Add(1)
		go wp.runWorker(ctx, i)
	}
}

// Stop gracefully shuts down workers and waits for running jobs to finish.
func (wp *WorkerPool) Stop() {
	wp.mu.Lock()
	if !wp.running {
		wp.mu.Unlock()
		return
	}
	wp.running = false
	close(wp.stopChan)
	wp.mu.Unlock()

	wp.wg.Wait()
}

func (wp *WorkerPool) runWorker(ctx context.Context, id int) {
	defer wp.wg.Done()
	slog.Info("Worker started", "worker_id", id, "queues", wp.queues)

	for {
		select {
		case <-ctx.Done():
			return
		case <-wp.stopChan:
			return
		default:
			// Poll queues in priority order
			payload := wp.popNextJob()
			if payload == nil {
				time.Sleep(100 * time.Millisecond)
				continue
			}

			wp.executeJob(ctx, id, payload)
		}
	}
}

// popNextJob checks queues sequentially according to priority order.
func (wp *WorkerPool) popNextJob() *JobPayload {
	for _, qName := range wp.queues {
		payload, err := wp.driver.Pop(qName, 50*time.Millisecond)
		if err == nil && payload != nil {
			return payload
		}
	}
	return nil
}

func (wp *WorkerPool) executeJob(ctx context.Context, workerID int, payload *JobPayload) {
	wp.typesMu.RLock()
	t, exists := wp.types[payload.Type]
	wp.typesMu.RUnlock()

	if !exists {
		err := fmt.Errorf("queue: unrecognized job type '%s'", payload.Type)
		slog.Error("Unrecognized job type", "type", payload.Type)
		wp.metrics.RecordFailure(workerID, payload, 0, err)
		wp.moveToDLQ(payload, err)
		return
	}

	val := reflect.New(t).Interface()
	if err := json.Unmarshal(payload.Data, val); err != nil {
		slog.Error("Failed to deserialize job payload", "type", payload.Type, "error", err)
		wp.metrics.RecordFailure(workerID, payload, 0, err)
		wp.moveToDLQ(payload, err)
		return
	}

	job, ok := val.(Job)
	if !ok {
		err := fmt.Errorf("queue: struct '%s' does not implement Job interface", payload.Type)
		slog.Error(err.Error())
		wp.metrics.RecordFailure(workerID, payload, 0, err)
		wp.moveToDLQ(payload, err)
		return
	}

	wp.metrics.SetWorkerBusy(workerID, payload.Type)
	payload.Attempts++

	// Determine timeout
	execCtx := ctx
	if timeoutable, ok := job.(Timeoutable); ok && timeoutable.Timeout() > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, timeoutable.Timeout())
		defer cancel()
	}

	start := time.Now()
	err := job.Handle(execCtx)
	duration := time.Since(start)

	if err != nil {
		slog.Warn("Job failed", "type", payload.Type, "attempt", payload.Attempts, "max_tries", payload.MaxTries, "error", err)
		wp.metrics.RecordFailure(workerID, payload, duration, err)

		if payload.Attempts < payload.MaxTries {
			// Calculate backoff
			backoff := time.Duration(payload.Attempts*payload.Attempts) * 100 * time.Millisecond
			if retryable, ok := job.(Retryable); ok && retryable.Backoff() > 0 {
				backoff = retryable.Backoff()
			}

			_ = wp.driver.Later(payload.Queue, backoff, payload)
		} else {
			// Max retries exhausted: move to Dead-Letter Queue
			slog.Error("Job exhausted max retries, sent to Dead-Letter Queue", "type", payload.Type, "id", payload.ID)
			wp.moveToDLQ(payload, err)
		}
	} else {
		slog.Debug("Job processed successfully", "type", payload.Type, "id", payload.ID, "duration", duration)
		wp.metrics.RecordSuccess(workerID, payload, duration)
	}
}

func (wp *WorkerPool) moveToDLQ(payload *JobPayload, jobErr error) {
	if wp.dlq == nil {
		return
	}

	errStr := "unknown error"
	if jobErr != nil {
		errStr = jobErr.Error()
	}

	failed := &FailedJob{
		ID:        payload.ID,
		Queue:     payload.Queue,
		Type:      payload.Type,
		Payload:   string(payload.Data),
		Exception: errStr,
		FailedAt:  time.Now(),
	}

	_ = wp.dlq.Log(failed)
}
