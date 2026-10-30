package artisan

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golaravel/golaravel/pkg/queue"
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
