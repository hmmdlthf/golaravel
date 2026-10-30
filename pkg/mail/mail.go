package mail

import (
	"fmt"
	"log/slog"
	"net/smtp"
	"strings"
)

// Message represents an email message.
type Message struct {
	FromAddress string
	ToAddresses []string
	SubjectLine string
	HTMLBody    string
	TextBody    string
}

// NewMessage creates a new fluent email message builder.
func NewMessage() *Message {
	return &Message{
		FromAddress: "noreply@golaravel.dev",
		ToAddresses: make([]string, 0, 2),
	}
}

// From sets the sender email.
func (m *Message) From(from string) *Message {
	m.FromAddress = from
	return m
}

// To sets recipient email addresses.
func (m *Message) To(recipients ...string) *Message {
	m.ToAddresses = append(m.ToAddresses, recipients...)
	return m
}

// Subject sets the email subject line.
func (m *Message) Subject(subject string) *Message {
	m.SubjectLine = subject
	return m
}

// HTML sets HTML body content.
func (m *Message) HTML(html string) *Message {
	m.HTMLBody = html
	return m
}

// Text sets plain text body content.
func (m *Message) Text(text string) *Message {
	m.TextBody = text
	return m
}

// Driver defines the mail sending driver interface.
type Driver interface {
	Send(msg *Message) error
}

// LogDriver logs emails to standard logger (great for local testing and CI).
type LogDriver struct{}

func (d *LogDriver) Send(msg *Message) error {
	slog.Info("📧 [MAIL SENT via LogDriver]",
		"to", strings.Join(msg.ToAddresses, ", "),
		"from", msg.FromAddress,
		"subject", msg.SubjectLine,
	)
	return nil
}

// SMTPConfig holds SMTP server credentials.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
}

// SMTPDriver sends emails via an external SMTP server.
type SMTPDriver struct {
	cfg SMTPConfig
}

// NewSMTPDriver creates an SMTP driver.
func NewSMTPDriver(cfg SMTPConfig) *SMTPDriver {
	return &SMTPDriver{cfg: cfg}
}

func (d *SMTPDriver) Send(msg *Message) error {
	addr := fmt.Sprintf("%s:%d", d.cfg.Host, d.cfg.Port)
	auth := smtp.PlainAuth("", d.cfg.Username, d.cfg.Password, d.cfg.Host)

	headers := make(map[string]string)
	headers["From"] = msg.FromAddress
	headers["To"] = strings.Join(msg.ToAddresses, ", ")
	headers["Subject"] = msg.SubjectLine
	headers["MIME-Version"] = "1.0"

	var body string
	if msg.HTMLBody != "" {
		headers["Content-Type"] = "text/html; charset=UTF-8"
		body = msg.HTMLBody
	} else {
		headers["Content-Type"] = "text/plain; charset=UTF-8"
		body = msg.TextBody
	}

	var messageBuilder strings.Builder
	for k, v := range headers {
		messageBuilder.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
	}
	messageBuilder.WriteString("\r\n" + body)

	return smtp.SendMail(addr, auth, msg.FromAddress, msg.ToAddresses, []byte(messageBuilder.String()))
}

// Default global driver is LogDriver
var globalDriver Driver = &LogDriver{}

// SetDriver configures the global email transport driver.
func SetDriver(d Driver) {
	globalDriver = d
}

// Send sends an email message using the configured global driver.
func Send(msg *Message) error {
	return globalDriver.Send(msg)
}
