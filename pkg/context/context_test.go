package context_test

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/golaravel/golaravel/pkg/context"
)

func TestContext_FormFile_And_SaveUploadedFile(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	// Write text field
	if err := writer.WriteField("title", "My Avatar"); err != nil {
		t.Fatal(err)
	}

	// Write file field
	part, err := writer.CreateFormFile("avatar", "test_photo.png")
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("PNG_IMAGE_TEST_PAYLOAD")
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	ctx := context.NewContext()
	ctx.Reset(w, req)

	// 1. FormValue
	if title := ctx.FormValue("title"); title != "My Avatar" {
		t.Fatalf("expected title 'My Avatar', got '%s'", title)
	}

	// 2. FormFile
	fh, err := ctx.FormFile("avatar")
	if err != nil {
		t.Fatalf("FormFile failed: %v", err)
	}
	if fh.Filename != "test_photo.png" {
		t.Fatalf("expected filename 'test_photo.png', got '%s'", fh.Filename)
	}

	// 3. SaveUploadedFile
	tmpDir, err := os.MkdirTemp("", "ctx_upload_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dst := filepath.Join(tmpDir, "saved_photo.png")
	if err := ctx.SaveUploadedFile(fh, dst); err != nil {
		t.Fatalf("SaveUploadedFile failed: %v", err)
	}

	savedBytes, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed reading saved file: %v", err)
	}
	if !bytes.Equal(savedBytes, content) {
		t.Fatalf("saved content mismatch")
	}

	// 4. Missing FormFile returns error
	if _, err := ctx.FormFile("nonexistent"); err == nil {
		t.Fatal("expected error for non-existent file field")
	}
}

func TestContext_BindJSON_And_JSON(t *testing.T) {
	type Payload struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}

	reqBody := `{"name":"Ahmad","email":"ahmad@example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/users", io.NopCloser(bytes.NewReader([]byte(reqBody))))
	w := httptest.NewRecorder()

	ctx := context.NewContext()
	ctx.Reset(w, req)

	var p Payload
	if err := ctx.BindJSON(&p); err != nil {
		t.Fatalf("BindJSON failed: %v", err)
	}
	if p.Name != "Ahmad" || p.Email != "ahmad@example.com" {
		t.Fatalf("payload mismatch: %+v", p)
	}

	err := ctx.JSON(http.StatusCreated, map[string]string{"status": "ok"})
	if err != nil {
		t.Fatalf("JSON response failed: %v", err)
	}

	if w.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", w.Code)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"status":"ok"`)) {
		t.Fatalf("unexpected response body: %s", w.Body.String())
	}
}

func TestHandlerChainLongerThan127(t *testing.T) {
	c := context.NewContext()
	c.Reset(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	calls := 0
	handlers := make([]context.HandlerFunc, 300)
	for i := range handlers {
		handlers[i] = func(*context.Context) error { calls++; return nil }
	}
	c.SetHandlers(handlers)
	if err := c.Next(); err != nil {
		t.Fatal(err)
	}
	if calls != 300 || !c.IsAborted() {
		t.Fatalf("ran %d of 300 handlers", calls)
	}
}
