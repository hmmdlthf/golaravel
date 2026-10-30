package session

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/golaravel"
)

func TestSession_Encrypter(t *testing.T) {
	enc := NewEncrypter("my-super-secret-key-32-chars-long!")

	plain := []byte("hello golaravel session encryption")
	ciphertext, err := enc.Encrypt(plain)
	if err != nil {
		t.Fatalf("failed encrypting: %v", err)
	}

	decrypted, err := enc.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("failed decrypting: %v", err)
	}

	if !bytes.Equal(plain, decrypted) {
		t.Fatalf("decrypted bytes mismatch: expected %s, got %s", plain, decrypted)
	}

	// Tampered payload must fail authentication
	tampered := ciphertext[:len(ciphertext)-5] + "AAAAA"
	_, err = enc.Decrypt(tampered)
	if err == nil {
		t.Fatal("expected decryption error on tampered payload")
	}
}

func TestSession_CRUD(t *testing.T) {
	s := NewSession()

	// 1. Put and Get
	s.Put("username", "taylor")
	s.Put("attempts", 3)
	s.Put("is_admin", true)

	if s.GetString("username") != "taylor" {
		t.Fatalf("expected taylor, got %s", s.GetString("username"))
	}
	if s.GetInt("attempts") != 3 {
		t.Fatalf("expected 3, got %d", s.GetInt("attempts"))
	}
	if !s.GetBool("is_admin") {
		t.Fatal("expected true for is_admin")
	}

	// 2. Default values
	if s.GetString("missing", "default_val") != "default_val" {
		t.Fatal("expected default value")
	}

	// 3. Push and Pull
	s.Push("tags", "tech")
	s.Push("tags", "golang")
	tags := s.Get("tags").([]any)
	if len(tags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(tags))
	}

	val := s.Pull("username")
	if val != "taylor" || s.Has("username") {
		t.Fatal("expected Pull to return value and delete key")
	}

	// 4. Forget and Flush
	s.Put("foo", "bar")
	s.Forget("foo")
	if s.Has("foo") {
		t.Fatal("expected foo to be forgotten")
	}

	s.Put("k1", "v1")
	s.Put("k2", "v2")
	s.Flush()
	if len(s.All()) != 0 {
		t.Fatal("expected empty session after Flush")
	}

	// 5. CSRF Token and ID Regeneration
	tok1 := s.Token()
	if len(tok1) != 40 {
		t.Fatalf("expected 40-char token, got %d", len(tok1))
	}
	tok2 := s.RegenerateToken()
	if tok1 == tok2 {
		t.Fatal("expected new token after RegenerateToken")
	}

	id1 := s.ID()
	_ = s.Regenerate()
	if id1 == s.ID() {
		t.Fatal("expected new ID after Regenerate")
	}
}

func TestSession_FlashAging(t *testing.T) {
	s := NewSession()

	// 1. Flash a message
	s.Flash("status", "Invoice Saved")

	if s.GetString("status") != "Invoice Saved" {
		t.Fatalf("expected Invoice Saved immediately after flash")
	}

	// Request 1 ends: AgeFlash called
	s.AgeFlash()

	// Request 2 begins: message is in oldFlash and still readable
	if s.GetString("status") != "Invoice Saved" {
		t.Fatalf("expected Invoice Saved in subsequent request")
	}

	// Request 2 ends: AgeFlash called again
	s.AgeFlash()

	// Request 3 begins: message must be purged
	if s.Has("status") || s.GetString("status") != "" {
		t.Fatal("expected flashed status to be purged after 2nd lifecycle aging")
	}
}

func TestSession_ReflashAndKeep(t *testing.T) {
	s := NewSession()

	s.Flash("notice", "Keep this alive")
	s.AgeFlash() // shifted to old

	// Keep notice for another cycle
	s.Keep("notice")
	s.AgeFlash() // shifted back to old

	if s.GetString("notice") != "Keep this alive" {
		t.Fatal("expected notice to be preserved by Keep")
	}

	// Reflash all
	s.Reflash()
	s.AgeFlash()
	if s.GetString("notice") != "Keep this alive" {
		t.Fatal("expected notice to be preserved by Reflash")
	}
}

func TestSession_FileDriver(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "golaravel_sess_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	driver, err := NewFileDriver(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	sessID := "test-session-123"
	payload := []byte(`{"id":"test-session-123","attributes":{"user":"alice"}}`)

	// Write
	if err := driver.Write(sessID, payload); err != nil {
		t.Fatalf("failed writing session file: %v", err)
	}

	// Read
	data, err := driver.Read(sessID)
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("read mismatch: %v", err)
	}

	// Destroy
	if err := driver.Destroy(sessID); err != nil {
		t.Fatalf("failed destroying session: %v", err)
	}

	_, err = driver.Read(sessID)
	if err != ErrSessionNotFound {
		t.Fatalf("expected ErrSessionNotFound after Destroy, got: %v", err)
	}
}

func TestSession_HTTP_Middleware_Lifecycle(t *testing.T) {
	mgr := NewManager(Config{
		CookieName: "test_session",
		Driver:     NewMemoryDriver(),
		SecretKey:  "testing-secret-key-32-chars-long",
	})

	app := golaravel.New()
	app.Use(StartSession(mgr))

	app.POST("/login", func(c *context.Context) error {
		s := FromContext(c)
		s.Put("user_id", "42")
		s.Flash("status", "Welcome Back!")
		return c.JSON(http.StatusOK, map[string]string{"msg": "logged in"})
	})

	app.GET("/dashboard", func(c *context.Context) error {
		s := FromContext(c)
		return c.JSON(http.StatusOK, map[string]any{
			"user_id": s.GetString("user_id"),
			"status":  s.GetString("status"),
		})
	})

	// 1. First Request: POST /login -> sets session and flash
	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("POST", "/login", nil)
	app.ServeHTTP(w1, req1)

	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w1.Code)
	}

	// Extract Set-Cookie
	cookies := w1.Result().Cookies()
	var sessCookie *http.Cookie
	for _, ck := range cookies {
		if ck.Name == "test_session" {
			sessCookie = ck
			break
		}
	}
	if sessCookie == nil || sessCookie.Value == "" {
		t.Fatal("expected test_session cookie in response")
	}

	// 2. Second Request: GET /dashboard with cookie -> reads user_id & flash status
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/dashboard", nil)
	req2.AddCookie(sessCookie)
	app.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w2.Code)
	}

	body2 := strings.TrimSpace(w2.Body.String())
	if body2 != `{"status":"Welcome Back!","user_id":"42"}` {
		t.Fatalf("expected flash status in 2nd request, got: %s", body2)
	}

	// Extract updated cookie from 2nd response
	for _, ck := range w2.Result().Cookies() {
		if ck.Name == "test_session" {
			sessCookie = ck
			break
		}
	}

	// 3. Third Request: GET /dashboard with updated cookie -> flash status MUST BE EXPIRED
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("GET", "/dashboard", nil)
	req3.AddCookie(sessCookie)
	app.ServeHTTP(w3, req3)

	body3 := strings.TrimSpace(w3.Body.String())
	if body3 != `{"status":"","user_id":"42"}` {
		t.Fatalf("expected flash status to be empty in 3rd request, got: %s", body3)
	}
}

func TestSession_CSRF_Verification(t *testing.T) {
	mgr := NewManager(Config{
		CookieName: "csrf_session",
		Driver:     NewMemoryDriver(),
		SecretKey:  "testing-secret-key-32-chars-long",
	})

	app := golaravel.New()
	app.Use(StartSession(mgr))
	app.Use(VerifyCSRFToken())

	app.GET("/form", func(c *context.Context) error {
		s := FromContext(c)
		return c.JSON(http.StatusOK, map[string]string{"token": s.Token()})
	})

	app.POST("/submit", func(c *context.Context) error {
		return c.String(http.StatusOK, "Submitted successfully")
	})

	// 1. GET /form -> retrieves CSRF token and session cookie
	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/form", nil)
	app.ServeHTTP(w1, req1)

	sessCookie := w1.Result().Cookies()[0]
	// Get token directly from cookie session
	s := mgr.loadSession(sessCookie.Value)
	csrfToken := s.Token()

	// 2. POST /submit with valid X-CSRF-TOKEN -> Success 200
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/submit", nil)
	req2.AddCookie(sessCookie)
	req2.Header.Set("X-CSRF-TOKEN", csrfToken)
	app.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid CSRF, got %d: %s", w2.Code, w2.Body.String())
	}

	// 3. POST /submit with missing/invalid token -> 403 Forbidden
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("POST", "/submit", nil)
	req3.AddCookie(sessCookie)
	req3.Header.Set("X-CSRF-TOKEN", "invalid-token")
	app.ServeHTTP(w3, req3)

	if w3.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for invalid CSRF token, got %d", w3.Code)
	}
}
