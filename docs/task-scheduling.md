# Advanced In-Process Cron Scheduler & Distributed Task Runner ⏱️

GoLaravel provides a pure Go, zero-dependency in-process cron scheduler and distributed task runner modeled after Laravel's task scheduling architecture. It combines full 5-part cron syntax parsing, fluent timing intervals, distributed atomic locking, multi-server single-node execution, and dedicated Artisan CLI commands.

---

## 1. Defining Scheduled Tasks

Register tasks in your application lifecycle (for example in `main.go` or an `app/console/kernel.go` setup function) using `schedule.Call`, `schedule.Exec`, or `schedule.Job`.

### Closures & Functions
```go
import (
    "context"
    "github.com/golaravel/golaravel/pkg/schedule"
)

schedule.Call(func(ctx context.Context) error {
    return pruneExpiredTokens()
}).Name("auth:prune-tokens").DailyAt("02:00")
```

### Shell / OS Commands
```go
schedule.Exec("pg_dump", "-U", "postgres", "production_db", "-f", "/backups/nightly.sql").
    Name("backup:database").
    DailyAt("03:30").
    RunInBackground()
```

### Queued Background Jobs
Automatically push background jobs into the queue system (`pkg/queue`) on schedule:
```go
schedule.Job(&jobs.GenerateWeeklyReportJob{}).
    Name("reports:weekly").
    WeeklyOn(time.Monday, "08:00")
```

---

## 2. Fluent Timing Directives

You can schedule tasks with standard 5-part cron syntax or expressive fluent helpers:

| Directive | Cron Equivalent | Description |
|---|---|---|
| `Cron("* * * * *")` | Custom | Arbitrary 5-field cron expression |
| `EveryMinute()` | `* * * * *` | Run every minute |
| `EveryTwoMinutes()` | `*/2 * * * *` | Run every 2 minutes |
| `EveryFiveMinutes()` | `*/5 * * * *` | Run every 5 minutes |
| `EveryTenMinutes()` | `*/10 * * * *` | Run every 10 minutes |
| `EveryFifteenMinutes()` | `*/15 * * * *` | Run every 15 minutes |
| `EveryThirtyMinutes()` | `*/30 * * * *` | Run every 30 minutes |
| `Hourly()` | `0 * * * *` | Run at the start of every hour |
| `HourlyAt(15)` | `15 * * * *` | Run hourly at minute 15 |
| `Daily()` | `0 0 * * *` | Run every day at midnight (00:00) |
| `DailyAt("14:30")` | `30 14 * * *` | Run every day at 14:30 |
| `TwiceDaily(1, 13)` | `0 1,13 * * *` | Run twice daily at 01:00 and 13:00 |
| `Weekly()` | `0 0 * * 0` | Run weekly on Sunday at midnight |
| `WeeklyOn(time.Friday, "18:00")` | `0 18 * * 5` | Run weekly on Friday at 18:00 |
| `Monthly()` | `0 0 1 * *` | Run monthly on the 1st at midnight |
| `MonthlyOn(15, "09:00")` | `0 9 15 * *` | Run monthly on the 15th at 09:00 |
| `Quarterly()` | `0 0 1 1,4,7,10 *`| Run on the 1st day of every quarter |
| `Yearly()` | `0 0 1 1 *` | Run annually on January 1st at 00:00 |
| `Weekdays()` | `* * * * 1-5` | Constrain to Monday through Friday |
| `Weekends()` | `* * * * 0,6` | Constrain to Saturday and Sunday |
| `Days(time.Monday, time.Wednesday)` | `* * * * 1,3` | Constrain to specific weekdays |
| `Timezone("America/New_York")` | N/A | Evaluate schedule in specified timezone |

### Window Constrains
Restrict tasks to certain times of day:
```go
schedule.Call(syncMetrics).
    EveryFiveMinutes().
    Between("08:00", "18:00")

schedule.Call(heavyETL).
    Hourly().
    UnlessBetween("09:00", "17:00") // Skip during peak business hours
```

---

## 3. Distributed Concurrency & Clustering

### `WithoutOverlapping(timeout ...time.Duration)`
Prevents a long-running task from executing if its previous run is still in progress. Uses atomic distributed locks (`pkg/cache` backed by Redis or Memory):
```go
schedule.Call(processLargeDataExport).
    Hourly().
    WithoutOverlapping(30 * time.Minute)
```

### `OnOneServer(timeout ...time.Duration)`
In high-availability, multi-node cloud deployments (e.g. Kubernetes with multiple replica pods running cron), `OnOneServer` guarantees that **only one server instance** executes the task during that schedule window:
```go
schedule.Call(chargeRecurringSubscriptions).
    DailyAt("00:01").
    OnOneServer()
```

### `RunInBackground()`
Runs the task inside a detached worker goroutine so long-running operations do not block other due tasks:
```go
schedule.Exec("python3", "ai_pipeline.py").
    EveryTenMinutes().
    RunInBackground()
```

---

## 4. Conditional Filters & Lifecycle Hooks

### Execution Conditionals
```go
schedule.Call(sendDailyEmailDigest).
    DailyAt("09:00").
    When(func() bool {
        return features.IsEnabled("daily_digests")
    }).
    Skip(func() bool {
        return holidayCalendar.IsHoliday(time.Now())
    })
```

### Lifecycle Callbacks
```go
schedule.Call(performNightlyCleanup).
    DailyAt("04:00").
    Before(func() {
        slog.Info("Starting nightly cleanup...")
    }).
    After(func() {
        slog.Info("Nightly cleanup finished.")
    }).
    OnSuccess(func() {
        metrics.Counter("nightly_cleanup_success").Inc()
    }).
    OnFailure(func(err error) {
        alertManager.NotifySlack("Nightly cleanup failed: " + err.Error())
    })
```

---

## 5. Artisan CLI Commands

GoLaravel includes three Artisan commands for managing and inspecting the scheduler:

### 1. `artisan schedule:list`
Displays a formatted ASCII table of all registered scheduled tasks, cron expressions, next due timestamps, timezones, and concurrency flags:

```bash
$ artisan schedule:list

GoLaravel Scheduled Tasks:
  TASK / COMMAND          CRON            NEXT DUE               TIMEZONE     OVERLAP    ONE SERVER
  --------------          ----            --------               --------     -------    ----------
  auth:prune-tokens       0 2 * * *       2026-10-31 02:00:00    Local        Allow      No
  backup:database         30 3 * * *      2026-10-31 03:30:00    Local        Allow      No
  reports:weekly          0 8 * * 1       2026-11-02 08:00:00    Local        Allow      No
  billing:charge          1 0 * * *       2026-10-31 00:01:00    Local        Block      Yes
```

### 2. `artisan schedule:run`
Executes all tasks due at the current minute. Designed to be invoked by your server's system cron daemon:

```bash
# Add this single entry to your server crontab (* * * * *):
* * * * * cd /var/www/app && ./artisan schedule:run >> /dev/null 2>&1
```

Terminal output:
```bash
$ artisan schedule:run

Running scheduled tasks [2026-10-30 22:30:00]...
  [22:30:00] auth:prune-tokens              DONE (0.042s)
  [22:30:00] billing:charge                 DONE (0.185s)

Summary: 2 executed, 2 succeeded, 0 failed.
```

### 3. `artisan schedule:work`
Runs an in-process daemon worker that executes `schedule:run` every minute. Ideal for local development, Docker containers, and serverless containers where crond is not installed:

```bash
$ artisan schedule:work

GoLaravel Schedule Worker Daemon started.
Running due tasks every minute. Press Ctrl+C to stop.

[22:31:00] Task 'auth:prune-tokens' DONE (0.038s)
[22:32:00] Task 'reports:cache' DONE (0.015s)
```

---

## 6. Programmatic In-Process Runner

If you prefer running the scheduler inside your main Go web service without external crond:

```go
func main() {
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // Register tasks
    schedule.Call(syncStatus).EveryMinute()
    schedule.Call(refreshFeeds).Hourly()

    // Launch ticker loop (ticks and runs due tasks every minute)
    schedule.Start(ctx)
    defer schedule.Stop()

    // Start HTTP server...
}
```
