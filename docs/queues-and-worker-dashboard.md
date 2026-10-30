# Redis Queue Driver & Worker Dashboard (pkg/queue) (PRD §6) 🚀

GoLaravel provides a high-throughput, enterprise-grade asynchronous background job processing system modeled after Laravel's Queue and Horizon architectures, completely reimagined in Go standard library with zero third-party dependencies.

---

## 🌟 Key Features

- **Pluggable Driver Architecture**: Seamlessly switch between in-memory priority channels (`MemoryDriver`) for zero-overhead local development/tests and distributed Redis clusters (`RedisDriver`) for production.
- **Zero-Dependency Redis RESP Client**: Built-in pure Go Redis Serialization Protocol (RESP2/RESP3) client with TCP connection pooling, keepalive, auth, and sub-millisecond atomic operations (`LPUSH`, `BRPOP`, `LLEN`, `ZADD`, `ZRANGEBYSCORE`, `HSET`, `HGETALL`).
- **Multi-Queue Priority Polling**: Strict priority scheduling (e.g. `high` -> `default` -> `low`) where workers guarantee higher-priority queues are exhausted before consuming lower-priority queues.
- **Extensible Job Interfaces**:
  - `Queueable`: Custom queue targeting per job.
  - `Delayable`: Time-delayed executions (`Later(delay, job)`).
  - `Retryable`: Configurable max tries and custom backoff curves.
  - `Timeoutable`: Strict execution context cancellation deadlines.
- **Dead-Letter Queue (DLQ) & Failed Job Storage**: Permanent audit trail of exhausted retries with full payloads, exception stack traces, and failure timestamps stored in memory or Redis hashes (`FailedJobStore`).
- **Laravel Horizon-Equivalent Worker Dashboard**:
  - Real-time responsive Tailwind web console (`GET /queue/dashboard`).
  - Active worker inspection, queue depths, throughput per minute, and live job history.
  - Dead-Letter Queue management with one-click individual retry, batch retry-all, and flush actions.
- **Artisan CLI Operations**:
  - `artisan queue:work --queue=high,default,low --concurrency=4 --driver=redis`
  - `artisan queue:failed`
  - `artisan queue:retry <id|all>`
  - `artisan queue:flush`

---

## 🏗️ Architecture Overview

```
                                +-------------------+
                                |   golaravel.Queue |
                                +---------+---------+
                                          |
                      +-------------------+-------------------+
                      |                                       |
              Immediate Dispatch                      Delayed Dispatch
          Queue.Push(job) / OnQueue()             Queue.Later(duration, job)
                      |                                       |
                      v                                       v
         +-------------------------+             +-------------------------+
         |     Driver.Push()       |             |     Driver.Later()      |
         |  (Memory or Redis List) |             |  (Memory or Redis ZSet) |
         +------------+------------+             +------------+------------+
                      |                                       |
                      |                                       v
                      |                         Background Delay Scheduler
                      |                       (Promotes expired to ready)
                      v                                       |
         +----------------------------------------------------+
         |
         v
+------------------+     Priority Polling      +----------------------------+
|   Worker Pool    | <======================== | Queue Driver (high->low)   |
+--------+---------+                           +----------------------------+
         |
         |---> Execution Timeout (`Timeoutable`)
         |---> Automatic Backoff Retry (`Retryable`)
         |
         +---> If Exceeded Max Attempts
                      |
                      v
         +-------------------------+
         |   Dead-Letter Queue     | <=====> Web Dashboard & Artisan CLI
         |   (FailedJobStore)      |         (/queue/dashboard, queue:failed)
         +-------------------------+
```

---

## 📦 Defining Background Jobs

A GoLaravel background job is any Go struct that implements `Handle(ctx context.Context) error`.

You can scaffold new jobs instantly using Artisan:
```bash
artisan make:job SendWelcomeEmail
```

### Basic Job

```go
package jobs

import (
    "context"
    "fmt"
    "time"
)

type SendWelcomeEmailJob struct {
    UserID string `json:"user_id"`
    Email  string `json:"email"`
}

func (j *SendWelcomeEmailJob) Handle(ctx context.Context) error {
    fmt.Printf("Sending welcome email to %s (User ID: %s)...\n", j.Email, j.UserID)
    return nil
}
```

### Priority, Delayed, Retryable & Timeoutable Job

Implement optional job interfaces to control queue behavior:

```go
package jobs

import (
    "context"
    "time"
)

type ProcessVideoJob struct {
    VideoID string `json:"video_id"`
}

// Queueable: routes this job to the "high" priority queue
func (j *ProcessVideoJob) Queue() string {
    return "high"
}

// Delayable: delays execution by 30 seconds
func (j *ProcessVideoJob) Delay() time.Duration {
    return 30 * time.Second
}

// Retryable: attempts up to 5 times with exponential backoff
func (j *ProcessVideoJob) MaxTries() int {
    return 5
}

func (j *ProcessVideoJob) Backoff() time.Duration {
    return 5 * time.Second
}

// Timeoutable: cancels the execution context if it exceeds 2 minutes
func (j *ProcessVideoJob) Timeout() time.Duration {
    return 2 * time.Minute
}

func (j *ProcessVideoJob) Handle(ctx context.Context) error {
    select {
    case <-time.After(10 * time.Second):
        return nil
    case <-ctx.Done():
        return ctx.Err()
    }
}
```

---

## ⚡ Dispatching Jobs

Jobs can be dispatched using the top-level `golaravel.Queue` facade or the `pkg/queue` package directly:

### 1. Immediate Dispatch
```go
import "github.com/golaravel/golaravel"

// Dispatched to default queue
err := golaravel.Queue.Push(&jobs.SendWelcomeEmailJob{
    UserID: "usr_100",
    Email:  "user@example.com",
})
```

### 2. Time-Delayed Dispatch
```go
// Dispatched to execute in 5 minutes
err := golaravel.Queue.Later(5*time.Minute, &jobs.SendWelcomeEmailJob{
    UserID: "usr_100",
    Email:  "user@example.com",
})
```

### 3. Fluent Multi-Queue Chaining
```go
// Explicitly target high priority with custom max tries and delay
err := golaravel.Queue.OnQueue("high").
    Delay(10 * time.Second).
    MaxTries(5).
    Push(&jobs.ProcessVideoJob{VideoID: "vid_994"})
```

---

## 🛠️ Configuration & Drivers

Drivers are automatically configured based on standard environment variables:

| Variable | Description | Default |
|---|---|---|
| `QUEUE_CONNECTION` | Backend driver: `memory` or `redis` | `memory` |
| `REDIS_HOST` | Redis host address | `127.0.0.1` |
| `REDIS_PORT` | Redis TCP port | `6379` |
| `REDIS_PASSWORD` | Optional Redis password authentication | `""` |

### Manual Driver Switching in Code

```go
import "github.com/golaravel/golaravel/pkg/queue"

// Switch to Redis driver manually
driver, err := queue.NewRedisDriver(queue.RedisConfig{
    Addr:     "127.0.0.1:6379",
    Password: "secret-password",
    Prefix:   "myapp:queue:",
})
if err != nil {
    log.Fatal(err)
}

dlq := queue.NewRedisFailedJobStore(queue.RedisConfig{
    Addr:     "127.0.0.1:6379",
    Password: "secret-password",
    Prefix:   "myapp:queue:",
})

q := queue.DefaultQueue()
q.SetDriver(driver)
q.SetDLQ(dlq)
```

---

## 🖥️ Horizon Worker Dashboard

GoLaravel includes an out-of-the-box real-time web console inspired by Laravel Horizon.

### Mounting the Dashboard

Mount the dashboard routes to your application router:

```go
package main

import (
    "github.com/golaravel/golaravel"
    "github.com/golaravel/golaravel/pkg/queue"
)

func main() {
    app := golaravel.New()

    // Mounts UI at /queue/dashboard and JSON APIs at /queue/api/*
    queue.MountDashboard(app, "/queue")

    app.Run(":8080")
}
```

### Accessing the Dashboard

Open `http://localhost:8080/queue/dashboard` in your browser:

- **Metrics Cards**: Displays active worker count, queue depths across priority levels, total processed, total failed, and system uptime.
- **Live Worker Status Table**: Real-time worker goroutine state (`idle`, `busy`), current active job type, and worker processed count.
- **Dead-Letter Queue (DLQ) Manager**: Formatted table of permanently failed jobs, error stack traces, and failure timestamps with interactive buttons:
  - **Retry**: Re-queues the failed job immediately.
  - **Retry All**: Re-queues all failed jobs in batch.
  - **Flush**: Clears all failed jobs from the DLQ.

### JSON API Endpoints

The dashboard provides REST endpoints for headless integrations and custom monitoring systems:

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/queue/dashboard` | Tailwind HTML web dashboard UI |
| `GET` | `/queue/api/stats` | JSON snapshot of workers, metrics, and queue sizes |
| `GET` | `/queue/api/failed` | JSON array of paginated Dead-Letter Queue items |
| `POST` | `/queue/api/retry/:id` | Re-queues a specific failed job |
| `POST` | `/queue/api/retry-all` | Re-queues all jobs in Dead-Letter Queue |
| `DELETE` | `/queue/api/failed/:id` | Deletes a failed job record |
| `POST` | `/queue/api/flush` | Purges all failed jobs |

---

## 💻 Artisan CLI Operations

Manage queue workers and dead-letter queues directly from your terminal:

### 1. Launching Worker Pool
```bash
# Default: memory driver, concurrency 2, polling high -> default -> low
artisan queue:work

# Production: Redis driver with 8 concurrent workers
artisan queue:work --driver=redis --concurrency=8 --queue=high,default,low
```

### 2. Inspecting Failed Jobs
```bash
artisan queue:failed
```
*Output:*
```
Dead-Letter Queue (2 failed jobs):
  ID                  QUEUE    TYPE            FAILED AT            ERROR
  --                  -----    ----            ---------            -----
  job_1730312001      high     ProcessVideo    2026-10-30 21:00:01  ffmpeg: memory limit exceeded
  job_1730312055      default  SendWelcomeJob  2026-10-30 21:00:55  dial tcp 10.0.0.1:25: connect: connection refused
```

### 3. Retrying Failed Jobs
```bash
# Retry a single job
artisan queue:retry job_1730312001

# Retry all failed jobs
artisan queue:retry all
```

### 4. Flushing Dead-Letter Queue
```bash
artisan queue:flush
```

---

## 🧪 Testing Queues

Because `MemoryDriver` and `WorkerPool` are completely decoupled, testing background queues is fast, synchronous or asynchronous, and requires zero external containers:

```go
func TestWelcomeEmailJob(t *testing.T) {
    q := queue.NewQueue()
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // Start in-memory worker
    q.StartWorkers(ctx, 1, "default")

    // Dispatch job
    err := q.Push(&SendWelcomeEmailJob{UserID: "usr_42", Email: "test@example.com"})
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }

    // Wait briefly for worker execution
    time.Sleep(50 * time.Millisecond)

    // Verify DLQ has no errors
    count, _ := q.DLQ().Count()
    if count != 0 {
        t.Fatalf("expected 0 failed jobs, got %d", count)
    }

    q.Stop()
}
```
