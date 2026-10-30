# Queues & Task Scheduling ⏱️

GoLaravel includes an async background queue system and a fluent in-process cron scheduler.

---

## 1. Defining Background Jobs

Create a job using Artisan:
```bash
artisan make:job SendWelcomeEmail
```

Implement the `queue.Job` interface:

```go
package jobs

import (
	"context"
	"log/slog"
	"github.com/golaravel/golaravel/pkg/mail"
)

type SendWelcomeEmailJob struct {
	UserEmail string `json:"user_email"`
	UserName  string `json:"user_name"`
}

func (j *SendWelcomeEmailJob) Handle(ctx context.Context) error {
	slog.Info("Sending welcome email", "email", j.UserEmail)

	return mail.Send(mail.NewMessage().
		To(j.UserEmail).
		Subject("Welcome to GoLaravel!").
		HTML("<h1>Hello " + j.UserName + "!</h1>"),
	)
}
```

---

## 2. Dispatching Jobs

Dispatch jobs from any handler or service:

```go
import "github.com/golaravel/golaravel/pkg/queue"

func RegisterHandler(c *golaravel.Context) error {
	// ... create user ...

	// Dispatch job asynchronously to the background worker pool:
	_ = queue.Push(&jobs.SendWelcomeEmailJob{
		UserEmail: user.Email,
		UserName:  user.Name,
	})

	return c.JSON(201, user)
}
```

---

## 3. Running Queue Workers

Launch concurrent background workers using the Artisan CLI:

```bash
artisan queue:work
```

---

## 4. In-Process Task Scheduler (Cron Engine)

Replace external crontab files with a fluent Go scheduler:

```go
import "github.com/golaravel/golaravel/pkg/schedule"

// Schedule tasks with expressive intervals:
schedule.Call(func(ctx context.Context) error {
	return pruneExpiredTokens()
}).EveryMinute()

schedule.Call(func(ctx context.Context) error {
	return syncBillingStatus()
}).Hourly()

schedule.Call(func(ctx context.Context) error {
	return sendDailyDigest()
}).DailyAt("00:00")

// Start the scheduler ticker loop:
schedule.Start(context.Background())
```
