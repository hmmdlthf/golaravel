package schedule

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"log/slog"
	"github.com/golaravel/golaravel/pkg/cache"
	"github.com/golaravel/golaravel/pkg/queue"
)

// ScheduledTask is an alias for Event for backward compatibility.
type ScheduledTask = Event

// Event represents a scheduled task registered with the scheduler.
type Event struct {
	mu sync.RWMutex

	name               string
	description        string
	cronExpr           string
	cron               *CronExpression
	timezone           *time.Location
	withoutOverlapping bool
	onOneServer        bool
	runInBackground    bool
	lockTimeout        time.Duration

	action      func(ctx context.Context) error
	command     string
	commandArgs []string
	job         queue.Job

	filters      []func() bool
	rejects      []func() bool
	beforeHooks  []func()
	afterHooks   []func()
	successHooks []func()
	failureHooks []func(error)

	lastRun time.Time
}

// NewEvent creates a new Event with default minute schedule.
func NewEvent() *Event {
	e := &Event{
		timezone:    time.Local,
		lockTimeout: 1 * time.Hour,
	}
	_ = e.EveryMinute()
	return e
}

// Name assigns a unique human-readable identifier to the scheduled event.
func (e *Event) Name(name string) *Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.name = name
	return e
}

// Description adds explanatory documentation to the scheduled event.
func (e *Event) Description(desc string) *Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.description = desc
	return e
}

// GetName returns the event name or an inferred fallback.
func (e *Event) GetName() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.name != "" {
		return e.name
	}
	if e.command != "" {
		return e.command
	}
	if e.job != nil {
		return fmt.Sprintf("job:%T", e.job)
	}
	if e.description != "" {
		return e.description
	}
	return "closure"
}

// GetDescription returns the event description.
func (e *Event) GetDescription() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.description
}

// GetCron returns the parsed CronExpression.
func (e *Event) GetCron() *CronExpression {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cron
}

// GetCronExpression returns the raw cron expression string.
func (e *Event) GetCronExpression() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cronExpr
}

// GetTimezone returns the configured timezone location.
func (e *Event) GetTimezone() *time.Location {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.timezone != nil {
		return e.timezone
	}
	return time.Local
}

// Timezone sets the execution timezone for evaluating schedule times.
func (e *Event) Timezone(tz string) *Event {
	loc, err := time.LoadLocation(tz)
	if err == nil {
		e.mu.Lock()
		e.timezone = loc
		if e.cronExpr != "" {
			e.cron, _ = ParseCron(e.cronExpr, loc)
		}
		e.mu.Unlock()
	}
	return e
}

// Cron sets a custom 5-field cron expression.
func (e *Event) Cron(expr string) *Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cronExpr = expr
	c, err := ParseCron(expr, e.timezone)
	if err == nil {
		e.cron = c
	}
	return e
}

// EveryMinute schedules the task to run once every minute.
func (e *Event) EveryMinute() *Event {
	return e.Cron("* * * * *")
}

// EveryTwoMinutes schedules the task to run once every 2 minutes.
func (e *Event) EveryTwoMinutes() *Event {
	return e.Cron("*/2 * * * *")
}

// EveryFiveMinutes schedules the task to run once every 5 minutes.
func (e *Event) EveryFiveMinutes() *Event {
	return e.Cron("*/5 * * * *")
}

// EveryTenMinutes schedules the task to run once every 10 minutes.
func (e *Event) EveryTenMinutes() *Event {
	return e.Cron("*/10 * * * *")
}

// EveryFifteenMinutes schedules the task to run once every 15 minutes.
func (e *Event) EveryFifteenMinutes() *Event {
	return e.Cron("*/15 * * * *")
}

// EveryThirtyMinutes schedules the task to run once every 30 minutes.
func (e *Event) EveryThirtyMinutes() *Event {
	return e.Cron("*/30 * * * *")
}

// Hourly schedules the task to run at the start of every hour.
func (e *Event) Hourly() *Event {
	return e.Cron("0 * * * *")
}

// HourlyAt schedules the task to run once an hour at the given minute offset.
func (e *Event) HourlyAt(minute int) *Event {
	return e.Cron(fmt.Sprintf("%d * * * *", minute))
}

// Daily schedules the task to run once daily at midnight (00:00).
func (e *Event) Daily() *Event {
	return e.Cron("0 0 * * *")
}

// DailyAt schedules the task to run daily at a specified time ("HH:MM" e.g., "14:30").
func (e *Event) DailyAt(timeStr string) *Event {
	var hour, min int
	_, err := fmt.Sscanf(timeStr, "%d:%d", &hour, &min)
	if err != nil {
		return e.Cron("0 0 * * *")
	}
	return e.Cron(fmt.Sprintf("%d %d * * *", min, hour))
}

// TwiceDaily schedules the task to run twice daily at specified hours.
func (e *Event) TwiceDaily(firstHour, secondHour int) *Event {
	return e.Cron(fmt.Sprintf("0 %d,%d * * *", firstHour, secondHour))
}

// Weekly schedules the task to run once weekly on Sunday at midnight.
func (e *Event) Weekly() *Event {
	return e.Cron("0 0 * * 0")
}

// WeeklyOn schedules the task to run weekly on a specific day and time.
func (e *Event) WeeklyOn(day time.Weekday, timeStr string) *Event {
	var hour, min int
	_, err := fmt.Sscanf(timeStr, "%d:%d", &hour, &min)
	if err != nil {
		hour, min = 0, 0
	}
	return e.Cron(fmt.Sprintf("%d %d * * %d", min, hour, int(day)))
}

// Monthly schedules the task to run once a month on the 1st at midnight.
func (e *Event) Monthly() *Event {
	return e.Cron("0 0 1 * *")
}

// MonthlyOn schedules the task to run on a specific day of the month and time.
func (e *Event) MonthlyOn(day int, timeStr string) *Event {
	var hour, min int
	_, err := fmt.Sscanf(timeStr, "%d:%d", &hour, &min)
	if err != nil {
		hour, min = 0, 0
	}
	return e.Cron(fmt.Sprintf("%d %d %d * *", min, hour, day))
}

// Quarterly schedules the task to run on the 1st of Jan, Apr, Jul, Oct.
func (e *Event) Quarterly() *Event {
	return e.Cron("0 0 1 1,4,7,10 *")
}

// Yearly schedules the task to run once a year on January 1st at midnight.
func (e *Event) Yearly() *Event {
	return e.Cron("0 0 1 1 *")
}

// Weekdays restricts the task to run only on Monday through Friday.
func (e *Event) Weekdays() *Event {
	return e.Days(time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday)
}

// Weekends restricts the task to run only on Saturday and Sunday.
func (e *Event) Weekends() *Event {
	return e.Days(time.Saturday, time.Sunday)
}

// Mondays restricts task execution to Mondays.
func (e *Event) Mondays() *Event { return e.Days(time.Monday) }

// Tuesdays restricts task execution to Tuesdays.
func (e *Event) Tuesdays() *Event { return e.Days(time.Tuesday) }

// Wednesdays restricts task execution to Wednesdays.
func (e *Event) Wednesdays() *Event { return e.Days(time.Wednesday) }

// Thursdays restricts task execution to Thursdays.
func (e *Event) Thursdays() *Event { return e.Days(time.Thursday) }

// Fridays restricts task execution to Fridays.
func (e *Event) Fridays() *Event { return e.Days(time.Friday) }

// Saturdays restricts task execution to Saturdays.
func (e *Event) Saturdays() *Event { return e.Days(time.Saturday) }

// Sundays restricts task execution to Sundays.
func (e *Event) Sundays() *Event { return e.Days(time.Sunday) }

// Days restricts execution to specific weekdays.
func (e *Event) Days(days ...time.Weekday) *Event {
	if len(days) == 0 {
		return e
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	dayStrs := make([]string, len(days))
	for i, d := range days {
		dayStrs[i] = fmt.Sprintf("%d", int(d))
	}

	// Update 5th field of cron expression
	fields := []string{"*", "*", "*", "*", "*"}
	if e.cronExpr != "" {
		cur := splitFields(e.cronExpr)
		if len(cur) == 5 {
			fields = cur
		}
	}
	fields[4] = joinFields(dayStrs, ",")
	newExpr := joinFields(fields, " ")
	e.cronExpr = newExpr
	e.cron, _ = ParseCron(newExpr, e.timezone)
	return e
}

// Between restricts the schedule to run only between startTime and endTime ("HH:MM").
func (e *Event) Between(startTime, endTime string) *Event {
	var startH, startM, endH, endM int
	_, err1 := fmt.Sscanf(startTime, "%d:%d", &startH, &startM)
	_, err2 := fmt.Sscanf(endTime, "%d:%d", &endH, &endM)
	if err1 != nil || err2 != nil {
		return e
	}

	startMinutes := startH*60 + startM
	endMinutes := endH*60 + endM

	return e.When(func() bool {
		now := time.Now()
		if e.timezone != nil {
			now = now.In(e.timezone)
		}
		curMinutes := now.Hour()*60 + now.Minute()
		if startMinutes <= endMinutes {
			return curMinutes >= startMinutes && curMinutes <= endMinutes
		}
		// Wrap around midnight
		return curMinutes >= startMinutes || curMinutes <= endMinutes
	})
}

// UnlessBetween prevents the schedule from running between startTime and endTime ("HH:MM").
func (e *Event) UnlessBetween(startTime, endTime string) *Event {
	var startH, startM, endH, endM int
	_, err1 := fmt.Sscanf(startTime, "%d:%d", &startH, &startM)
	_, err2 := fmt.Sscanf(endTime, "%d:%d", &endH, &endM)
	if err1 != nil || err2 != nil {
		return e
	}

	startMinutes := startH*60 + startM
	endMinutes := endH*60 + endM

	return e.Skip(func() bool {
		now := time.Now()
		if e.timezone != nil {
			now = now.In(e.timezone)
		}
		curMinutes := now.Hour()*60 + now.Minute()
		if startMinutes <= endMinutes {
			return curMinutes >= startMinutes && curMinutes <= endMinutes
		}
		// Wrap around midnight
		return curMinutes >= startMinutes || curMinutes <= endMinutes
	})
}

// WithoutOverlapping prevents overlapping runs using distributed atomic cache locks.
func (e *Event) WithoutOverlapping(timeout ...time.Duration) *Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.withoutOverlapping = true
	if len(timeout) > 0 && timeout[0] > 0 {
		e.lockTimeout = timeout[0]
	}
	return e
}

// OnOneServer ensures single-node execution in multi-server clusters using atomic locks.
func (e *Event) OnOneServer(timeout ...time.Duration) *Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.onOneServer = true
	if len(timeout) > 0 && timeout[0] > 0 {
		e.lockTimeout = timeout[0]
	}
	return e
}

// RunInBackground indicates the task should run asynchronously in a detached worker goroutine.
func (e *Event) RunInBackground() *Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.runInBackground = true
	return e
}

// When attaches a conditional truth test. The task will only execute if all filters return true.
func (e *Event) When(fn func() bool) *Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.filters = append(e.filters, fn)
	return e
}

// Skip attaches a conditional rejection test. The task will be skipped if any reject returns true.
func (e *Event) Skip(fn func() bool) *Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rejects = append(e.rejects, fn)
	return e
}

// Before registers a lifecycle hook invoked immediately before execution starts.
func (e *Event) Before(fn func()) *Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.beforeHooks = append(e.beforeHooks, fn)
	return e
}

// After registers a lifecycle hook invoked immediately after execution finishes.
func (e *Event) After(fn func()) *Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.afterHooks = append(e.afterHooks, fn)
	return e
}

// OnSuccess registers a callback invoked if the task completes without error.
func (e *Event) OnSuccess(fn func()) *Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.successHooks = append(e.successHooks, fn)
	return e
}

// OnFailure registers a callback invoked if the task returns an error.
func (e *Event) OnFailure(fn func(err error)) *Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.failureHooks = append(e.failureHooks, fn)
	return e
}

// IsWithoutOverlapping returns whether overlapping execution is disabled.
func (e *Event) IsWithoutOverlapping() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.withoutOverlapping
}

// IsOnOneServer returns whether execution is restricted to one server.
func (e *Event) IsOnOneServer() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.onOneServer
}

// IsRunInBackground returns whether task runs asynchronously.
func (e *Event) IsRunInBackground() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.runInBackground
}

// LockKey returns a deterministic unique key for distributed locking.
func (e *Event) LockKey() string {
	ident := e.GetName()
	e.mu.RLock()
	cron := e.cronExpr
	e.mu.RUnlock()
	if cron != "" {
		ident += ":" + cron
	}
	hash := sha256.Sum256([]byte(ident))
	return hex.EncodeToString(hash[:8])
}

// IsDue checks if the event should run at the given timestamp.
func (e *Event) IsDue(t time.Time) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.cron == nil {
		return false
	}

	loc := e.timezone
	if loc == nil {
		loc = time.Local
	}
	tInLoc := t.In(loc)

	// Don't run multiple times in the same minute
	if !e.lastRun.IsZero() {
		lastInLoc := e.lastRun.In(loc)
		if lastInLoc.Year() == tInLoc.Year() &&
			lastInLoc.Month() == tInLoc.Month() &&
			lastInLoc.Day() == tInLoc.Day() &&
			lastInLoc.Hour() == tInLoc.Hour() &&
			lastInLoc.Minute() == tInLoc.Minute() {
			return false
		}
	}

	return e.cron.Matches(tInLoc)
}

// Next calculates the next occurrence after the given time.
func (e *Event) Next(t time.Time) time.Time {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.cron == nil {
		return time.Time{}
	}
	return e.cron.Next(t)
}

// Run executes the scheduled event with full locking, conditional tests, and hooks.
func (e *Event) Run(ctx context.Context) error {
	e.mu.Lock()
	e.lastRun = time.Now()
	withoutOverlapping := e.withoutOverlapping
	onOneServer := e.onOneServer
	timeout := e.lockTimeout
	filters := append([]func() bool{}, e.filters...)
	rejects := append([]func() bool{}, e.rejects...)
	beforeHooks := append([]func(){}, e.beforeHooks...)
	afterHooks := append([]func(){}, e.afterHooks...)
	successHooks := append([]func(){}, e.successHooks...)
	failureHooks := append([]func(error){}, e.failureHooks...)
	action := e.action
	cmdName := e.command
	cmdArgs := e.commandArgs
	queueJob := e.job
	e.mu.Unlock()

	lockKey := e.LockKey()

	// 1. Evaluate conditional filters
	for _, fn := range filters {
		if !fn() {
			return nil
		}
	}
	for _, fn := range rejects {
		if fn() {
			return nil
		}
	}

	// 2. OnOneServer distributed check
	// Ensures only one cluster node executes this task during its minute window.
	var serverLock *cache.AtomicLock
	if onOneServer {
		minuteStamp := time.Now().UTC().Format("200601021504")
		serverLockKey := fmt.Sprintf("schedule:server:%s:%s", lockKey, minuteStamp)
		serverLock = cache.Lock(serverLockKey, 55*time.Second)
		acquired, err := serverLock.Acquire()
		if err != nil || !acquired {
			// Another node has claimed execution for this minute window
			return nil
		}
	}

	// 3. WithoutOverlapping concurrency check
	var overlapLock *cache.AtomicLock
	if withoutOverlapping {
		overlapKey := fmt.Sprintf("schedule:overlap:%s", lockKey)
		overlapLock = cache.Lock(overlapKey, timeout)
		acquired, err := overlapLock.Acquire()
		if err != nil || !acquired {
			return errors.New("task already running and overlapping is disabled")
		}
		defer func() {
			_ = overlapLock.Release()
		}()
	}

	// 4. Trigger before hooks
	for _, hook := range beforeHooks {
		hook()
	}

	// 5. Execute payload
	var runErr error
	if action != nil {
		runErr = action(ctx)
	} else if cmdName != "" {
		cmd := exec.CommandContext(ctx, cmdName, cmdArgs...)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		runErr = cmd.Run()
		if runErr != nil && out.Len() > 0 {
			runErr = fmt.Errorf("%w: %s", runErr, out.String())
		}
	} else if queueJob != nil {
		runErr = queue.Push(queueJob)
	}

	// 6. Trigger after hooks
	for _, hook := range afterHooks {
		hook()
	}

	// 7. Trigger success or failure hooks
	if runErr == nil {
		for _, hook := range successHooks {
			hook()
		}
	} else {
		for _, hook := range failureHooks {
			hook(runErr)
		}
		slog.Error("Scheduled event failed", "task", e.GetName(), "error", runErr)
	}

	return runErr
}

func splitFields(s string) []string {
	var res []string
	for _, f := range splitWhitespace(s) {
		res = append(res, f)
	}
	return res
}

func splitWhitespace(s string) []string {
	var words []string
	cur := ""
	for _, r := range s {
		if r == ' ' || r == '\t' {
			if cur != "" {
				words = append(words, cur)
				cur = ""
			}
		} else {
			cur += string(r)
		}
	}
	if cur != "" {
		words = append(words, cur)
	}
	return words
}

func joinFields(items []string, sep string) string {
	res := ""
	for i, item := range items {
		if i > 0 {
			res += sep
		}
		res += item
	}
	return res
}
