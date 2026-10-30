package notification_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/golaravel/golaravel/pkg/broadcast"
	"github.com/golaravel/golaravel/pkg/mail"
	"github.com/golaravel/golaravel/pkg/notification"
)

type TestUser struct {
	ID    int
	Email string
	Phone string
}

func (u *TestUser) RouteNotificationFor(channel string) string {
	switch channel {
	case "mail":
		return u.Email
	case "database":
		return fmt.Sprintf("%d", u.ID)
	case "broadcast":
		return fmt.Sprintf("users.%d", u.ID)
	case "sms":
		return u.Phone
	case "slack":
		return "" // Uses default webhook
	default:
		return ""
	}
}

type InvoicePaidNotification struct {
	InvoiceNumber string
	Amount        float64
}

func (n *InvoicePaidNotification) Via(notifiable notification.Notifiable) []string {
	return []string{"mail", "database", "broadcast", "slack", "sms"}
}

func (n *InvoicePaidNotification) ToMail(notifiable notification.Notifiable) (*mail.Message, error) {
	return mail.NewMessage().
		Subject("Payment Received: " + n.InvoiceNumber).
		HTML(fmt.Sprintf("<h1>Thank you!</h1><p>Your payment of $%.2f was confirmed.</p>", n.Amount)), nil
}

func (n *InvoicePaidNotification) ToDatabase(notifiable notification.Notifiable) (*notification.DatabaseMessage, error) {
	return &notification.DatabaseMessage{
		Type: "InvoicePaid",
		Data: map[string]any{
			"invoice_number": n.InvoiceNumber,
			"amount":         n.Amount,
		},
	}, nil
}

func (n *InvoicePaidNotification) ToBroadcast(notifiable notification.Notifiable) (*broadcast.Message, error) {
	return &broadcast.Message{
		Event: "invoice.paid",
		Data: map[string]any{
			"invoice_number": n.InvoiceNumber,
			"amount":         n.Amount,
		},
	}, nil
}

func (n *InvoicePaidNotification) ToSlack(notifiable notification.Notifiable) (*notification.SlackMessage, error) {
	return &notification.SlackMessage{
		Text: fmt.Sprintf(":white_check_mark: Invoice *%s* was paid ($%.2f)", n.InvoiceNumber, n.Amount),
	}, nil
}

func (n *InvoicePaidNotification) ToSMS(notifiable notification.Notifiable) (*notification.SMSMessage, error) {
	return &notification.SMSMessage{
		Body: fmt.Sprintf("Payment received for %s: $%.2f. Thank you!", n.InvoiceNumber, n.Amount),
	}, nil
}

type MockSMSDriver struct {
	mu       sync.Mutex
	messages []*notification.SMSMessage
}

func (d *MockSMSDriver) SendSMS(ctx context.Context, msg *notification.SMSMessage) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.messages = append(d.messages, msg)
	return nil
}

func TestMultiChannelNotification(t *testing.T) {
	ctx := context.Background()

	// 1. Mock Slack Webhook Server
	slackReceived := false
	slackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slackReceived = true
		w.WriteHeader(http.StatusOK)
	}))
	defer slackServer.Close()

	// 2. Mock SMS Driver
	mockSMS := &MockSMSDriver{}

	// 3. Setup Notification Manager
	mgr := notification.NewManager()
	mgr.RegisterChannel("slack", notification.NewSlackChannel(slackServer.URL))
	mgr.RegisterChannel("sms", notification.NewSMSChannel(mockSMS))

	// 4. Setup Broadcast Subscriber
	hub := broadcast.DefaultHub()
	subCh, unsub := hub.Subscribe("users.101")
	defer unsub()

	user := &TestUser{
		ID:    101,
		Email: "billing@acme.com",
		Phone: "+15550199",
	}

	notif := &InvoicePaidNotification{
		InvoiceNumber: "INV-2026-001",
		Amount:        3850.00,
	}

	// 5. Dispatch across all 5 channels!
	err := mgr.Send(ctx, []notification.Notifiable{user}, notif)
	if err != nil {
		t.Fatalf("unexpected error dispatching notification: %v", err)
	}

	// Verify Database Channel
	unread, err := mgr.DatabaseStore().GetUnread(ctx, "101")
	if err != nil {
		t.Fatalf("error retrieving unread notifications: %v", err)
	}
	if len(unread) != 1 {
		t.Fatalf("expected 1 in-app notification, got %d", len(unread))
	}
	if unread[0].Data["invoice_number"] != "INV-2026-001" {
		t.Errorf("unexpected invoice number in database notification: %v", unread[0].Data["invoice_number"])
	}

	// Mark as read
	if err := mgr.DatabaseStore().MarkAsRead(ctx, "101", unread[0].ID); err != nil {
		t.Fatalf("failed marking as read: %v", err)
	}
	unreadAfter, _ := mgr.DatabaseStore().GetUnread(ctx, "101")
	if len(unreadAfter) != 0 {
		t.Errorf("expected 0 unread notifications after markAsRead, got %d", len(unreadAfter))
	}

	// Verify Broadcast Channel (real-time message received)
	select {
	case msg := <-subCh:
		if msg.Event != "invoice.paid" {
			t.Errorf("expected event 'invoice.paid', got %s", msg.Event)
		}
		if msg.Data["invoice_number"] != "INV-2026-001" {
			t.Errorf("unexpected broadcast payload: %v", msg.Data)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for broadcast notification")
	}

	// Verify Slack Channel
	if !slackReceived {
		t.Errorf("expected Slack webhook to be delivered")
	}

	// Verify SMS Channel
	if len(mockSMS.messages) != 1 {
		t.Fatalf("expected 1 SMS delivered, got %d", len(mockSMS.messages))
	}
	if mockSMS.messages[0].To != "+15550199" {
		t.Errorf("expected SMS to +15550199, got %s", mockSMS.messages[0].To)
	}
}

func TestOnDemandNotification(t *testing.T) {
	ctx := context.Background()
	mgr := notification.NewManager()

	recipient := notification.OnDemand("mail", "audit@external-auditor.com")
	notif := &InvoicePaidNotification{
		InvoiceNumber: "INV-AUDIT-99",
		Amount:        500.00,
	}

	err := mgr.Send(ctx, []notification.Notifiable{recipient}, notif)
	if err == nil {
		// Note: mail channel succeeds because mock log driver is default
		t.Log("OnDemand mail delivery succeeded")
	}
}
