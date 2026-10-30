package flutter

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/router"
)

func TestFlutter_ServeWeb(t *testing.T) {
	// Create mock web dist directory
	tmpDir, err := os.MkdirTemp("", "flutter_web_dist_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	indexHTML := `<!DOCTYPE html><html><head><title>Flutter Web</title></head><body>Flutter App Body</body></html>`
	if err := os.WriteFile(filepath.Join(tmpDir, "index.html"), []byte(indexHTML), 0644); err != nil {
		t.Fatal(err)
	}

	mainDartJS := `console.log("Flutter engine loaded");`
	if err := os.WriteFile(filepath.Join(tmpDir, "main.dart.js"), []byte(mainDartJS), 0644); err != nil {
		t.Fatal(err)
	}

	r := router.New()
	ServeWeb(r, Options{
		MountPath:   "/app",
		DistDir:     tmpDir,
		FallbackSPA: true,
	})

	// 1. Test fetching static asset
	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodGet, "/app/main.dart.js", nil)
	c1 := context.NewContext()
	c1.Reset(w1, req1)
	handlers := r.Match("GET", "/app/main.dart.js", c1)
	if handlers == nil {
		t.Fatal("expected match for /app/main.dart.js")
	}
	c1.SetHandlers(handlers)
	_ = c1.Next()

	if !strings.Contains(w1.Body.String(), `console.log("Flutter engine loaded")`) {
		t.Fatalf("expected main.dart.js content, got %s", w1.Body.String())
	}

	// 2. Test SPA fallback for client-side route (/app/dashboard/settings)
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/app/dashboard/settings", nil)
	c2 := context.NewContext()
	c2.Reset(w2, req2)
	handlers2 := r.Match("GET", "/app/dashboard/settings", c2)
	if handlers2 == nil {
		t.Fatal("expected match for /app/dashboard/settings")
	}
	c2.SetHandlers(handlers2)
	_ = c2.Next()

	if !strings.Contains(w2.Body.String(), "<title>Flutter Web</title>") {
		t.Fatalf("expected fallback to index.html, got %s", w2.Body.String())
	}
}
