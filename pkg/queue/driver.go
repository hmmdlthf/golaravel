package queue

import (
	"errors"
	"time"
)

// Common queue errors
var (
	ErrQueueEmpty      = errors.New("queue: queue is empty")
	ErrQueueFull       = errors.New("queue: queue is full")
	ErrJobNotFound     = errors.New("queue: job not found")
	ErrDriverNotFound  = errors.New("queue: driver not found")
	ErrConnectionError = errors.New("queue: connection error")
)

// Driver defines the contract for queue storage backends (Memory, Redis, etc.).
type Driver interface {
	// Push enqueues a job payload immediately on the given queue.
	Push(queue string, payload *JobPayload) error

	// Later enqueues a job payload with a delay.
	Later(queue string, delay time.Duration, payload *JobPayload) error

	// Pop pops the next ready job from the queue. If timeout > 0, it blocks up to timeout.
	Pop(queue string, timeout time.Duration) (*JobPayload, error)

	// Size returns the count of ready jobs in the queue.
	Size(queue string) (int64, error)

	// Clear purges all jobs from the queue.
	Clear(queue string) error
}

// FailedJobStore defines the contract for Dead-Letter Queue (DLQ) storage.
type FailedJobStore interface {
	// Log records a permanently failed job.
	Log(job *FailedJob) error

	// All retrieves paginated failed jobs.
	All(limit, offset int) ([]*FailedJob, error)

	// Find locates a failed job by ID.
	Find(id string) (*FailedJob, error)

	// Delete removes a failed job by ID.
	Delete(id string) error

	// Flush removes all failed jobs.
	Flush() error

	// Count returns the total number of failed jobs.
	Count() (int64, error)
}
