package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/a-h/templ"
)

// Attachment represents an email attachment (in-memory or file-backed).
type Attachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Data        []byte `json:"data"`
	Inline      bool   `json:"inline"`
	ContentID   string `json:"content_id,omitempty"`
}

// Message represents a full-featured email message.
type Message struct {
	FromAddress     string            `json:"from_address"`
	FromName        string            `json:"from_name,omitempty"`
	ToAddresses     []string          `json:"to_addresses"`
	CcAddresses     []string          `json:"cc_addresses,omitempty"`
	BccAddresses    []string          `json:"bcc_addresses,omitempty"`
	ReplyToAddress  string            `json:"reply_to_address,omitempty"`
	ReplyToName     string            `json:"reply_to_name,omitempty"`
	SubjectLine     string            `json:"subject_line"`
	HTMLBody        string            `json:"html_body,omitempty"`
	TextBody        string            `json:"text_body,omitempty"`
	Attachments     []*Attachment     `json:"attachments,omitempty"`
	CustomHeaders   map[string]string `json:"custom_headers,omitempty"`
}

// NewMessage creates a new fluent email message builder.
func NewMessage() *Message {
	return &Message{
		FromAddress:   "noreply@golaravel.dev",
		ToAddresses:   make([]string, 0, 2),
		CcAddresses:   make([]string, 0),
		BccAddresses:  make([]string, 0),
		Attachments:   make([]*Attachment, 0),
		CustomHeaders: make(map[string]string),
	}
}

// From sets the sender email and optional display name.
func (m *Message) From(from string, name ...string) *Message {
	m.FromAddress = from
	if len(name) > 0 && name[0] != "" {
		m.FromName = name[0]
	}
	return m
}

// To sets recipient email addresses.
func (m *Message) To(recipients ...string) *Message {
	for _, r := range recipients {
		if strings.TrimSpace(r) != "" {
			m.ToAddresses = append(m.ToAddresses, strings.TrimSpace(r))
		}
	}
	return m
}

// Cc sets carbon-copy recipient email addresses.
func (m *Message) Cc(recipients ...string) *Message {
	for _, r := range recipients {
		if strings.TrimSpace(r) != "" {
			m.CcAddresses = append(m.CcAddresses, strings.TrimSpace(r))
		}
	}
	return m
}

// Bcc sets blind-carbon-copy recipient email addresses.
func (m *Message) Bcc(recipients ...string) *Message {
	for _, r := range recipients {
		if strings.TrimSpace(r) != "" {
			m.BccAddresses = append(m.BccAddresses, strings.TrimSpace(r))
		}
	}
	return m
}

// ReplyTo sets the Reply-To header.
func (m *Message) ReplyTo(address string, name ...string) *Message {
	m.ReplyToAddress = address
	if len(name) > 0 && name[0] != "" {
		m.ReplyToName = name[0]
	}
	return m
}

// Subject sets the email subject line.
func (m *Message) Subject(subject string) *Message {
	m.SubjectLine = subject
	return m
}

// HTML sets the HTML body content.
func (m *Message) HTML(html string) *Message {
	m.HTMLBody = html
	return m
}

// Text sets the plain text body content.
func (m *Message) Text(text string) *Message {
	m.TextBody = text
	return m
}

// View renders a Templ component directly into the message HTML body.
func (m *Message) View(ctx context.Context, cmp templ.Component) *Message {
	if cmp == nil {
		return m
	}
	var buf bytes.Buffer
	if err := cmp.Render(ctx, &buf); err == nil {
		m.HTMLBody = buf.String()
	}
	return m
}

// Attach attaches in-memory data as a named attachment.
func (m *Message) Attach(filename string, data []byte, contentType ...string) *Message {
	ct := "application/octet-stream"
	if len(contentType) > 0 && contentType[0] != "" {
		ct = contentType[0]
	} else if detected := mime.TypeByExtension(filepath.Ext(filename)); detected != "" {
		ct = detected
	}

	m.Attachments = append(m.Attachments, &Attachment{
		Filename:    filename,
		ContentType: ct,
		Data:        data,
		Inline:      false,
	})
	return m
}

// AttachFile reads and attaches a file from local disk.
func (m *Message) AttachFile(path string, filename ...string) *Message {
	data, err := os.ReadFile(path)
	if err != nil {
		return m
	}

	name := filepath.Base(path)
	if len(filename) > 0 && filename[0] != "" {
		name = filename[0]
	}

	return m.Attach(name, data)
}

// Header sets a custom email header.
func (m *Message) Header(key, value string) *Message {
	if m.CustomHeaders == nil {
		m.CustomHeaders = make(map[string]string)
	}
	m.CustomHeaders[key] = value
	return m
}

// AllRecipients returns the consolidated set of envelope recipients (To, Cc, Bcc).
func (m *Message) AllRecipients() []string {
	seen := make(map[string]bool)
	var list []string

	add := func(addrs []string) {
		for _, addr := range addrs {
			addr = strings.TrimSpace(addr)
			if addr != "" && !seen[addr] {
				seen[addr] = true
				list = append(list, addr)
			}
		}
	}

	add(m.ToAddresses)
	add(m.CcAddresses)
	add(m.BccAddresses)
	return list
}

// BuildMIME compiles the Message into RFC 2045 / 2046 compliant MIME bytes.
func (m *Message) BuildMIME() ([]byte, error) {
	var buf bytes.Buffer

	// 1. Headers. Every value is validated (no CR / LF, parsable addresses)
	// and encoded, since names, addresses and subjects often come from
	// user input.
	fromHeader, err := formatAddress(m.FromAddress, m.FromName)
	if err != nil {
		return nil, err
	}
	buf.WriteString(fmt.Sprintf("From: %s\r\n", fromHeader))

	if len(m.ToAddresses) > 0 {
		to, err := formatAddressList(m.ToAddresses)
		if err != nil {
			return nil, err
		}
		buf.WriteString(fmt.Sprintf("To: %s\r\n", to))
	}
	if len(m.CcAddresses) > 0 {
		cc, err := formatAddressList(m.CcAddresses)
		if err != nil {
			return nil, err
		}
		buf.WriteString(fmt.Sprintf("Cc: %s\r\n", cc))
	}
	if _, err := formatAddressList(m.BccAddresses); err != nil {
		return nil, err
	}
	if m.ReplyToAddress != "" {
		replyTo, err := formatAddress(m.ReplyToAddress, m.ReplyToName)
		if err != nil {
			return nil, err
		}
		buf.WriteString(fmt.Sprintf("Reply-To: %s\r\n", replyTo))
	}

	subject, err := encodeHeaderText("Subject", m.SubjectLine)
	if err != nil {
		return nil, err
	}
	buf.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	buf.WriteString(fmt.Sprintf("Date: %s\r\n", time.Now().Format(time.RFC1123Z)))
	buf.WriteString(fmt.Sprintf("Message-ID: <%s@%s>\r\n", generateRandomID(), getHostname(m.FromAddress)))
	buf.WriteString("MIME-Version: 1.0\r\n")

	keys := make([]string, 0, len(m.CustomHeaders))
	for k := range m.CustomHeaders {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !validHeaderName(k) {
			return nil, fmt.Errorf("%w: header name %q", ErrInvalidHeader, k)
		}
		v, err := encodeHeaderText(k, m.CustomHeaders[k])
		if err != nil {
			return nil, err
		}
		buf.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
	}

	hasAttachments := len(m.Attachments) > 0
	hasHTML := m.HTMLBody != ""
	hasText := m.TextBody != ""

	// 2. MIME Structure Dispatch
	if !hasAttachments {
		if hasHTML && hasText {
			altBoundary := generateBoundary("alt")
			buf.WriteString(fmt.Sprintf("Content-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n", altBoundary))

			// Text part
			buf.WriteString(fmt.Sprintf("--%s\r\n", altBoundary))
			buf.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
			buf.WriteString("Content-Transfer-Encoding: 7bit\r\n\r\n")
			buf.WriteString(m.TextBody)
			buf.WriteString("\r\n\r\n")

			// HTML part
			buf.WriteString(fmt.Sprintf("--%s\r\n", altBoundary))
			buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
			buf.WriteString("Content-Transfer-Encoding: 7bit\r\n\r\n")
			buf.WriteString(m.HTMLBody)
			buf.WriteString(fmt.Sprintf("\r\n--%s--\r\n", altBoundary))
			return buf.Bytes(), nil
		}

		if hasHTML {
			buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
			buf.WriteString("Content-Transfer-Encoding: 7bit\r\n\r\n")
			buf.WriteString(m.HTMLBody)
			return buf.Bytes(), nil
		}

		// Text only
		buf.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
		buf.WriteString("Content-Transfer-Encoding: 7bit\r\n\r\n")
		buf.WriteString(m.TextBody)
		return buf.Bytes(), nil
	}

	// With Attachments: multipart/mixed containing alternative/body + attachments
	mixedBoundary := generateBoundary("mixed")
	buf.WriteString(fmt.Sprintf("Content-Type: multipart/mixed; boundary=\"%s\"\r\n\r\n", mixedBoundary))

	// Body part
	buf.WriteString(fmt.Sprintf("--%s\r\n", mixedBoundary))
	if hasHTML && hasText {
		altBoundary := generateBoundary("alt")
		buf.WriteString(fmt.Sprintf("Content-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n", altBoundary))

		buf.WriteString(fmt.Sprintf("--%s\r\n", altBoundary))
		buf.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
		buf.WriteString("Content-Transfer-Encoding: 7bit\r\n\r\n")
		buf.WriteString(m.TextBody)
		buf.WriteString("\r\n\r\n")

		buf.WriteString(fmt.Sprintf("--%s\r\n", altBoundary))
		buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
		buf.WriteString("Content-Transfer-Encoding: 7bit\r\n\r\n")
		buf.WriteString(m.HTMLBody)
		buf.WriteString(fmt.Sprintf("\r\n--%s--\r\n\r\n", altBoundary))
	} else if hasHTML {
		buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
		buf.WriteString("Content-Transfer-Encoding: 7bit\r\n\r\n")
		buf.WriteString(m.HTMLBody)
		buf.WriteString("\r\n\r\n")
	} else {
		buf.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
		buf.WriteString("Content-Transfer-Encoding: 7bit\r\n\r\n")
		buf.WriteString(m.TextBody)
		buf.WriteString("\r\n\r\n")
	}

	// Attachments
	for _, att := range m.Attachments {
		buf.WriteString(fmt.Sprintf("--%s\r\n", mixedBoundary))
		// Filenames often come from uploads: let mime quote / encode them
		// (RFC 2231) instead of pasting them into the header.
		if hasLineBreak(att.Filename) || hasLineBreak(att.ContentType) {
			return nil, fmt.Errorf("%w: attachment %q", ErrInvalidHeader, att.Filename)
		}
		mediaType, _, err := mime.ParseMediaType(att.ContentType)
		if err != nil {
			mediaType = "application/octet-stream"
		}
		ctHeader := mime.FormatMediaType(mediaType, map[string]string{"name": att.Filename})
		cdHeader := mime.FormatMediaType("attachment", map[string]string{"filename": att.Filename})
		if ctHeader == "" || cdHeader == "" {
			return nil, fmt.Errorf("%w: attachment %q", ErrInvalidHeader, att.Filename)
		}
		buf.WriteString(fmt.Sprintf("Content-Type: %s\r\n", ctHeader))
		buf.WriteString(fmt.Sprintf("Content-Disposition: %s\r\n", cdHeader))
		buf.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")

		encoded := base64.StdEncoding.EncodeToString(att.Data)
		// Wrap base64 lines at 76 characters per RFC 2045
		for i := 0; i < len(encoded); i += 76 {
			end := i + 76
			if end > len(encoded) {
				end = len(encoded)
			}
			buf.WriteString(encoded[i:end])
			buf.WriteString("\r\n")
		}
		buf.WriteString("\r\n")
	}

	buf.WriteString(fmt.Sprintf("--%s--\r\n", mixedBoundary))
	return buf.Bytes(), nil
}

func generateRandomID() string {
	b := make([]byte, 12)
	_, _ = io.ReadFull(rand.Reader, b)
	return fmt.Sprintf("%x", b)
}

func generateBoundary(kind string) string {
	return fmt.Sprintf("=_golaravel_%s_%s", kind, generateRandomID())
}

func getHostname(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) == 2 && parts[1] != "" {
		return parts[1]
	}
	return "golaravel.local"
}
