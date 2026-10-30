package schedule

import (
	"context"
	"time"

	"github.com/golaravel/golaravel/pkg/queue"
)

var defaultScheduler = NewScheduler()

// DefaultScheduler returns the package-level singleton scheduler instance.
func DefaultScheduler() *Scheduler {
	return defaultScheduler
}

// Call schedules a closure to be executed on the default scheduler.
func Call(action func(ctx context.Context) error) *Event {
	return defaultScheduler.Call(action)
}

// Exec schedules an OS command to be executed on the default scheduler.
func Exec(command string, args ...string) *Event {
	return defaultScheduler.Exec(command, args...)
}

// Job schedules a queue.Job to be dispatched on the default scheduler.
func Job(job queue.Job) *Event {
	return defaultScheduler.Job(job)
}

// Register adds a pre-configured Event to the default scheduler.
func Register(event *Event) *Event {
	return defaultScheduler.Register(event)
}

// Events returns all events registered on the default scheduler.
func Events() []*Event {
	return defaultScheduler.Events()
}

// Clear removes all tasks from the default scheduler.
func Clear() {
	defaultScheduler.Clear()
}

// RunDue executes all tasks due at the given timestamp on the default scheduler.
func RunDue(ctx context.Context, now time.Time) []TaskResult {
	return defaultScheduler.RunDue(ctx, now)
}

// Start launches the in-process scheduler ticker loop on the default scheduler.
func Start(ctx context.Context) {
	defaultScheduler.Start(ctx)
}

// Stop halts the default scheduler ticker loop.
func Stop() {
	defaultScheduler.Stop()
}
