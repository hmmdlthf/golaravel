package mail

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"sync"
	"time"
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
	Encryption         string // "tls" (implicit), "starttls" (required), or "" (STARTTLS when offered)
	InsecureSkipVerify bool
	// Timeout bounds connecting and the SMTP conversation (default 30s,
	// plus a second per 64 KiB of message).
	Timeout time.Duration
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
	addr := net.JoinHostPort(d.cfg.Host, strconv.Itoa(d.cfg.Port))
	recipients := msg.AllRecipients()
	if len(recipients) == 0 {
		return fmt.Errorf("mail: cannot send message with no recipients")
	}

	mimeBytes, err := msg.BuildMIME()
	if err != nil {
		return fmt.Errorf("mail: failed to construct MIME message: %w", err)
	}

	timeout := d.cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	tlsConfig := &tls.Config{ServerName: d.cfg.Host, InsecureSkipVerify: d.cfg.InsecureSkipVerify}
	mode := strings.ToLower(d.cfg.Encryption)
	implicitTLS := mode == "tls" || d.cfg.Port == 465

	dialer := &net.Dialer{Timeout: timeout}
	var conn net.Conn
	if implicitTLS {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsConfig)
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("mail: connection to %s failed: %w", addr, err)
	}
	defer conn.Close()
	// One deadline for the whole conversation, so a stuck server cannot
	// block the sender forever.
	_ = conn.SetDeadline(time.Now().Add(timeout + time.Duration(len(mimeBytes)/(64<<10))*time.Second))

	c, err := smtp.NewClient(conn, d.cfg.Host)
	if err != nil {
		return fmt.Errorf("mail: SMTP client initialization failed: %w", err)
	}
	defer c.Close()

	if !implicitTLS {
		ok, _ := c.Extension("STARTTLS")
		switch {
		case ok:
			if err := c.StartTLS(tlsConfig); err != nil {
				return fmt.Errorf("mail: STARTTLS failed: %w", err)
			}
		case mode == "starttls":
			// Required: never fall back to plain text when TLS was asked
			// for (an attacker can strip the STARTTLS offer).
			return fmt.Errorf("mail: server %s does not offer STARTTLS", addr)
		}
	}

	if d.cfg.Username != "" {
		// smtp.PlainAuth refuses to send credentials over an unencrypted
		// connection to anything but localhost.
		if err := c.Auth(smtp.PlainAuth("", d.cfg.Username, d.cfg.Password, d.cfg.Host)); err != nil {
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
