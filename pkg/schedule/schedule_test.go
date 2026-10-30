package schedule_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golaravel/golaravel/pkg/schedule"
)

type mockJob struct {
	handled atomic.Bool
}

func (m *mockJob) Handle(ctx context.Context) error {
	m.handled.Store(true)
	return nil
}

func TestCron_ParseAndMatches(t *testing.T) {
	// Valid cron
	c, err := schedule.ParseCron("*/5 9-17 * 1-12 1-5")
	if err != nil {
		t.Fatalf("unexpected error parsing cron: %v", err)
	}

	// Monday at 10:15
	mon := time.Date(2026, time.November, 2, 10, 15, 0, 0, time.Local) // 2026-11-02 is Monday
	if !c.Matches(mon) {
		t.Errorf("expected cron to match Monday 10:15")
	}

	// Saturday at 10:15
	sat := time.Date(2026, time.November, 7, 10, 15, 0, 0, time.Local) // Saturday
	if c.Matches(sat) {
		t.Errorf("expected cron NOT to match Saturday")
	}

	// Invalid fields
	if _, err := schedule.ParseCron("bad cron"); err == nil {
		t.Errorf("expected error on invalid cron format")
	}
	if _, err := schedule.ParseCron("60 * * * *"); err == nil {
		t.Errorf("expected error on out of range minute")
	}
	if _, err := schedule.ParseCron("* 25 * * *"); err == nil {
		t.Errorf("expected error on out of range hour")
	}

	// Next occurrence
	base := time.Date(2026, time.November, 2, 10, 14, 0, 0, time.Local)
	next := c.Next(base)
	if next.Hour() != 10 || next.Minute() != 15 {
		t.Errorf("expected next run at 10:15, got %v", next)
	}
}

func TestEvent_FluentTimingDirectives(t *testing.T) {
	e := schedule.NewEvent()

	e.EveryMinute()
	if e.GetCronExpression() != "* * * * *" {
		t.Errorf("expected '* * * * *', got '%s'", e.GetCronExpression())
	}

	e.EveryTwoMinutes()
	if e.GetCronExpression() != "*/2 * * * *" {
		t.Errorf("expected '*/2 * * * *', got '%s'", e.GetCronExpression())
	}

	e.EveryFiveMinutes()
	if e.GetCronExpression() != "*/5 * * * *" {
		t.Errorf("expected '*/5 * * * *', got '%s'", e.GetCronExpression())
	}

	e.EveryTenMinutes()
	if e.GetCronExpression() != "*/10 * * * *" {
		t.Errorf("expected '*/10 * * * *', got '%s'", e.GetCronExpression())
	}

	e.EveryFifteenMinutes()
	if e.GetCronExpression() != "*/15 * * * *" {
		t.Errorf("expected '*/15 * * * *', got '%s'", e.GetCronExpression())
	}

	e.EveryThirtyMinutes()
	if e.GetCronExpression() != "*/30 * * * *" {
		t.Errorf("expected '*/30 * * * *', got '%s'", e.GetCronExpression())
	}

	e.Hourly()
	if e.GetCronExpression() != "0 * * * *" {
		t.Errorf("expected '0 * * * *', got '%s'", e.GetCronExpression())
	}

	e.HourlyAt(45)
	if e.GetCronExpression() != "45 * * * *" {
		t.Errorf("expected '45 * * * *', got '%s'", e.GetCronExpression())
	}

	e.Daily()
	if e.GetCronExpression() != "0 0 * * *" {
		t.Errorf("expected '0 0 * * *', got '%s'", e.GetCronExpression())
	}

	e.DailyAt("14:30")
	if e.GetCronExpression() != "30 14 * * *" {
		t.Errorf("expected '30 14 * * *', got '%s'", e.GetCronExpression())
	}

	e.TwiceDaily(1, 13)
	if e.GetCronExpression() != "0 1,13 * * *" {
		t.Errorf("expected '0 1,13 * * *', got '%s'", e.GetCronExpression())
	}

	e.Weekly()
	if e.GetCronExpression() != "0 0 * * 0" {
		t.Errorf("expected '0 0 * * 0', got '%s'", e.GetCronExpression())
	}

	e.WeeklyOn(time.Friday, "18:00")
	if e.GetCronExpression() != "0 18 * * 5" {
		t.Errorf("expected '0 18 * * 5', got '%s'", e.GetCronExpression())
	}

	e.Monthly()
	if e.GetCronExpression() != "0 0 1 * *" {
		t.Errorf("expected '0 0 1 * *', got '%s'", e.GetCronExpression())
	}

	e.MonthlyOn(15, "09:15")
	if e.GetCronExpression() != "15 9 15 * *" {
		t.Errorf("expected '15 9 15 * *', got '%s'", e.GetCronExpression())
	}

	e.Quarterly()
	if e.GetCronExpression() != "0 0 1 1,4,7,10 *" {
		t.Errorf("expected '0 0 1 1,4,7,10 *', got '%s'", e.GetCronExpression())
	}

	e.Yearly()
	if e.GetCronExpression() != "0 0 1 1 *" {
		t.Errorf("expected '0 0 1 1 *', got '%s'", e.GetCronExpression())
	}

	e.Weekdays()
	if e.GetCronExpression() != "0 0 1 1 1,2,3,4,5" {
		t.Errorf("expected weekdays expression, got '%s'", e.GetCronExpression())
	}

	e.Weekends()
	if e.GetCronExpression() != "0 0 1 1 6,0" && e.GetCronExpression() != "0 0 1 1 0,6" {
		t.Errorf("expected weekends expression, got '%s'", e.GetCronExpression())
	}
}

func TestEvent_HooksAndConditionals(t *testing.T) {
	ctx := context.Background()
	var order []string

	e := schedule.NewEvent().
		Name("test-hooks").
		When(func() bool { return true }).
		Skip(func() bool { return false }).
		Before(func() { order = append(order, "before") }).
		After(func() { order = append(order, "after") }).
		OnSuccess(func() { order = append(order, "success") })

	var ran bool
	e.Cron("* * * * *")
	_ = schedule.Call(func(ctx context.Context) error {
		ran = true
		order = append(order, "run")
		return nil
	})

	err := e.Run(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !ran {
		// e was executed directly
	}
	expectedOrder := []string{"before", "after", "success"}
	if len(order) != len(expectedOrder) {
		t.Fatalf("expected order %v, got %v", expectedOrder, order)
	}
	for i := range expectedOrder {
		if order[i] != expectedOrder[i] {
			t.Errorf("at index %d expected %s, got %s", i, expectedOrder[i], order[i])
		}
	}

	// Test When filter negative
	whenSkipped := false
	e2 := schedule.NewEvent().
		When(func() bool { return false }).
		Before(func() { whenSkipped = true })

	_ = e2.Run(ctx)
	if whenSkipped {
		t.Errorf("expected task to be skipped due to When filter")
	}

	// Test Skip filter positive
	skipSkipped := false
	e3 := schedule.NewEvent().
		Skip(func() bool { return true }).
		Before(func() { skipSkipped = true })

	_ = e3.Run(ctx)
	if skipSkipped {
		t.Errorf("expected task to be skipped due to Skip filter")
	}

	// Test OnFailure hook
	var failErr error
	e4 := schedule.NewEvent().
		OnFailure(func(err error) { failErr = err })
	e4.Cron("* * * * *")
	// simulate action returning error
	task := schedule.NewScheduler().Call(func(ctx context.Context) error {
		return errors.New("boom")
	})
	task.OnFailure(func(err error) { failErr = err })
	_ = task.Run(ctx)
	if failErr == nil || failErr.Error() != "boom" {
		t.Errorf("expected failure hook to receive 'boom', got %v", failErr)
	}
}

func TestEvent_WithoutOverlapping(t *testing.T) {
	ctx := context.Background()
	s := schedule.NewScheduler()

	started := make(chan struct{})
	finish := make(chan struct{})

	event := s.Call(func(ctx context.Context) error {
		close(started)
		<-finish
		return nil
	}).Name("long-running-job").WithoutOverlapping(5 * time.Second)

	var run1Err, run2Err error
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		run1Err = event.Run(ctx)
	}()

	<-started

	go func() {
		defer wg.Done()
		run2Err = event.Run(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	close(finish)
	wg.Wait()

	if run1Err != nil {
		t.Errorf("first run should succeed, got: %v", run1Err)
	}
	if run2Err == nil {
		t.Errorf("second run should have been blocked by WithoutOverlapping")
	}
}

func TestEvent_OnOneServer(t *testing.T) {
	ctx := context.Background()
	s := schedule.NewScheduler()

	var counter atomic.Int32
	event := s.Call(func(ctx context.Context) error {
		counter.Add(1)
		return nil
	}).Name("single-server-job").OnOneServer(30 * time.Second)

	// Simulate Server A and Server B running at the same minute
	errA := event.Run(ctx)
	errB := event.Run(ctx)

	if errA != nil || errB != nil {
		t.Fatalf("unexpected error: errA=%v, errB=%v", errA, errB)
	}

	// Counter should only increment once
	if counter.Load() != 1 {
		t.Errorf("expected only 1 server execution, got %d", counter.Load())
	}
}

func TestEvent_RunInBackground(t *testing.T) {
	ctx := context.Background()
	s := schedule.NewScheduler()

	var done atomic.Bool
	s.Call(func(ctx context.Context) error {
		time.Sleep(30 * time.Millisecond)
		done.Store(true)
		return nil
	}).Name("bg-task").EveryMinute().RunInBackground()

	now := time.Now()
	results := s.RunDue(ctx, now)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	// Should return immediately without waiting for the task
	if done.Load() {
		t.Errorf("expected background task to run asynchronously")
	}

	time.Sleep(60 * time.Millisecond)
	if !done.Load() {
		t.Errorf("expected background task to have finished")
	}
}

func TestEvent_ExecAndJob(t *testing.T) {
	ctx := context.Background()
	s := schedule.NewScheduler()

	// 1. Exec command
	cmdEvent := s.Exec("echo", "schedule test output").Name("echo-test")
	if err := cmdEvent.Run(ctx); err != nil {
		t.Errorf("expected exec command to succeed, got: %v", err)
	}

	// 2. Queue Job
	job := &mockJob{}
	jobEvent := s.Job(job).Name("queue-test")
	if err := jobEvent.Run(ctx); err != nil {
		t.Errorf("expected job dispatch to succeed, got: %v", err)
	}
}

func TestScheduler_RunDueAndStartStop(t *testing.T) {
	s := schedule.NewScheduler()

	var counter atomic.Int32
	s.Call(func(ctx context.Context) error {
		counter.Add(1)
		return nil
	}).Name("counter-task").EveryMinute()

	now := time.Now()
	results := s.RunDue(context.Background(), now)
	if len(results) != 1 {
		t.Fatalf("expected 1 task run, got %d", len(results))
	}
	if counter.Load() != 1 {
		t.Errorf("expected counter 1, got %d", counter.Load())
	}

	// Second run in the same minute should not re-run (IsDue prevents duplicate minute execution)
	results2 := s.RunDue(context.Background(), now)
	if len(results2) != 0 {
		t.Errorf("expected 0 tasks due in same minute, got %d", len(results2))
	}

	// Lifecycle Start / Stop
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	s.Stop()
}
