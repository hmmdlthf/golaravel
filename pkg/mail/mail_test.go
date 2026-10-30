package mail_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
	"github.com/golaravel/golaravel/pkg/golaravel"
	"github.com/golaravel/golaravel/pkg/mail"
	"github.com/golaravel/golaravel/pkg/queue"
)

type WelcomeMailable struct {
	Name string
}

func (m *WelcomeMailable) Build(ctx context.Context) (*mail.Message, error) {
	return mail.NewMessage().
		Subject("Welcome to GoLaravel, " + m.Name).
		HTML("<h1>Welcome " + m.Name + "</h1>").
		Text("Welcome " + m.Name + " to GoLaravel"), nil
}

func TestMessage_FluentBuilderAndMIME(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "invoice.pdf")
	_ = os.WriteFile(testFile, []byte("%PDF-1.4 mock pdf content"), 0644)

	msg := mail.NewMessage().
		From("billing@golaravel.dev", "GoLaravel Billing").
		To("customer@example.com").
		Cc("accountant@example.com").
		Bcc("audit@example.com").
		ReplyTo("support@golaravel.dev", "Support Team").
		Subject("Invoice #1024").
		HTML("<h1>Your Invoice</h1><p>Thank you for your business!</p>").
		Text("Your Invoice - Thank you for your business!").
		AttachFile(testFile, "invoice-1024.pdf").
		Attach("receipt.txt", []byte("Paid in full: $49.00"), "text/plain").
		Header("X-Tenant-ID", "tenant_42")

	// Verify all recipients
	recipients := msg.AllRecipients()
	if len(recipients) != 3 {
		t.Fatalf("expected 3 recipients, got %d (%v)", len(recipients), recipients)
	}

	// Verify MIME compilation
	mimeBytes, err := msg.BuildMIME()
	if err != nil {
		t.Fatalf("BuildMIME failed: %v", err)
	}

	mimeStr := string(mimeBytes)
	if !strings.Contains(mimeStr, "From: GoLaravel Billing <billing@golaravel.dev>") {
		t.Errorf("MIME missing From header")
	}
	if !strings.Contains(mimeStr, "To: customer@example.com") {
		t.Errorf("MIME missing To header")
	}
	if !strings.Contains(mimeStr, "Cc: accountant@example.com") {
		t.Errorf("MIME missing Cc header")
	}
	if strings.Contains(mimeStr, "Bcc:") {
		t.Errorf("MIME must NOT contain Bcc header per RFC standard")
	}
	if !strings.Contains(mimeStr, "Subject: Invoice #1024") {
		t.Errorf("MIME missing Subject header")
	}
	if !strings.Contains(mimeStr, "X-Tenant-ID: tenant_42") {
		t.Errorf("MIME missing custom header")
	}
	if !strings.Contains(mimeStr, "multipart/mixed") {
		t.Errorf("MIME should be multipart/mixed with attachments")
	}
	if !strings.Contains(mimeStr, "multipart/alternative") {
		t.Errorf("MIME should contain multipart/alternative body")
	}
	if !strings.Contains(mimeStr, "filename=\"invoice-1024.pdf\"") {
		t.Errorf("MIME missing file attachment")
	}
	if !strings.Contains(mimeStr, "filename=\"receipt.txt\"") {
		t.Errorf("MIME missing in-memory attachment")
	}
}

func TestMessage_TemplViewRendering(t *testing.T) {
	ctx := context.Background()

	// Mock Templ component
	mockComponent := templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_, err := io.WriteString(w, `<div class="email-card"><h2>Hello from Templ!</h2></div>`)
		return err
	})

	msg := mail.NewMessage().
		To("user@example.com").
		Subject("Templ Email").
		View(ctx, mockComponent)

	if !strings.Contains(msg.HTMLBody, `<div class="email-card"><h2>Hello from Templ!</h2></div>`) {
		t.Fatalf("expected HTML body to contain rendered Templ component, got: %s", msg.HTMLBody)
	}
}

func TestMail_FakeAndAssertions(t *testing.T) {
	mem := mail.Fake()
	defer mail.Restore()

	mail.AssertNotSent(t)

	err := mail.To("user1@example.com", "user2@example.com").
		Cc("manager@example.com").
		Send(context.Background(), &WelcomeMailable{Name: "Alice"})

	if err != nil {
		t.Fatalf("unexpected Send error: %v", err)
	}

	mail.AssertSentCount(t, 1)

	mail.AssertSent(t, func(m *mail.Message) bool {
		return m.SubjectLine == "Welcome to GoLaravel, Alice" &&
			len(m.ToAddresses) == 2 &&
			m.ToAddresses[0] == "user1@example.com"
	})

	mail.AssertNotSent(t, func(m *mail.Message) bool {
		return m.SubjectLine == "Non Existent Subject"
	})

	if mem.Count() != 1 {
		t.Errorf("expected 1 sent message in MemoryDriver")
	}
	if mem.Last() == nil || mem.Last().SubjectLine != "Welcome to GoLaravel, Alice" {
		t.Errorf("unexpected last message: %v", mem.Last())
	}
}

func TestMail_QueuedMailable(t *testing.T) {
	mail.Fake()
	defer mail.Restore()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	queue.StartWorkers(ctx, 2, "default")
	defer queue.Stop()

	// Dispatch queued mail
	err := mail.To("queued@example.com").Queue(ctx, &WelcomeMailable{Name: "QueuedUser"})
	if err != nil {
		t.Fatalf("unexpected Queue error: %v", err)
	}

	// Wait for queue worker to process the job
	time.Sleep(100 * time.Millisecond)

	mail.AssertSent(t, func(m *mail.Message) bool {
		return len(m.ToAddresses) > 0 && m.ToAddresses[0] == "queued@example.com"
	})
}

func TestMail_PreviewStudio(t *testing.T) {
	mail.ClearPreviews()

	// Register various preview formats
	mail.RegisterPreview("Welcome Email", &WelcomeMailable{Name: "Bob"})
	mail.RegisterPreview("Closure Mail", func(ctx context.Context) (*mail.Message, error) {
		return mail.NewMessage().
			Subject("Direct Closure").
			HTML("<p>Closure content</p>").
			Text("Closure text"), nil
	})
	mail.RegisterPreview("Raw Message", mail.NewMessage().
		Subject("Direct Message").
		HTML("<b>Bold</b>").
		Text("Bold"))

	names := mail.GetPreviewNames()
	if len(names) != 3 {
		t.Fatalf("expected 3 previews registered, got %d", len(names))
	}

	app := golaravel.New()
	mail.MountPreviewStudio(app, "/mail/preview")

	// 1. Test Dashboard HTML
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/mail/preview?mail=Welcome%20Email", nil)
	app.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 OK for dashboard, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Mail Preview Studio") {
		t.Errorf("dashboard missing header")
	}
	if !strings.Contains(body, "Welcome Email") {
		t.Errorf("dashboard missing registered preview name")
	}

	// 2. Test Isolated Iframe HTML render
	wRender := httptest.NewRecorder()
	reqRender, _ := http.NewRequest("GET", "/mail/preview/render?mail=Welcome%20Email", nil)
	app.ServeHTTP(wRender, reqRender)
	if wRender.Code != http.StatusOK {
		t.Errorf("expected 200 OK for render, got %d", wRender.Code)
	}
	if !strings.Contains(wRender.Body.String(), "<h1>Welcome Bob</h1>") {
		t.Errorf("render output missing expected HTML content")
	}

	// 3. Test Raw MIME
	wMIME := httptest.NewRecorder()
	reqMIME, _ := http.NewRequest("GET", "/mail/preview/raw?mail=Welcome%20Email&format=mime", nil)
	app.ServeHTTP(wMIME, reqMIME)
	if wMIME.Code != http.StatusOK {
		t.Errorf("expected 200 OK for raw MIME, got %d", wMIME.Code)
	}
	if !strings.Contains(wMIME.Body.String(), "Subject: Welcome to GoLaravel, Bob") {
		t.Errorf("raw MIME missing Subject header")
	}

	// 4. Test Raw Text
	wText := httptest.NewRecorder()
	reqText, _ := http.NewRequest("GET", "/mail/preview/raw?mail=Welcome%20Email&format=text", nil)
	app.ServeHTTP(wText, reqText)
	if wText.Code != http.StatusOK {
		t.Errorf("expected 200 OK for raw text, got %d", wText.Code)
	}
	if !strings.Contains(wText.Body.String(), "Welcome Bob to GoLaravel") {
		t.Errorf("raw text missing content")
	}

	// 5. Test JSON Metadata API
	wAPI := httptest.NewRecorder()
	reqAPI, _ := http.NewRequest("GET", "/mail/preview/api/list", nil)
	app.ServeHTTP(wAPI, reqAPI)
	if wAPI.Code != http.StatusOK {
		t.Errorf("expected 200 OK for api list, got %d", wAPI.Code)
	}
	if !strings.Contains(wAPI.Body.String(), "Welcome Email") {
		t.Errorf("api list missing preview item")
	}

	// 6. Test Error Handling
	mail.RegisterPreview("Failing Mail", func(ctx context.Context) (*mail.Message, error) {
		return nil, errors.New("template compile error")
	})
	wErr := httptest.NewRecorder()
	reqErr, _ := http.NewRequest("GET", "/mail/preview/render?mail=Failing%20Mail", nil)
	app.ServeHTTP(wErr, reqErr)
	if wErr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for failing preview, got %d", wErr.Code)
	}
}
