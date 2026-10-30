# Advanced Mailables, Templ View Templates & Preview Studio 📧

GoLaravel delivers a complete, production-grade email system inspired by Laravel Mailables, paired with Go's compiled speed, type-safe Templ view rendering, background queue integration (`pkg/queue`), and an interactive in-browser **Mail Preview Studio**.

---

## 1. Creating Mailables

Generate a new Mailable class using the Artisan CLI:

```bash
# Generate a standard HTML + text Mailable
artisan make:mail Welcome

# Generate a Mailable with an accompanying Templ component view
artisan make:mail Invoice --view
```

### Generated Mailable Struct (`app/mail/welcome_mail.go`)
```go
package mail

import (
    "context"
    "github.com/golaravel/golaravel/pkg/mail"
)

type WelcomeMail struct {
    Recipient string
    Name      string
}

func NewWelcomeMail(recipient, name string) *WelcomeMail {
    return &WelcomeMail{
        Recipient: recipient,
        Name:      name,
    }
}

// Build compiles the message with headers and body.
func (m *WelcomeMail) Build(ctx context.Context) (*mail.Message, error) {
    return mail.NewMessage().
        To(m.Recipient).
        Subject("Welcome to GoLaravel, " + m.Name).
        HTML("<h1>Welcome " + m.Name + "!</h1><p>Thank you for joining us.</p>").
        Text("Welcome " + m.Name + "!\nThank you for joining us."), nil
}
```

---

## 2. Type-Safe Templ View Templates

Instead of raw string concatenation or fragile runtime HTML templates, GoLaravel Mailables seamlessly compile type-safe **Templ components** into email bodies via `msg.View(ctx, component)`:

### Companion Templ View (`app/views/mail/invoice.templ`)
```templ
package mailviews

templ InvoiceEmail(name string, invoiceID string, amount string) {
    <div style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 600px; margin: 0 auto; padding: 24px; border: 1px solid #e2e8f0; border-radius: 8px;">
        <h1 style="color: #0f172a; font-size: 20px; font-weight: 700; margin-bottom: 16px;">Invoice Ready</h1>
        <p style="color: #475569; font-size: 15px; line-height: 1.6;">
            Hello <strong>{ name }</strong>, your invoice <code>#{ invoiceID }</code> for <strong>{ amount }</strong> has been generated.
        </p>
        <div style="margin-top: 24px; padding-top: 16px; border-top: 1px solid #e2e8f0; font-size: 13px; color: #94a3b8;">
            &copy; GoLaravel Framework. All rights reserved.
        </div>
    </div>
}
```

### Using Templ in Mailable
```go
func (m *InvoiceMail) Build(ctx context.Context) (*mail.Message, error) {
    return mail.NewMessage().
        To(m.Recipient).
        Subject("Your Invoice #" + m.InvoiceID).
        View(ctx, mailviews.InvoiceEmail(m.Name, m.InvoiceID, "$49.00")).
        Text("Hello " + m.Name + ", your invoice #" + m.InvoiceID + " is ready."), nil
}
```

---

## 3. Fluent Sending & Asynchronous Queues

Send mailables synchronously or offload them to background queue workers (`pkg/queue`) without blocking HTTP requests:

```go
import "github.com/golaravel/golaravel/pkg/mail"

// 1. Synchronous delivery
err := mail.To("customer@example.com").
    Cc("accountant@example.com").
    Bcc("audit@example.com").
    Send(ctx, mail.NewWelcomeMail("customer@example.com", "Sarah"))

// 2. Asynchronous queued delivery (dispatches SendQueuedMailJob to worker pool)
err := mail.To("customer@example.com").
    Queue(ctx, mail.NewWelcomeMail("customer@example.com", "Sarah"))

// 3. Delayed delivery (scheduled via queue after delay)
err := mail.To("customer@example.com").
    Later(ctx, 15*time.Minute, mail.NewWelcomeMail("customer@example.com", "Sarah"))
```

---

## 4. Attachments & MIME Multipart Engine

GoLaravel includes an RFC 2045 / RFC 2046 compliant MIME compiler that automatically organizes:
- `text/plain` and `text/html` into a `multipart/alternative` container.
- File and in-memory attachments into an enclosing `multipart/mixed` container.
- Base64 encoding with 76-character line wrapping per email protocol standards.

```go
msg := mail.NewMessage().
    From("billing@golaravel.dev", "GoLaravel Billing").
    To("client@example.com").
    Subject("Monthly Statement").
    HTML("<h1>Monthly Statement Attached</h1>").
    Text("Monthly Statement Attached").
    AttachFile("/var/reports/statement-october.pdf", "Statement-Oct.pdf").
    Attach("metadata.json", []byte(`{"account": 1024, "status": "paid"}`), "application/json").
    Header("X-Entity-Ref-ID", "invoice_1024")

err := mail.Send(msg)
```

---

## 5. Mail Preview Studio

Visual email testing in the browser without sending actual emails or using sandbox inboxes.

### Registering Previews
Register your mailables in development routes or during application bootstrap:

```go
// Register struct mailable
mail.RegisterPreview("Welcome Email", mail.NewWelcomeMail("sarah@example.com", "Sarah Connor"))

// Register with closure
mail.RegisterPreview("Invoice Receipt", func(ctx context.Context) (*mail.Message, error) {
    return mail.NewInvoiceMail("client@example.com", "Alice", "INV-2026-99").Build(ctx)
})

// Register raw message
mail.RegisterPreview("Security Alert", mail.NewMessage().
    Subject("New Login Detected").
    HTML("<h3>New Login from Linux (Firefox)</h3>"))
```

### Mounting the Studio Route
Mount the interactive preview dashboard on your GoLaravel router or Engine:

```go
app := golaravel.New()

// Mounts at /mail/preview (or custom prefix)
mail.MountPreviewStudio(app, "/mail/preview")
```

### Studio Features
Visit `http://localhost:8080/mail/preview`:
1. **Interactive Sidebar**: Instant template switching and live search filter.
2. **Device Viewport Simulator**:
   - **Desktop**: Full 100% width canvas.
   - **Tablet**: 768px bounded frame.
   - **Mobile**: 375px phone frame.
3. **Isolated Iframe Rendering**: Email CSS styles are completely sandboxed and do not leak into the dashboard.
4. **Header Inspector**: Live display of From, To, Cc, Subject, Date, and attachments badge.
5. **Raw Inspection Tabs**:
   - `GET /mail/preview/render?mail=<Name>`: Isolated live HTML.
   - `GET /mail/preview/raw?mail=<Name>&format=text`: Plain text fallback.
   - `GET /mail/preview/raw?mail=<Name>&format=mime`: Raw RFC 2822 / 2045 MIME compilation.
   - `GET /mail/preview/api/list`: JSON metadata list of all registered templates.

---

## 6. Testing & Mail Fake

Prevent real emails from being dispatched during automated testing and assert mail activity:

```go
func TestRegistration_SendsWelcomeEmail(t *testing.T) {
    // 1. Trap all outgoing emails in an in-memory ArrayDriver
    mail.Fake()
    defer mail.Restore()

    // 2. Perform application action
    client := app.Test(t)
    client.PostJSON("/register", map[string]string{
        "name":  "Alice",
        "email": "alice@example.com",
    }).AssertStatus(201)

    // 3. Assertions
    mail.AssertSentCount(t, 1)

    mail.AssertSent(t, func(m *mail.Message) bool {
        return m.SubjectLine == "Welcome to GoLaravel, Alice" &&
            len(m.ToAddresses) > 0 &&
            m.ToAddresses[0] == "alice@example.com"
    })

    mail.AssertNotSent(t, func(m *mail.Message) bool {
        return m.SubjectLine == "Password Reset"
    })
}
```

---

## 7. Transport Drivers Configuration

Configure the active mail driver globally:

```go
// 1. LogDriver (default): logs emails to slog / terminal
mail.SetDriver(&mail.LogDriver{})

// 2. MemoryDriver: stores sent messages in an in-memory slice (used by mail.Fake())
mail.SetDriver(mail.NewMemoryDriver())

// 3. SMTPDriver: sends emails via standard or TLS/STARTTLS SMTP
mail.SetDriver(mail.NewSMTPDriver(mail.SMTPConfig{
    Host:       "smtp.mailtrap.io",
    Port:       587,
    Username:   "my_username",
    Password:   "my_password",
    Encryption: "starttls", // or "tls" for implicit port 465
}))
```
