package translation_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/golaravel"
	"github.com/golaravel/golaravel/pkg/session"
	"github.com/golaravel/golaravel/pkg/translation"
)

func TestInterpolate_BasicAndCasing(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		params   map[string]any
		expected string
	}{
		{
			name:     "basic placeholder",
			line:     "Hello, :name!",
			params:   map[string]any{"name": "Alice"},
			expected: "Hello, Alice!",
		},
		{
			name:     "multiple placeholders",
			line:     ":greeting, :name! Welcome to :app.",
			params:   map[string]any{"greeting": "Hi", "name": "Bob", "app": "GoLaravel"},
			expected: "Hi, Bob! Welcome to GoLaravel.",
		},
		{
			name:     "case sensitivity - lower",
			line:     "user :name has signed in",
			params:   map[string]any{"name": "alice"},
			expected: "user alice has signed in",
		},
		{
			name:     "case sensitivity - title",
			line:     "User :Name has signed in",
			params:   map[string]any{"name": "alice"},
			expected: "User Alice has signed in",
		},
		{
			name:     "case sensitivity - upper",
			line:     "ATTENTION: :NAME",
			params:   map[string]any{"name": "alice"},
			expected: "ATTENTION: ALICE",
		},
		{
			name:     "numeric and boolean types",
			line:     "User ID: :id, Active: :active, Balance: :balance",
			params:   map[string]any{"id": 42, "active": true, "balance": 150.75},
			expected: "User ID: 42, Active: true, Balance: 150.75",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := translation.Interpolate(tt.line, tt.params)
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestSelectPlural(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		number   int
		expected string
	}{
		{
			name:     "simple singular",
			line:     "apple|apples",
			number:   1,
			expected: "apple",
		},
		{
			name:     "simple plural",
			line:     "apple|apples",
			number:   0,
			expected: "apples",
		},
		{
			name:     "simple plural greater than 1",
			line:     "apple|apples",
			number:   5,
			expected: "apples",
		},
		{
			name:     "explicit rules exact match zero",
			line:     "{0} There are none|{1} There is one|[2,*] There are many",
			number:   0,
			expected: "There are none",
		},
		{
			name:     "explicit rules exact match one",
			line:     "{0} There are none|{1} There is one|[2,*] There are many",
			number:   1,
			expected: "There is one",
		},
		{
			name:     "explicit rules range match",
			line:     "{0} There are none|{1} There is one|[2,*] There are many",
			number:   10,
			expected: "There are many",
		},
		{
			name:     "bounded range match",
			line:     "[1,4] A few|[5,10] Several|[11,*] Many",
			number:   3,
			expected: "A few",
		},
		{
			name:     "bounded range match second",
			line:     "[1,4] A few|[5,10] Several|[11,*] Many",
			number:   7,
			expected: "Several",
		},
		{
			name:     "bounded range match unbounded",
			line:     "[1,4] A few|[5,10] Several|[11,*] Many",
			number:   25,
			expected: "Many",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := translation.SelectPlural(tt.line, tt.number)
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestTranslator_FSLoadingAndFallback(t *testing.T) {
	mockFS := fstest.MapFS{
		"lang/en/auth.json": &fstest.MapFile{
			Data: []byte(`{
				"failed": "These credentials do not match our records.",
				"throttle": "Too many login attempts. Please try again in :seconds seconds."
			}`),
		},
		"lang/en/messages.json": &fstest.MapFile{
			Data: []byte(`{
				"welcome": "Welcome, :name!",
				"apples": "{0} No apples|{1} :count apple|[2,*] :count apples",
				"nested": {
					"title": "Nested Title"
				}
			}`),
		},
		"lang/es/auth.json": &fstest.MapFile{
			Data: []byte(`{
				"failed": "Estas credenciales no coinciden con nuestros registros."
			}`),
		},
		"lang/es.json": &fstest.MapFile{
			Data: []byte(`{
				"Save": "Guardar",
				"Cancel": "Cancelar"
			}`),
		},
	}

	tr := translation.New(
		translation.WithLocale("es"),
		translation.WithFallback("en"),
	)

	if err := tr.LoadFS(mockFS, "lang"); err != nil {
		t.Fatalf("LoadFS failed: %v", err)
	}

	// 1. Existing key in active locale "es"
	if got := tr.Get("auth.failed"); got != "Estas credenciales no coinciden con nuestros registros." {
		t.Errorf("unexpected translated line: %q", got)
	}

	// 2. Direct root JSON translation in "es"
	if got := tr.Get("Save"); got != "Guardar" {
		t.Errorf("unexpected direct translation: %q", got)
	}

	// 3. Fallback to "en" when missing in "es"
	if got := tr.Get("auth.throttle", map[string]any{"seconds": 60}); got != "Too many login attempts. Please try again in 60 seconds." {
		t.Errorf("unexpected fallback translation: %q", got)
	}

	// 4. Fallback nested key
	if got := tr.Get("messages.nested.title"); got != "Nested Title" {
		t.Errorf("unexpected nested key: %q", got)
	}

	// 5. Pluralization in fallback locale
	if got := tr.Choice("messages.apples", 0); got != "No apples" {
		t.Errorf("unexpected plural 0: %q", got)
	}
	if got := tr.Choice("messages.apples", 1); got != "1 apple" {
		t.Errorf("unexpected plural 1: %q", got)
	}
	if got := tr.Choice("messages.apples", 5); got != "5 apples" {
		t.Errorf("unexpected plural 5: %q", got)
	}

	// 6. Non-existent key returns key name
	if got := tr.Get("non.existent.key"); got != "non.existent.key" {
		t.Errorf("expected key itself, got: %q", got)
	}
}

func TestTranslator_AddLinesAndAddJSON(t *testing.T) {
	tr := translation.New(translation.WithLocale("fr"))

	tr.AddLines(map[string]string{
		"hello": "Bonjour :name",
	}, "fr", "common")

	if got := tr.Get("common.hello", map[string]any{"name": "Jean"}); got != "Bonjour Jean" {
		t.Errorf("unexpected translation: %q", got)
	}

	err := tr.AddJSON([]byte(`{"submit": "Soumettre"}`), "fr")
	if err != nil {
		t.Fatalf("AddJSON failed: %v", err)
	}

	if got := tr.Get("submit"); got != "Soumettre" {
		t.Errorf("unexpected translation: %q", got)
	}
}

func TestTranslator_ContextIntegration(t *testing.T) {
	tr := translation.New(translation.WithLocale("en"), translation.WithFallback("en"))
	tr.AddLines(map[string]string{
		"greeting": "Hello, :name!",
		"items":    "{0} None|{1} 1 item|[2,*] :count items",
	}, "en", "ui")
	tr.AddLines(map[string]string{
		"greeting": "¡Hola, :name!",
		"items":    "{0} Ninguno|{1} 1 elemento|[2,*] :count elementos",
	}, "es", "ui")

	translation.SetDefault(tr)

	c := context.NewContext()
	c.SetLocale("es")

	if got := c.GetLocale(); got != "es" {
		t.Fatalf("expected locale 'es', got %q", got)
	}

	if got := c.Trans("ui.greeting", map[string]any{"name": "Carlos"}); got != "¡Hola, Carlos!" {
		t.Errorf("unexpected context Trans: %q", got)
	}

	if got := c.TransChoice("ui.items", 3); got != "3 elementos" {
		t.Errorf("unexpected context TransChoice: %q", got)
	}
}

func TestMiddleware_LocaleResolution(t *testing.T) {
	tr := translation.New(translation.WithLocale("en"))
	tr.AddLines(map[string]string{"msg": "English"}, "en", "app")
	tr.AddLines(map[string]string{"msg": "Español"}, "es", "app")
	tr.AddLines(map[string]string{"msg": "Français"}, "fr", "app")
	tr.AddLines(map[string]string{"msg": "العربية"}, "ar", "app")
	translation.SetDefault(tr)

	cfg := translation.LocaleConfig{
		DefaultLocale:    "en",
		SupportedLocales: []string{"en", "es", "fr", "ar"},
		ParamName:        "lang",
		CookieName:       "locale",
		PersistCookie:    true,
		PersistSession:   true,
	}

	sessionManager := session.NewManager(session.Config{
		CookieName: "test_session",
		Driver:     session.NewMemoryDriver(),
		SecretKey:  "testing-secret-key-32-chars-long",
	})

	app := golaravel.New()
	app.Use(session.StartSession(sessionManager))
	app.Use(translation.Middleware(cfg))

	app.GET("/test", func(c *context.Context) error {
		return c.String(http.StatusOK, "%s", c.Trans("app.msg"))
	})
	app.GET("/locale/:lang/hello", func(c *context.Context) error {
		return c.String(http.StatusOK, "%s", c.Trans("app.msg"))
	})

	// 1. Query parameter resolution: ?lang=fr
	req := httptest.NewRequest("GET", "/test?lang=fr", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Body.String() != "Français" {
		t.Errorf("expected Français, got: %q", rec.Body.String())
	}
	if rec.Header().Get("Content-Language") != "fr" {
		t.Errorf("expected Content-Language 'fr', got: %q", rec.Header().Get("Content-Language"))
	}

	// 2. Route parameter resolution: /locale/ar/hello
	req = httptest.NewRequest("GET", "/locale/ar/hello", nil)
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Body.String() != "العربية" {
		t.Errorf("expected العربية, got: %q", rec.Body.String())
	}

	// 3. Accept-Language header resolution: es-ES,es;q=0.9,en;q=0.8
	req = httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Accept-Language", "es-ES,es;q=0.9,en;q=0.8")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Body.String() != "Español" {
		t.Errorf("expected Español from header, got: %q", rec.Body.String())
	}

	// 4. Cookie resolution
	req = httptest.NewRequest("GET", "/test", nil)
	req.AddCookie(&http.Cookie{Name: "locale", Value: "fr"})
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Body.String() != "Français" {
		t.Errorf("expected Français from cookie, got: %q", rec.Body.String())
	}

	// 5. Fallback to default locale if unsupported
	req = httptest.NewRequest("GET", "/test?lang=de", nil)
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Body.String() != "English" {
		t.Errorf("expected fallback to English, got: %q", rec.Body.String())
	}
}

func TestTranslator_LoadFromDir(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "lang_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	enDir := filepath.Join(tempDir, "en")
	_ = os.MkdirAll(enDir, 0755)

	authFile := filepath.Join(enDir, "auth.json")
	_ = os.WriteFile(authFile, []byte(`{"failed": "Invalid credentials"}`), 0644)

	tr := translation.New()
	if err := tr.LoadDir(tempDir); err != nil {
		t.Fatalf("LoadDir failed: %v", err)
	}

	if got := tr.Get("auth.failed"); got != "Invalid credentials" {
		t.Errorf("expected 'Invalid credentials', got: %q", got)
	}
}
