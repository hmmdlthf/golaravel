package queue

import (
	"context"
	"time"
)

// Job defines the fundamental contract for executable background jobs.
type Job interface {
	Handle(ctx context.Context) error
}

// Queueable allows a Job to specify its default destination queue.
type Queueable interface {
	Queue() string
}

// Delayable allows a Job to specify an initial delay before becoming eligible for processing.
type Delayable interface {
	Delay() time.Duration
}

// Retryable allows a Job to define custom retry limits and backoff durations.
type Retryable interface {
	MaxTries() int
	Backoff() time.Duration
}

// Timeoutable allows a Job to specify a maximum execution timeout.
type Timeoutable interface {
	Timeout() time.Duration
}

// JobPayload represents serialized job metadata for execution and persistence.
type JobPayload struct {
	ID          string    `json:"id"`
	Queue       string    `json:"queue"`
	Type        string    `json:"type"`
	Data        []byte    `json:"data"`
	Attempts    int       `json:"attempts"`
	MaxTries    int       `json:"max_tries"`
	CreatedAt   time.Time `json:"created_at"`
	AvailableAt time.Time `json:"available_at"`
}

// FailedJob represents a permanently failed job stored in the Dead-Letter Queue.
type FailedJob struct {
	ID        string    `json:"id"`
	Queue     string    `json:"queue"`
	Type      string    `json:"type"`
	Payload   string    `json:"payload"`
	Exception string    `json:"exception"`
	FailedAt  time.Time `json:"failed_at"`
}
