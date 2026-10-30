package mail

import (
	"sync"
	"testing"
)

var (
	testMu         sync.Mutex
	previousDriver Driver
)

// Fake swaps the active mail driver with an in-memory test driver and returns it.
func Fake() *MemoryDriver {
	testMu.Lock()
	defer testMu.Unlock()

	previousDriver = GetDriver()
	mem := NewMemoryDriver()
	SetDriver(mem)
	return mem
}

// Restore restores the driver that was active before Fake() was invoked.
func Restore() {
	testMu.Lock()
	defer testMu.Unlock()

	if previousDriver != nil {
		SetDriver(previousDriver)
		previousDriver = nil
	} else {
		SetDriver(&LogDriver{})
	}
}

// AssertSent asserts that at least one sent message matches the provided filter.
func AssertSent(t *testing.T, filter func(m *Message) bool) {
	t.Helper()
	d, ok := GetDriver().(*MemoryDriver)
	if !ok {
		t.Fatalf("mail.AssertSent requires mail.Fake() to be active")
	}

	for _, msg := range d.Sent() {
		if filter(msg) {
			return
		}
	}
	t.Fatalf("expected matching email was not sent. Total emails sent: %d", d.Count())
}

// AssertSentCount asserts that exactly expected number of emails were sent.
func AssertSentCount(t *testing.T, expected int) {
	t.Helper()
	d, ok := GetDriver().(*MemoryDriver)
	if !ok {
		t.Fatalf("mail.AssertSentCount requires mail.Fake() to be active")
	}

	actual := d.Count()
	if actual != expected {
		t.Fatalf("expected %d emails to be sent, but got %d", expected, actual)
	}
}

// AssertNotSent asserts that no email matching the filter was sent.
func AssertNotSent(t *testing.T, filter ...func(m *Message) bool) {
	t.Helper()
	d, ok := GetDriver().(*MemoryDriver)
	if !ok {
		t.Fatalf("mail.AssertNotSent requires mail.Fake() to be active")
	}

	if len(filter) == 0 {
		if d.Count() > 0 {
			t.Fatalf("expected no emails to be sent, but found %d", d.Count())
		}
		return
	}

	fn := filter[0]
	for _, msg := range d.Sent() {
		if fn(msg) {
			t.Fatalf("an email matching the rejection filter was sent: subject='%s', to=%v", msg.SubjectLine, msg.ToAddresses)
		}
	}
}
