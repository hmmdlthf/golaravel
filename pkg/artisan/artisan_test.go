package artisan

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golaravel/golaravel/pkg/queue"
	"github.com/golaravel/golaravel/pkg/schedule"
)

func TestArtisan_QueueCommands(t *testing.T) {
	reg := NewRegistry()

	// Verify all queue commands are registered
	expectedCmds := []string{"queue:work", "queue:failed", "queue:retry", "queue:flush"}
	for _, cmd := range expectedCmds {
		if _, ok := reg.commands[cmd]; !ok {
			t.Fatalf("expected command '%s' to be registered", cmd)
		}
	}

	dlq := queue.DefaultQueue().DLQ()
	_ = dlq.Flush()

	// 1. Test queue:failed on empty DLQ
	if err := runQueueFailed(nil); err != nil {
		t.Fatalf("runQueueFailed failed on empty DLQ: %v", err)
	}

	// 2. Add a failed job to DLQ
	failedJob := &queue.FailedJob{
		ID:        "artisan_failed_1",
		Queue:     "default",
		Type:      "SendEmailJob",
		Payload:   `{"email":"user@example.com"}`,
		Exception: "SMTP connection failed",
		FailedAt:  time.Now(),
	}
	if err := dlq.Log(failedJob); err != nil {
		t.Fatalf("failed to log to DLQ: %v", err)
	}

	// 3. Test queue:failed with populated DLQ
	if err := runQueueFailed(nil); err != nil {
		t.Fatalf("runQueueFailed failed with populated DLQ: %v", err)
	}

	// 4. Test queue:retry <id>
	if err := runQueueRetry([]string{"artisan_failed_1"}); err != nil {
		t.Fatalf("runQueueRetry failed: %v", err)
	}

	// DLQ should now be empty
	cnt, _ := dlq.Count()
	if cnt != 0 {
		t.Fatalf("expected 0 in DLQ after retry, got %d", cnt)
	}

	// 5. Test queue:retry all
	_ = dlq.Log(&queue.FailedJob{
		ID:        "artisan_failed_2",
		Queue:     "default",
		Type:      "SendEmailJob",
		Payload:   `{"email":"user2@example.com"}`,
		Exception: "SMTP timeout",
		FailedAt:  time.Now(),
	})

	if err := runQueueRetry([]string{"all"}); err != nil {
		t.Fatalf("runQueueRetry all failed: %v", err)
	}

	// 6. Test queue:flush
	_ = dlq.Log(&queue.FailedJob{
		ID:        "artisan_failed_3",
		Queue:     "default",
		Type:      "SendEmailJob",
		Payload:   `{"email":"user3@example.com"}`,
		Exception: "SMTP timeout",
		FailedAt:  time.Now(),
	})

	if err := runQueueFlush(nil); err != nil {
		t.Fatalf("runQueueFlush failed: %v", err)
	}

	cntAfterFlush, _ := dlq.Count()
	if cntAfterFlush != 0 {
		t.Fatalf("expected 0 DLQ after flush, got %d", cntAfterFlush)
	}
}

func TestMakeOAuth_Scaffold(t *testing.T) {
	reg := NewRegistry()
	if _, ok := reg.commands["make:oauth"]; !ok {
		t.Fatal("expected make:oauth command to be registered in artisan registry")
	}

	// Clean up any test artifact
	targetFile := "app/handlers/github_oauth_controller.go"
	_ = os.Remove(targetFile)
	defer os.Remove(targetFile)

	// Missing provider name
	if err := makeOAuth(nil); err == nil {
		t.Fatal("expected error when provider name is missing")
	}

	// Scaffold GitHub OAuth controller
	if err := makeOAuth([]string{"GitHub"}); err != nil {
		t.Fatalf("makeOAuth failed: %v", err)
	}

	content, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("failed to read created file: %v", err)
	}

	strContent := string(content)
	if !strings.Contains(strContent, "GitHubOAuthController") {
		t.Fatal("file does not contain GitHubOAuthController")
	}
	if !strings.Contains(strContent, `oauth.Driver("github").Redirect(c)`) {
		t.Fatal("file does not contain redirect handler")
	}
	if !strings.Contains(strContent, `oauth.Driver("github").User(c)`) {
		t.Fatal("file does not contain user callback handler")
	}
}

func TestHealthCheckCommand(t *testing.T) {
	reg := NewRegistry()
	if _, ok := reg.commands["health"]; !ok {
		t.Fatal("expected 'health' command to be registered in artisan registry")
	}

	// Run artisan health
	if err := runHealthCheck(nil); err != nil {
		t.Fatalf("runHealthCheck failed: %v", err)
	}
}

func TestScheduleCommands(t *testing.T) {
	reg := NewRegistry()

	// Verify all schedule commands are registered
	scheduleCmds := []string{"schedule:run", "schedule:work", "schedule:list"}
	for _, cmd := range scheduleCmds {
		if _, ok := reg.commands[cmd]; !ok {
			t.Fatalf("expected command '%s' to be registered", cmd)
		}
	}

	// Clean scheduler
	schedule.Clear()

	// 1. Test schedule:list on empty list
	if err := runScheduleList(nil); err != nil {
		t.Fatalf("runScheduleList on empty failed: %v", err)
	}

	// 2. Register mock tasks
	var executed bool
	schedule.Call(func(ctx context.Context) error {
		executed = true
		return nil
	}).Name("mock:sync").EveryMinute().WithoutOverlapping()

	schedule.Call(func(ctx context.Context) error {
		return errors.New("failing-task-error")
	}).Name("mock:fail").EveryMinute().OnOneServer()

	// 3. Test schedule:list with registered tasks
	if err := runScheduleList(nil); err != nil {
		t.Fatalf("runScheduleList failed: %v", err)
	}

	// 4. Test schedule:run
	err := runScheduleRun(nil)
	if err == nil {
		t.Fatal("expected runScheduleRun to report error from failing task")
	}
	if !executed {
		t.Fatal("expected mock:sync to execute")
	}

	// 5. Clean scheduler after test
	schedule.Clear()
}

func TestMakeMail_Scaffolding(t *testing.T) {
	// 1. Missing name error
	if err := makeMail(nil); err == nil {
		t.Fatal("expected error on missing mail name")
	}

	// 2. Standard Mailable
	mailFile := "app/mail/welcome_mail.go"
	_ = os.Remove(mailFile)
	defer os.Remove(mailFile)

	if err := makeMail([]string{"Welcome"}); err != nil {
		t.Fatalf("makeMail failed: %v", err)
	}

	content, err := os.ReadFile(mailFile)
	if err != nil {
		t.Fatalf("failed to read generated mail file: %v", err)
	}
	strContent := string(content)
	if !strings.Contains(strContent, "type WelcomeMail struct") {
		t.Errorf("mail file missing WelcomeMail struct")
	}
	if !strings.Contains(strContent, "func (m *WelcomeMail) Build(ctx context.Context) (*mail.Message, error)") {
		t.Errorf("mail file missing Mailable Build method")
	}

	// 3. Mailable with Templ View (--view)
	invoiceMail := "app/mail/invoice_mail.go"
	invoiceView := "app/views/mail/invoice.templ"
	_ = os.Remove(invoiceMail)
	_ = os.Remove(invoiceView)
	defer os.Remove(invoiceMail)
	defer os.Remove(invoiceView)

	if err := makeMail([]string{"Invoice", "--view"}); err != nil {
		t.Fatalf("makeMail with --view failed: %v", err)
	}

	mailBytes, _ := os.ReadFile(invoiceMail)
	if !strings.Contains(string(mailBytes), "View(ctx, mailviews.InvoiceEmail(m.Name))") {
		t.Errorf("mail with --view missing View(...) call")
	}

	viewBytes, err := os.ReadFile(invoiceView)
	if err != nil {
		t.Fatalf("failed to read generated templ view: %v", err)
	}
	if !strings.Contains(string(viewBytes), "templ InvoiceEmail(name string)") {
		t.Errorf("templ view missing templ InvoiceEmail declaration")
	}
}

func TestMakeLimiter_Scaffolding(t *testing.T) {
	reg := NewRegistry()
	if _, ok := reg.commands["make:limiter"]; !ok {
		t.Fatal("expected make:limiter to be registered in artisan registry")
	}

	// Missing name
	if err := makeLimiter(nil); err == nil {
		t.Fatal("expected error on missing limiter name")
	}

	// Scaffold API limiter
	targetFile := "app/limiters/api_limiter.go"
	_ = os.Remove(targetFile)
	defer os.Remove(targetFile)

	if err := makeLimiter([]string{"Api"}); err != nil {
		t.Fatalf("makeLimiter failed: %v", err)
	}

	content, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("failed to read created limiter file: %v", err)
	}

	str := string(content)
	if !strings.Contains(str, `ratelimit.For("api"`) {
		t.Errorf("limiter file missing ratelimit.For(\"api\") registration")
	}
	if !strings.Contains(str, "ratelimit.PerMinute(120)") {
		t.Errorf("limiter file missing tiered throughput rule")
	}
}

func TestMakeFactory_Scaffolding(t *testing.T) {
	reg := NewRegistry()
	if _, ok := reg.commands["make:factory"]; !ok {
		t.Fatal("expected make:factory to be registered in artisan registry")
	}

	// Missing name
	if err := makeFactory(nil); err == nil {
		t.Fatal("expected error on missing factory name")
	}

	// Scaffold User factory
	targetFile := "database/factories/user_factory.go"
	_ = os.Remove(targetFile)
	defer os.Remove(targetFile)

	if err := makeFactory([]string{"User"}); err != nil {
		t.Fatalf("makeFactory failed: %v", err)
	}

	content, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("failed to read created factory file: %v", err)
	}

	str := string(content)
	if !strings.Contains(str, "type UserDef struct") {
		t.Errorf("factory file missing UserDef struct")
	}
	if !strings.Contains(str, "func UserFactory() *factory.Factory[UserDef]") {
		t.Errorf("factory file missing UserFactory constructor")
	}
}

func TestArtisan_Version(t *testing.T) {
	reg := NewRegistry()

	// Test flag invocations
	for _, flag := range []string{"version", "--version", "-v", "-V"} {
		if err := reg.Execute([]string{flag}); err != nil {
			t.Errorf("expected flag %s to succeed without error, got: %v", flag, err)
		}
	}
}

func TestMakeLang_Scaffolding(t *testing.T) {
	if err := makeLang(nil); err == nil {
		t.Fatal("expected error on missing locale")
	}

	testLocale := "test_es"
	defer os.RemoveAll(filepath.Join("lang", testLocale))
	defer os.Remove(filepath.Join("lang", testLocale+".json"))

	if err := makeLang([]string{testLocale}); err != nil {
		t.Fatalf("makeLang failed: %v", err)
	}

	expectedFiles := []string{
		filepath.Join("lang", testLocale, "auth.json"),
		filepath.Join("lang", testLocale, "validation.json"),
		filepath.Join("lang", testLocale, "pagination.json"),
		filepath.Join("lang", testLocale, "messages.json"),
		filepath.Join("lang", testLocale+".json"),
	}

	for _, f := range expectedFiles {
		if _, err := os.Stat(f); os.IsNotExist(err) {
			t.Errorf("expected scaffolded file %s does not exist", f)
		}
	}
}

// The generated OAuth controller must compile against the current API.
func TestMakeOAuth_GeneratedCodeCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go vet")
	}
	targetFile := "app/handlers/discord_oauth_controller.go"
	_ = os.Remove(targetFile)
	defer os.Remove(targetFile)
	if err := makeOAuth([]string{"Discord"}); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join("testdata", "oauthgen")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll("testdata")
	if err := os.WriteFile(filepath.Join(dir, "controller.go"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("go", "vet", "./"+filepath.ToSlash(dir)).CombinedOutput()
	if err != nil {
		t.Fatalf("generated controller does not compile:\n%s", out)
	}
	if strings.Contains(string(src), "TODO") || strings.Contains(string(src), `"user":    socialUser`) {
		t.Fatal("template still has the TODO or leaks the provider profile")
	}
}
