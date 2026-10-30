package schedule

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/golaravel/golaravel/pkg/queue"
)

// TaskResult records the outcome and telemetry of a scheduled task execution.
type TaskResult struct {
	Event      *Event
	StartedAt  time.Time
	FinishedAt time.Time
	Duration   time.Duration
	Error      error
	Skipped    bool
	SkipReason string
}

// Scheduler coordinates and executes recurring tasks.
type Scheduler struct {
	mu       sync.RWMutex
	tasks    []*Event
	running  bool
	stopChan chan struct{}
}

// NewScheduler creates a new task scheduler.
func NewScheduler() *Scheduler {
	return &Scheduler{
		tasks:    make([]*Event, 0, 16),
		stopChan: make(chan struct{}),
	}
}

// Call schedules a function closure to be executed.
func (s *Scheduler) Call(action func(ctx context.Context) error) *Event {
	s.mu.Lock()
	defer s.mu.Unlock()

	task := NewEvent()
	task.action = action
	s.tasks = append(s.tasks, task)
	return task
}

// Exec schedules an external operating system command to be executed.
func (s *Scheduler) Exec(command string, args ...string) *Event {
	s.mu.Lock()
	defer s.mu.Unlock()

	task := NewEvent()
	task.command = command
	task.commandArgs = args
	s.tasks = append(s.tasks, task)
	return task
}

// Job schedules a queue.Job to be dispatched to the queue system.
func (s *Scheduler) Job(job queue.Job) *Event {
	s.mu.Lock()
	defer s.mu.Unlock()

	task := NewEvent()
	task.job = job
	s.tasks = append(s.tasks, task)
	return task
}

// Register adds a pre-configured Event to the scheduler.
func (s *Scheduler) Register(event *Event) *Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks = append(s.tasks, event)
	return event
}

// Events returns a copy of all registered events.
func (s *Scheduler) Events() []*Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	events := make([]*Event, len(s.tasks))
	copy(events, s.tasks)
	return events
}

// Clear removes all registered events from the scheduler.
func (s *Scheduler) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks = s.tasks[:0]
}

// RunDue runs all tasks that are due at the given timestamp.
func (s *Scheduler) RunDue(ctx context.Context, now time.Time) []TaskResult {
	s.mu.RLock()
	tasks := make([]*Event, len(s.tasks))
	copy(tasks, s.tasks)
	s.mu.RUnlock()

	var results []TaskResult
	for _, task := range tasks {
		if !task.IsDue(now) {
			continue
		}

		res := TaskResult{
			Event:     task,
			StartedAt: time.Now(),
		}

		if task.IsRunInBackground() {
			go func(t *Event) {
				if err := t.Run(ctx); err != nil {
					slog.Error("Background scheduled task failed", "task", t.GetName(), "error", err)
				}
			}(task)

			res.FinishedAt = time.Now()
			res.Duration = res.FinishedAt.Sub(res.StartedAt)
			results = append(results, res)
			continue
		}

		err := task.Run(ctx)
		res.FinishedAt = time.Now()
		res.Duration = res.FinishedAt.Sub(res.StartedAt)
		res.Error = err
		results = append(results, res)
	}

	return results
}

// Start launches the in-process scheduler ticker loop, running due tasks every minute.
func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.stopChan = make(chan struct{})
	s.mu.Unlock()

	ticker := time.NewTicker(1 * time.Second)
	go func() {
		defer ticker.Stop()
		var lastMinute time.Time

		for {
			select {
			case <-ctx.Done():
				s.mu.Lock()
				s.running = false
				s.mu.Unlock()
				return
			case <-s.stopChan:
				return
			case now := <-ticker.C:
				currentMinute := now.Truncate(time.Minute)
				if currentMinute.After(lastMinute) {
					lastMinute = currentMinute
					s.RunDue(ctx, now)
				}
			}
		}
	}()
}

// Stop halts the running scheduler ticker loop.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	s.running = false
	close(s.stopChan)
}
