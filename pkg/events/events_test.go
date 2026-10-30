package events

import (
	"sync"
	"testing"
)

type UserRegistered struct {
	UserID string
	Email  string
}

func TestEvents_Sync(t *testing.T) {
	d := NewDispatcher()
	called := false

	d.Listen("user.registered", func(payload any) error {
		event := payload.(*UserRegistered)
		if event.UserID == "user_100" {
			called = true
		}
		return nil
	})

	err := d.Dispatch("user.registered", &UserRegistered{UserID: "user_100", Email: "u@example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !called {
		t.Fatal("expected listener to be called synchronously")
	}
}

func TestEvents_Async(t *testing.T) {
	d := NewDispatcher()
	var wg sync.WaitGroup
	wg.Add(1)

	d.Listen("invoice.generated", func(payload any) error {
		defer wg.Done()
		return nil
	})

	d.DispatchAsync("invoice.generated", "INV-100")
	wg.Wait()
}
