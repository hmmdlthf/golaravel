package flutter

import (
	"strings"
	"testing"
)

func TestLoadingConfig_RenderHTML(t *testing.T) {
	cfg := DefaultLoadingConfig()
	cfg.BrandName = "Test App"
	cfg.Subtitle = "Loading test..."
	cfg.Style = LoadingStyleSpinner

	html := cfg.RenderHTML()
	if !strings.Contains(html, "gfl-preloader") {
		t.Errorf("expected preloader container in rendered HTML")
	}
	if !strings.Contains(html, "Test App") {
		t.Errorf("expected brand name in rendered HTML")
	}
	if !strings.Contains(html, "Loading test...") {
		t.Errorf("expected subtitle in rendered HTML")
	}

	// Test styles
	for _, style := range []LoadingStyle{LoadingStylePulse, LoadingStyleBar, LoadingStyleDots, LoadingStyleMinimal} {
		cfg.Style = style
		out := cfg.RenderHTML()
		if len(out) == 0 {
			t.Errorf("expected non-empty output for style %s", style)
		}
	}

	// Test InjectLoadingScreen
	baseHTML := []byte("<!DOCTYPE html><html><head></head><body><div>Content</div></body></html>")
	injected := InjectLoadingScreen(baseHTML, cfg)
	if !strings.Contains(string(injected), "gfl-preloader") {
		t.Errorf("expected injected preloader inside body")
	}
}
