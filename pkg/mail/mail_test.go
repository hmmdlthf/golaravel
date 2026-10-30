package mail

import (
	"testing"
)

type MockMailDriver struct {
	LastSent *Message
}

func (d *MockMailDriver) Send(msg *Message) error {
	d.LastSent = msg
	return nil
}

func TestMail_Send(t *testing.T) {
	mock := &MockMailDriver{}
	SetDriver(mock)

	msg := NewMessage().
		To("user@example.com").
		Subject("Welcome to GoLaravel").
		HTML("<h1>Welcome!</h1>")

	err := Send(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.LastSent == nil || mock.LastSent.SubjectLine != "Welcome to GoLaravel" {
		t.Fatalf("expected email to be captured by driver, got: %v", mock.LastSent)
	}
}
