package notification

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/goccy/go-json"
	"github.com/golaravel/golaravel/pkg/broadcast"
	"github.com/golaravel/golaravel/pkg/mail"
)

// Channel defines the delivery mechanism for a notification channel.
type Channel interface {
	Send(ctx context.Context, notifiable Notifiable, notification Notification) error
}

// ============================================================================
// 1. MAIL CHANNEL
// ============================================================================

// MailChannel delivers notifications via email using pkg/mail.
type MailChannel struct {
	driver mail.Driver
}

// NewMailChannel creates an email delivery channel.
func NewMailChannel(driver mail.Driver) *MailChannel {
	return &MailChannel{driver: driver}
}

func (c *MailChannel) Send(ctx context.Context, notifiable Notifiable, notification Notification) error {
	mailable, ok := notification.(MailableNotification)
	if !ok {
		return fmt.Errorf("notification %T does not implement MailableNotification", notification)
	}

	msg, err := mailable.ToMail(notifiable)
	if err != nil {
		return fmt.Errorf("failed preparing mail message: %w", err)
	}

	// Auto-route recipient address if not already set
	if len(msg.ToAddresses) == 0 {
		recipient := notifiable.RouteNotificationFor("mail")
		if recipient == "" {
			// Skip delivery if notifiable does not support mail
			return nil
		}
		msg.To(recipient)
	}

	if c.driver != nil {
		return c.driver.Send(msg)
	}
	return mail.Send(msg)
}

// ============================================================================
// 2. DATABASE CHANNEL (IN-APP ALERTS)
// ============================================================================

// DatabaseStore defines repository storage for in-app database notifications.
type DatabaseStore interface {
	Save(ctx context.Context, notifiableID string, msg *DatabaseMessage) error
	GetUnread(ctx context.Context, notifiableID string) ([]*DatabaseMessage, error)
	MarkAsRead(ctx context.Context, notifiableID, id string) error
}

// MemoryDatabaseStore provides a fast, thread-safe in-memory store for testing and micro-services.
type MemoryDatabaseStore struct {
	mu            sync.RWMutex
	notifications map[string][]*DatabaseMessage
}

// NewMemoryDatabaseStore creates an in-memory notification store.
func NewMemoryDatabaseStore() *MemoryDatabaseStore {
	return &MemoryDatabaseStore{
		notifications: make(map[string][]*DatabaseMessage),
	}
}

func (s *MemoryDatabaseStore) Save(ctx context.Context, notifiableID string, msg *DatabaseMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notifications[notifiableID] = append(s.notifications[notifiableID], msg)
	return nil
}

func (s *MemoryDatabaseStore) GetUnread(ctx context.Context, notifiableID string) ([]*DatabaseMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var unread []*DatabaseMessage
	for _, n := range s.notifications[notifiableID] {
		if n.ReadAt == nil {
			unread = append(unread, n)
		}
	}
	return unread, nil
}

func (s *MemoryDatabaseStore) MarkAsRead(ctx context.Context, notifiableID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for _, n := range s.notifications[notifiableID] {
		if n.ID == id {
			n.ReadAt = &now
			return nil
		}
	}
	return fmt.Errorf("notification %s not found", id)
}

// DatabaseChannel delivers in-app notifications to database storage.
type DatabaseChannel struct {
	store DatabaseStore
}

// NewDatabaseChannel creates a database alert channel.
func NewDatabaseChannel(store DatabaseStore) *DatabaseChannel {
	if store == nil {
		store = NewMemoryDatabaseStore()
	}
	return &DatabaseChannel{store: store}
}

// Store returns the underlying notification store.
func (c *DatabaseChannel) Store() DatabaseStore {
	return c.store
}

func (c *DatabaseChannel) Send(ctx context.Context, notifiable Notifiable, notification Notification) error {
	dbNotification, ok := notification.(DatabaseNotification)
	if !ok {
		return fmt.Errorf("notification %T does not implement DatabaseNotification", notification)
	}

	msg, err := dbNotification.ToDatabase(notifiable)
	if err != nil {
		return fmt.Errorf("failed preparing database notification: %w", err)
	}

	if msg.ID == "" {
		msg.ID = generateUUID()
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now()
	}

	notifiableID := notifiable.RouteNotificationFor("database")
	if notifiableID == "" {
		// Skip delivery if notifiable does not provide an ID for database storage
		return nil
	}

	return c.store.Save(ctx, notifiableID, msg)
}

// ============================================================================
// 3. BROADCAST CHANNEL (REALTIME WEBSOCKETS & SSE)
// ============================================================================

// BroadcastChannel pushes notifications in real-time over WebSockets & SSE via pkg/broadcast.
type BroadcastChannel struct {
	hub *broadcast.Hub
}

// NewBroadcastChannel creates a real-time broadcast channel.
func NewBroadcastChannel(hub *broadcast.Hub) *BroadcastChannel {
	if hub == nil {
		hub = broadcast.DefaultHub()
	}
	return &BroadcastChannel{hub: hub}
}

func (c *BroadcastChannel) Send(ctx context.Context, notifiable Notifiable, notification Notification) error {
	bcastNotification, ok := notification.(BroadcastNotification)
	if !ok {
		return fmt.Errorf("notification %T does not implement BroadcastNotification", notification)
	}

	msg, err := bcastNotification.ToBroadcast(notifiable)
	if err != nil {
		return fmt.Errorf("failed preparing broadcast message: %w", err)
	}

	// Default target channel from notifiable route if not explicitly set
	if msg.Channel == "" {
		msg.Channel = notifiable.RouteNotificationFor("broadcast")
		if msg.Channel == "" {
			// Skip broadcast if notifiable provides no channel
			return nil
		}
	}

	c.hub.Broadcast(msg.Channel, msg.Event, msg.Data)
	return nil
}

// ============================================================================
// 4. SLACK CHANNEL (INCOMING WEBHOOKS)
// ============================================================================

// SlackChannel delivers rich notification cards to Slack via Webhooks.
type SlackChannel struct {
	httpClient *http.Client
	defaultURL string
}

// NewSlackChannel creates a Slack delivery channel.
func NewSlackChannel(defaultWebhookURL string) *SlackChannel {
	return &SlackChannel{
		httpClient: &http.Client{Timeout: 5 * time.Second},
		defaultURL: defaultWebhookURL,
	}
}

func (c *SlackChannel) Send(ctx context.Context, notifiable Notifiable, notification Notification) error {
	slackNotification, ok := notification.(SlackNotification)
	if !ok {
		return fmt.Errorf("notification %T does not implement SlackNotification", notification)
	}

	msg, err := slackNotification.ToSlack(notifiable)
	if err != nil {
		return fmt.Errorf("failed preparing slack message: %w", err)
	}

	webhookURL := notifiable.RouteNotificationFor("slack")
	if webhookURL == "" {
		webhookURL = c.defaultURL
	}
	if webhookURL == "" {
		// Skip slack if no webhook URL is configured
		return nil
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed marshaling slack message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("slack webhook request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("slack webhook responded with status %d", resp.StatusCode)
	}

	return nil
}

// ============================================================================
// 5. SMS CHANNEL
// ============================================================================

// SMSDriver defines an outgoing SMS provider (e.g. Twilio, AWS SNS, Log).
type SMSDriver interface {
	SendSMS(ctx context.Context, msg *SMSMessage) error
}

// LogSMSDriver logs SMS messages to structured logger (ideal for development).
type LogSMSDriver struct{}

func (d *LogSMSDriver) SendSMS(ctx context.Context, msg *SMSMessage) error {
	slog.Info("SMS Notification Sent", "to", msg.To, "body", msg.Body)
	return nil
}

// SMSChannel delivers SMS alerts to recipient phone numbers.
type SMSChannel struct {
	driver SMSDriver
}

// NewSMSChannel creates an SMS delivery channel.
func NewSMSChannel(driver SMSDriver) *SMSChannel {
	if driver == nil {
		driver = &LogSMSDriver{}
	}
	return &SMSChannel{driver: driver}
}

func (c *SMSChannel) Send(ctx context.Context, notifiable Notifiable, notification Notification) error {
	smsNotification, ok := notification.(SMSNotification)
	if !ok {
		return fmt.Errorf("notification %T does not implement SMSNotification", notification)
	}

	msg, err := smsNotification.ToSMS(notifiable)
	if err != nil {
		return fmt.Errorf("failed preparing SMS message: %w", err)
	}

	if msg.To == "" {
		msg.To = notifiable.RouteNotificationFor("sms")
		if msg.To == "" {
			// Skip SMS if notifiable provides no phone number
			return nil
		}
	}

	return c.driver.SendSMS(ctx, msg)
}

func generateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
