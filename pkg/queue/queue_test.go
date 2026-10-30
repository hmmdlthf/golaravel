package queue

import (
	"context"
	"sync"
	"testing"
	"time"
)

type SendWelcomeEmailJob struct {
	UserID string `json:"user_id"`
}

var emailJobProcessed = false
var emailJobMu sync.Mutex

func (j *SendWelcomeEmailJob) Handle(ctx context.Context) error {
	emailJobMu.Lock()
	defer emailJobMu.Unlock()
	emailJobProcessed = true
	return nil
}

func TestQueue_PushAndWork(t *testing.T) {
	q := NewQueue(100)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q.StartWorkers(ctx, 2)

	err := q.Push(&SendWelcomeEmailJob{UserID: "usr_99"})
	if err != nil {
		t.Fatalf("unexpected error pushing job: %v", err)
	}

	// Give worker time to process
	time.Sleep(50 * time.Millisecond)

	emailJobMu.Lock()
	processed := emailJobProcessed
	emailJobMu.Unlock()

	if !processed {
		t.Fatal("expected job to be processed by worker pool")
	}

	q.Stop()
}
