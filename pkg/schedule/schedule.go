package schedule

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ScheduledTask represents an item registered in the in-process cron scheduler.
type ScheduledTask struct {
	description string
	interval    time.Duration
	exactHour   int
	exactMin    int
	useExact    bool
	lastRun     time.Time
	action      func(ctx context.Context) error
}

// EveryMinute schedules the task to run every minute.
func (st *ScheduledTask) EveryMinute() *ScheduledTask {
	st.interval = 1 * time.Minute
	st.useExact = false
	return st
}

// EveryFiveMinutes schedules the task to run every 5 minutes.
func (st *ScheduledTask) EveryFiveMinutes() *ScheduledTask {
	st.interval = 5 * time.Minute
	st.useExact = false
	return st
}

// Hourly schedules the task to run once an hour.
func (st *ScheduledTask) Hourly() *ScheduledTask {
	st.interval = 1 * time.Hour
	st.useExact = false
	return st
}

// Daily schedules the task to run once every 24 hours.
func (st *ScheduledTask) Daily() *ScheduledTask {
	st.interval = 24 * time.Hour
	st.useExact = false
	return st
}

// DailyAt schedules the task at an exact time (e.g. "00:00" or "14:30").
func (st *ScheduledTask) DailyAt(timeStr string) *ScheduledTask {
	var hour, min int
	_, err := fmt.Sscanf(timeStr, "%d:%d", &hour, &min)
	if err == nil {
		st.exactHour = hour
		st.exactMin = min
		st.useExact = true
	}
	return st
}

// Scheduler coordinates all recurring tasks.
type Scheduler struct {
	tasks []*ScheduledTask
	mu    sync.RWMutex
}

// Global default scheduler
var globalScheduler = NewScheduler()

// NewScheduler creates a new task scheduler.
func NewScheduler() *Scheduler {
	return &Scheduler{
		tasks: make([]*ScheduledTask, 0, 16),
	}
}

// Call schedules a function to be executed.
func (s *Scheduler) Call(action func(ctx context.Context) error) *ScheduledTask {
	s.mu.Lock()
	defer s.mu.Unlock()

	task := &ScheduledTask{
		interval: 1 * time.Minute,
		action:   action,
	}
	s.tasks = append(s.tasks, task)
	return task
}

// Start launches the in-process scheduler ticker loop.
func (s *Scheduler) Start(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				s.evaluate(ctx, now)
			}
		}
	}()
}

func (s *Scheduler) evaluate(ctx context.Context, now time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, task := range s.tasks {
		if task.useExact {
			if now.Hour() == task.exactHour && now.Minute() == task.exactMin {
				if task.lastRun.Day() != now.Day() {
					task.lastRun = now
					go s.runTask(ctx, task)
				}
			}
		} else {
			if task.lastRun.IsZero() || now.Sub(task.lastRun) >= task.interval {
				task.lastRun = now
				go s.runTask(ctx, task)
			}
		}
	}
}

func (s *Scheduler) runTask(ctx context.Context, task *ScheduledTask) {
	if err := task.action(ctx); err != nil {
		slog.Error("Scheduled task failed", "error", err)
	}
}

// Global helper functions
func Call(action func(ctx context.Context) error) *ScheduledTask { return globalScheduler.Call(action) }
func Start(ctx context.Context)                                 { globalScheduler.Start(ctx) }
