package view

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	gcontext "github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/middleware"
)

func TestCSRFAndMethodHelpersEscape(t *testing.T) {
	var buf bytes.Buffer
	if err := CSRF(`tok"><script>`).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `name="_token"`) || strings.Contains(out, "<script>") {
		t.Fatalf("CSRF helper must escape the token: %s", out)
	}
	buf.Reset()
	_ = Method("PUT").Render(context.Background(), &buf)
	if !strings.Contains(buf.String(), `name="_method" value="PUT"`) {
		t.Fatalf("Method helper: %s", buf.String())
	}
}

func TestRawHTMLIsNotEscaped(t *testing.T) {
	var buf bytes.Buffer
	_ = RawHTML("<b>bold</b>").Render(context.Background(), &buf)
	if buf.String() != "<b>bold</b>" {
		t.Fatalf("RawHTML: %q", buf.String())
	}
}

func TestRenderAndCSRFToken(t *testing.T) {
	rec := httptest.NewRecorder()
	c := &gcontext.Context{}
	c.Reset(rec, httptest.NewRequest("GET", "/", nil))
	c.Set(middleware.CSRFContextKey, "abc123")
	if CSRFToken(c) != "abc123" {
		t.Fatalf("CSRFToken: %q", CSRFToken(c))
	}
	if err := Render(c, 201, CSRF(CSRFToken(c))); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `value="abc123"`) || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("Render: %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
}
