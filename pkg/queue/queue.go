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

// Job defines the contract for executable background jobs.
type Job interface {
	Handle(ctx context.Context) error
}

// JobPayload represents serialized job metadata for execution.
type JobPayload struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Data      []byte    `json:"data"`
	Attempts  int       `json:"attempts"`
	MaxTries  int       `json:"max_tries"`
	CreatedAt time.Time `json:"created_at"`
}

// Queue manages background jobs and concurrent worker pools.
type Queue struct {
	jobs      chan *JobPayload
	types     map[string]reflect.Type
	mu        sync.RWMutex
	maxTries  int
	stopChan  chan struct{}
	waitGroup sync.WaitGroup
}

// Global default queue
var globalQueue = NewQueue(1000)

// NewQueue creates an in-memory queue with buffer capacity.
func NewQueue(capacity int) *Queue {
	return &Queue{
		jobs:     make(chan *JobPayload, capacity),
		types:    make(map[string]reflect.Type),
		maxTries: 3,
		stopChan: make(chan struct{}),
	}
}

// RegisterJobType registers a Job struct type for deserialization.
func (q *Queue) RegisterJobType(job Job) {
	q.mu.Lock()
	defer q.mu.Unlock()
	t := reflect.TypeOf(job)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	q.types[t.Name()] = t
}

// Push serializes and enqueues a job for background processing.
func (q *Queue) Push(job Job) error {
	t := reflect.TypeOf(job)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	typeName := t.Name()

	q.RegisterJobType(job)

	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("queue: failed to serialize job: %w", err)
	}

	payload := &JobPayload{
		ID:        fmt.Sprintf("job_%d", time.Now().UnixNano()),
		Type:      typeName,
		Data:      data,
		Attempts:  0,
		MaxTries:  q.maxTries,
		CreatedAt: time.Now(),
	}

	select {
	case q.jobs <- payload:
		return nil
	default:
		return fmt.Errorf("queue: job queue is full")
	}
}

// StartWorkers launches a concurrent worker pool to consume and process jobs.
func (q *Queue) StartWorkers(ctx context.Context, concurrency int) {
	for i := 1; i <= concurrency; i++ {
		q.waitGroup.Add(1)
		go q.worker(ctx, i)
	}
}

// Stop gracefully stops worker goroutines and waits for completion.
func (q *Queue) Stop() {
	close(q.stopChan)
	q.waitGroup.Wait()
}

func (q *Queue) worker(ctx context.Context, id int) {
	defer q.waitGroup.Done()
	slog.Info("Queue worker started", "worker_id", id)

	for {
		select {
		case <-ctx.Done():
			return
		case <-q.stopChan:
			return
		case payload, ok := <-q.jobs:
			if !ok {
				return
			}
			q.executeJob(ctx, payload)
		}
	}
}

func (q *Queue) executeJob(ctx context.Context, payload *JobPayload) {
	q.mu.RLock()
	t, exists := q.types[payload.Type]
	q.mu.RUnlock()

	if !exists {
		slog.Error("Unrecognized job type", "type", payload.Type)
		return
	}

	val := reflect.New(t).Interface()
	if err := json.Unmarshal(payload.Data, val); err != nil {
		slog.Error("Failed to deserialize job", "type", payload.Type, "error", err)
		return
	}

	job, ok := val.(Job)
	if !ok {
		slog.Error("Job does not implement Job interface", "type", payload.Type)
		return
	}

	payload.Attempts++
	if err := job.Handle(ctx); err != nil {
		slog.Warn("Job execution failed", "type", payload.Type, "attempt", payload.Attempts, "error", err)
		if payload.Attempts < payload.MaxTries {
			// Retry after small backoff
			time.Sleep(time.Duration(payload.Attempts) * 100 * time.Millisecond)
			q.jobs <- payload
		} else {
			slog.Error("Job exhausted max retries, sent to Dead Letter", "type", payload.Type)
		}
	} else {
		slog.Debug("Job processed successfully", "type", payload.Type)
	}
}

// Global helper functions
func DefaultQueue() *Queue                                    { return globalQueue }
func Push(job Job) error                                      { return globalQueue.Push(job) }
func StartWorkers(ctx context.Context, concurrency int)      { globalQueue.StartWorkers(ctx, concurrency) }
func Stop()                                                   { globalQueue.Stop() }
