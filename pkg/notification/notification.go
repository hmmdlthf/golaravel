package notification

import (
	"time"

	"github.com/golaravel/golaravel/pkg/broadcast"
	"github.com/golaravel/golaravel/pkg/mail"
)

// Notification defines the base interface all notifications must implement.
type Notification interface {
	// Via returns the channels the notification should be delivered on.
	// Common channels: "mail", "database", "broadcast", "slack", "sms"
	Via(notifiable Notifiable) []string
}

// ShouldQueue marks a notification to be processed asynchronously by the background worker pool.
type ShouldQueue interface {
	ShouldQueue() bool
}

// MailableNotification is implemented by notifications supporting the "mail" channel.
type MailableNotification interface {
	ToMail(notifiable Notifiable) (*mail.Message, error)
}

// DatabaseNotification is implemented by notifications supporting the "database" channel.
type DatabaseNotification interface {
	ToDatabase(notifiable Notifiable) (*DatabaseMessage, error)
}

// BroadcastNotification is implemented by notifications supporting realtime WebSockets & SSE.
type BroadcastNotification interface {
	ToBroadcast(notifiable Notifiable) (*broadcast.Message, error)
}

// SlackNotification is implemented by notifications delivering to Slack channels or incoming webhooks.
type SlackNotification interface {
	ToSlack(notifiable Notifiable) (*SlackMessage, error)
}

// SMSNotification is implemented by notifications delivering SMS text messages.
type SMSNotification interface {
	ToSMS(notifiable Notifiable) (*SMSMessage, error)
}

// DatabaseMessage represents an in-app notification stored in the database.
type DatabaseMessage struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Data      map[string]any `json:"data"`
	ReadAt    *time.Time     `json:"read_at,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// SlackMessage represents a rich webhook card sent to Slack.
type SlackMessage struct {
	Channel     string            `json:"channel,omitempty"`
	Text        string            `json:"text"`
	Username    string            `json:"username,omitempty"`
	IconEmoji   string            `json:"icon_emoji,omitempty"`
	Attachments []SlackAttachment `json:"attachments,omitempty"`
}

// SlackAttachment represents a structured block or attachment inside a Slack message.
type SlackAttachment struct {
	Title string `json:"title,omitempty"`
	Text  string `json:"text,omitempty"`
	Color string `json:"color,omitempty"`
}

// SMSMessage represents an outgoing SMS text alert.
type SMSMessage struct {
	To   string `json:"to"`
	From string `json:"from,omitempty"`
	Body string `json:"body"`
}
