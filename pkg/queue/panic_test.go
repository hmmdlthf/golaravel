package queue

import (
	"context"
	"strings"
	"testing"
)

type panicJob struct{}

func (panicJob) Handle(context.Context) error { panic("boom") }

func TestRunJobRecoversPanics(t *testing.T) {
	err := runJob(context.Background(), panicJob{})
	if err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("panic must become an error: %v", err)
	}
}
