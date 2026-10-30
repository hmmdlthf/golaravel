package mail

import (
	"context"
	"fmt"
	"time"

	"github.com/golaravel/golaravel/pkg/queue"
)

// Mailable represents an email definition that can build a Message.
type Mailable interface {
	Build(ctx context.Context) (*Message, error)
}

// MailableFunc is a function adapter implementing the Mailable interface.
type MailableFunc func(ctx context.Context) (*Message, error)

// Build executes the underlying function.
func (f MailableFunc) Build(ctx context.Context) (*Message, error) {
	return f(ctx)
}

// PendingMail provides a fluent builder for configuring and sending mailables.
type PendingMail struct {
	to          []string
	cc          []string
	bcc         []string
	replyTo     string
	replyToName string
	from        string
	fromName    string
}

// NewPendingMail creates a new PendingMail builder.
func NewPendingMail() *PendingMail {
	return &PendingMail{
		to:  make([]string, 0, 2),
		cc:  make([]string, 0),
		bcc: make([]string, 0),
	}
}

// To sets recipient addresses.
func (p *PendingMail) To(recipients ...string) *PendingMail {
	p.to = append(p.to, recipients...)
	return p
}

// Cc sets carbon-copy recipient addresses.
func (p *PendingMail) Cc(recipients ...string) *PendingMail {
	p.cc = append(p.cc, recipients...)
	return p
}

// Bcc sets blind-carbon-copy recipient addresses.
func (p *PendingMail) Bcc(recipients ...string) *PendingMail {
	p.bcc = append(p.bcc, recipients...)
	return p
}

// From sets the sender address and optional name.
func (p *PendingMail) From(addr string, name ...string) *PendingMail {
	p.from = addr
	if len(name) > 0 {
		p.fromName = name[0]
	}
	return p
}

// ReplyTo sets the Reply-To address and optional name.
func (p *PendingMail) ReplyTo(addr string, name ...string) *PendingMail {
	p.replyTo = addr
	if len(name) > 0 {
		p.replyToName = name[0]
	}
	return p
}

// Prepare builds the Message from the Mailable and merges any PendingMail recipients.
func (p *PendingMail) Prepare(ctx context.Context, mailable Mailable) (*Message, error) {
	if mailable == nil {
		return nil, fmt.Errorf("mail: mailable cannot be nil")
	}

	msg, err := mailable.Build(ctx)
	if err != nil {
		return nil, fmt.Errorf("mail: failed to build mailable: %w", err)
	}
	if msg == nil {
		return nil, fmt.Errorf("mail: mailable returned nil message")
	}

	// Apply pending recipients and overrides
	if len(p.to) > 0 {
		msg.To(p.to...)
	}
	if len(p.cc) > 0 {
		msg.Cc(p.cc...)
	}
	if len(p.bcc) > 0 {
		msg.Bcc(p.bcc...)
	}
	if p.from != "" {
		msg.From(p.from, p.fromName)
	}
	if p.replyTo != "" {
		msg.ReplyTo(p.replyTo, p.replyToName)
	}

	return msg, nil
}

// Send builds and sends the mailable synchronously.
func (p *PendingMail) Send(ctx context.Context, mailable Mailable) error {
	msg, err := p.Prepare(ctx, mailable)
	if err != nil {
		return err
	}
	return Send(msg)
}

// Queue builds and dispatches the mailable asynchronously via the background queue.
func (p *PendingMail) Queue(ctx context.Context, mailable Mailable) error {
	msg, err := p.Prepare(ctx, mailable)
	if err != nil {
		return err
	}
	return queue.Push(&SendQueuedMailJob{Message: msg})
}

// Later builds and schedules the mailable to be sent after a delay.
func (p *PendingMail) Later(ctx context.Context, delay time.Duration, mailable Mailable) error {
	msg, err := p.Prepare(ctx, mailable)
	if err != nil {
		return err
	}
	return queue.Later(delay, &SendQueuedMailJob{Message: msg})
}

// Package-level fluent shortcuts

// To creates a fluent PendingMail transaction with recipients.
func To(recipients ...string) *PendingMail {
	return NewPendingMail().To(recipients...)
}

// Cc creates a fluent PendingMail transaction with CC recipients.
func Cc(recipients ...string) *PendingMail {
	return NewPendingMail().Cc(recipients...)
}

// Bcc creates a fluent PendingMail transaction with BCC recipients.
func Bcc(recipients ...string) *PendingMail {
	return NewPendingMail().Bcc(recipients...)
}

// SendMailable sends a mailable synchronously using its built recipients.
func SendMailable(ctx context.Context, mailable Mailable) error {
	return NewPendingMail().Send(ctx, mailable)
}

// Queue dispatches a mailable asynchronously to the queue.
func Queue(ctx context.Context, mailable Mailable) error {
	return NewPendingMail().Queue(ctx, mailable)
}

// Later schedules a mailable for delayed asynchronous sending.
func Later(ctx context.Context, delay time.Duration, mailable Mailable) error {
	return NewPendingMail().Later(ctx, delay, mailable)
}
