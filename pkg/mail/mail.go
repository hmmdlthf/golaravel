package mail

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/smtp"
	"strings"
	"sync"
)

// Driver defines the mail sending driver interface.
type Driver interface {
	Send(msg *Message) error
}

// LogDriver logs emails to standard logger (ideal for local testing and CI).
type LogDriver struct{}

func (d *LogDriver) Send(msg *Message) error {
	slog.Info("📧 [MAIL SENT via LogDriver]",
		"to", strings.Join(msg.ToAddresses, ", "),
		"cc", strings.Join(msg.CcAddresses, ", "),
		"from", msg.FromAddress,
		"subject", msg.SubjectLine,
		"attachments", len(msg.Attachments),
	)
	return nil
}

// MemoryDriver stores sent emails in-memory (ideal for tests and inspections).
type MemoryDriver struct {
	mu   sync.RWMutex
	sent []*Message
}

// NewMemoryDriver creates a new in-memory mail transport.
func NewMemoryDriver() *MemoryDriver {
	return &MemoryDriver{
		sent: make([]*Message, 0),
	}
}

func (d *MemoryDriver) Send(msg *Message) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sent = append(d.sent, msg)
	return nil
}

// Sent returns a copy of all sent email messages.
func (d *MemoryDriver) Sent() []*Message {
	d.mu.RLock()
	defer d.mu.RUnlock()
	res := make([]*Message, len(d.sent))
	copy(res, d.sent)
	return res
}

// Count returns the number of sent emails.
func (d *MemoryDriver) Count() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.sent)
}

// Last returns the most recently sent message or nil.
func (d *MemoryDriver) Last() *Message {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if len(d.sent) == 0 {
		return nil
	}
	return d.sent[len(d.sent)-1]
}

// Flush clears all recorded sent messages.
func (d *MemoryDriver) Flush() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sent = d.sent[:0]
}

// SMTPConfig holds SMTP server connection credentials.
type SMTPConfig struct {
	Host               string
	Port               int
	Username           string
	Password           string
	Encryption         string // "tls", "starttls", or ""
	InsecureSkipVerify bool
}

// SMTPDriver sends emails via an external SMTP server with MIME support.
type SMTPDriver struct {
	cfg SMTPConfig
}

// NewSMTPDriver creates an SMTP driver.
func NewSMTPDriver(cfg SMTPConfig) *SMTPDriver {
	return &SMTPDriver{cfg: cfg}
}

func (d *SMTPDriver) Send(msg *Message) error {
	addr := fmt.Sprintf("%s:%d", d.cfg.Host, d.cfg.Port)
	recipients := msg.AllRecipients()
	if len(recipients) == 0 {
		return fmt.Errorf("mail: cannot send message with no recipients")
	}

	mimeBytes, err := msg.BuildMIME()
	if err != nil {
		return fmt.Errorf("mail: failed to construct MIME message: %w", err)
	}

	// Implicit SSL/TLS on port 465
	if strings.ToLower(d.cfg.Encryption) == "tls" || d.cfg.Port == 465 {
		tlsConfig := &tls.Config{
			ServerName:         d.cfg.Host,
			InsecureSkipVerify: d.cfg.InsecureSkipVerify,
		}
		conn, err := tls.Dial("tcp", addr, tlsConfig)
		if err != nil {
			return fmt.Errorf("mail: TLS connection failed: %w", err)
		}
		defer conn.Close()

		c, err := smtp.NewClient(conn, d.cfg.Host)
		if err != nil {
			return fmt.Errorf("mail: SMTP client initialization failed: %w", err)
		}
		defer c.Close()

		if d.cfg.Username != "" {
			auth := smtp.PlainAuth("", d.cfg.Username, d.cfg.Password, d.cfg.Host)
			if err := c.Auth(auth); err != nil {
				return fmt.Errorf("mail: SMTP authentication failed: %w", err)
			}
		}

		if err := c.Mail(msg.FromAddress); err != nil {
			return err
		}
		for _, rcpt := range recipients {
			if err := c.Rcpt(rcpt); err != nil {
				return err
			}
		}

		w, err := c.Data()
		if err != nil {
			return err
		}
		if _, err := w.Write(mimeBytes); err != nil {
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
		return c.Quit()
	}

	// Standard plain / STARTTLS
	var auth smtp.Auth
	if d.cfg.Username != "" {
		auth = smtp.PlainAuth("", d.cfg.Username, d.cfg.Password, d.cfg.Host)
	}

	return smtp.SendMail(addr, auth, msg.FromAddress, recipients, mimeBytes)
}

// Global default driver is LogDriver
var (
	driverMu     sync.RWMutex
	globalDriver Driver = &LogDriver{}
)

// SetDriver configures the global email transport driver.
func SetDriver(d Driver) {
	driverMu.Lock()
	defer driverMu.Unlock()
	globalDriver = d
}

// GetDriver returns the currently configured global driver.
func GetDriver() Driver {
	driverMu.RLock()
	defer driverMu.RUnlock()
	return globalDriver
}

// Send sends an email message using the configured global driver.
func Send(msg *Message) error {
	driverMu.RLock()
	d := globalDriver
	driverMu.RUnlock()
	return d.Send(msg)
}
