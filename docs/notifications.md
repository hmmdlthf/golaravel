# Multi-Channel Notifications Engine 🔔 (PRD §7)

GoLaravel provides a unified, multi-channel notification engine inspired by Laravel's notification architecture. A single notification class can deliver across **Email**, **In-App Database Alerts**, **Realtime WebSockets/SSE Broadcasting**, **Slack Webhooks**, and **SMS (Twilio/Log)** either synchronously or asynchronously via background queue workers.

---

## 1. Generating Notifications with Artisan

You can generate a new notification class using the Artisan CLI:

```bash
artisan make:notification InvoicePaid
# or using Go:
go run cmd/artisan/main.go make:notification InvoicePaid
```

This creates `app/notifications/invoice_paid_notification.go` with boilerplates for `Via`, `ToMail`, `ToDatabase`, `ToBroadcast`, `ToSlack`, and `ToSMS`.

---

## 2. Notification Definition & Channel Interfaces

A notification in GoLaravel is any struct implementing the `notification.Notification` interface (`Via(recipient Notifiable) []string`). It optionally implements channel-specific interfaces:

| Channel Interface | Method Required | Purpose |
|---|---|---|
| `MailableNotification` | `ToMail(recipient Notifiable) *mail.Message` | Send formatted transactional email |
| `DatabaseNotification` | `ToDatabase(recipient Notifiable) *DatabaseMessage` | Store persistent in-app notifications |
| `BroadcastNotification` | `ToBroadcast(recipient Notifiable) *broadcast.Message` | Broadcast to WebSockets & SSE subscribers |
| `SlackNotification` | `ToSlack(recipient Notifiable) *SlackMessage` | Send rich webhook messages to Slack |
| `SMSNotification` | `ToSMS(recipient Notifiable) *SMSMessage` | Send SMS messages via Twilio or Log |
| `ShouldQueue` | `ShouldQueue() bool` | Defer delivery to asynchronous queue workers |

### Example Notification:
```go
package notifications

import (
	"fmt"
	"github.com/golaravel/golaravel/pkg/broadcast"
	"github.com/golaravel/golaravel/pkg/mail"
	"github.com/golaravel/golaravel/pkg/notification"
)

type InvoicePaidNotification struct {
	InvoiceNumber string
	CustomerName  string
	Amount        float64
}

func NewInvoicePaidNotification(invNum, customer string, amount float64) *InvoicePaidNotification {
	return &InvoicePaidNotification{InvoiceNumber: invNum, CustomerName: customer, Amount: amount}
}

// Via determines which channels the notification should be dispatched through.
func (n *InvoicePaidNotification) Via(notifiable notification.Notifiable) []string {
	return []string{
		notification.ChannelMail,
		notification.ChannelDatabase,
		notification.ChannelBroadcast,
	}
}

func (n *InvoicePaidNotification) ToMail(notifiable notification.Notifiable) *mail.Message {
	return mail.NewMessage().
		Subject(fmt.Sprintf("Payment Receipt: %s", n.InvoiceNumber)).
		Body(fmt.Sprintf("Dear %s,\n\nWe received your payment of $%.2f. Thank you!", n.CustomerName, n.Amount))
}

func (n *InvoicePaidNotification) ToDatabase(notifiable notification.Notifiable) *notification.DatabaseMessage {
	return &notification.DatabaseMessage{
		Type: "InvoicePaid",
		Data: map[string]any{
			"invoice_number": n.InvoiceNumber,
			"amount":         n.Amount,
		},
	}
}

func (n *InvoicePaidNotification) ToBroadcast(notifiable notification.Notifiable) *broadcast.Message {
	return &broadcast.Message{
		Channel: "invoices",
		Event:   "invoice.paid",
		Data: map[string]any{
			"invoice_number": n.InvoiceNumber,
			"amount":         n.Amount,
		},
	}
}
```

---

## 3. Recipients (`Notifiable`)

Any struct that implements `RouteNotificationFor(channel string) string` can receive notifications.

### Ent Model Implementation:
```go
func (u *User) RouteNotificationFor(channel string) string {
    switch channel {
    case "mail":
        return u.Email
    case "sms":
        return u.Phone
    case "database":
        return fmt.Sprintf("user_%d", u.ID)
    case "slack":
        return u.SlackWebhookURL
    default:
        return ""
    }
}
```

### On-Demand Anonymous Notifiables:
When sending notifications to ad-hoc recipients that aren't stored users (e.g. guest checkouts or dynamic webhooks):

```go
recipient := notification.OnDemand("mail", "billing@acme.com").
    Route("sms", "+15551234567").
    Route("slack", "https://hooks.slack.com/services/xxx/yyy/zzz")

_ = notification.SendOne(ctx, recipient, notif)
```

> [!NOTE]
> If a notification's `Via()` lists multiple channels (e.g. `mail` and `sms`), but a recipient does not have a phone number configured (`RouteNotificationFor("sms") == ""`), GoLaravel gracefully skips the unrouted channels and dispatches to the available ones.

---

## 4. Sending Notifications

### Synchronous Dispatch (`SendNow`)
Deliver immediately on the current goroutine:
```go
err := notification.SendNow(ctx, notif, recipient1, recipient2)
```

### Asynchronous Queue Dispatch (`Send`)
If the notification implements `ShouldQueue` returning `true`, or when calling `SendAsync`, delivery is automatically queued to prevent network latency (SMTP, Slack, Twilio) from stalling the HTTP request:

```go
// Dispatches to pkg/queue workers if ShouldQueue() is true
err := notification.Send(ctx, notif, user)
```

---

## 5. In-App Notifications (`DatabaseStore`)

In-app alerts are stored using the thread-safe `DatabaseStore`:

```go
store := notification.GetDatabaseStore()

// Retrieve all unread notifications for a user
unread, err := store.GetUnread(ctx, fmt.Sprintf("user_%d", user.ID))

// Mark a specific notification as read
err = store.MarkAsRead(ctx, notificationID)

// Mark all as read
err = store.MarkAllAsRead(ctx, fmt.Sprintf("user_%d", user.ID))
```

---

## 6. Supported Delivery Channels

1. **Email Channel (`mail`)**: Dispatches through `pkg/mail` using SMTP or local log driver.
2. **Database Channel (`database`)**: Stores in-app alerts in persistent storage with read/unread tracking.
3. **Broadcast Channel (`broadcast`)**: Dispatches live updates to WebSockets (`/api/ws`) and Server-Sent Events (`/api/broadcast/sse`).
4. **Slack Channel (`slack`)**: Posts rich JSON webhooks with customizable sender usernames and markdown formatting.
5. **SMS Channel (`sms`)**: Dispatches text messages via Twilio API driver or local log driver.
