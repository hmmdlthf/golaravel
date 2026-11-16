package schedule

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestPanickingTaskDoesNotCrash(t *testing.T) {
	s := NewScheduler()
	s.Call(func(ctx context.Context) error { panic("boom") }).EveryMinute().Name("explodes")
	s.Call(func(ctx context.Context) error { panic("bg boom") }).EveryMinute().Name("bg").RunInBackground()
	res := s.RunDue(context.Background(), time.Now())
	found := false
	for _, r := range res {
		if r.Error != nil && strings.Contains(r.Error.Error(), "panicked") {
			found = true
		}
	}
	if !found {
		t.Fatalf("panic must become an error result: %+v", res)
	}
	time.Sleep(50 * time.Millisecond) // the background panic must not crash the test binary
}

func TestTailBufferKeepsTheEnd(t *testing.T) {
	b := &tailBuffer{max: 8}
	_, _ = b.Write([]byte("0123456789abcdef"))
	if b.String() != "89abcdef" {
		t.Fatalf("tail: %q", b.String())
	}
}
