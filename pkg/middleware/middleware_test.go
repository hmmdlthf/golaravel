package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golaravel/golaravel/pkg/context"
)

func run(t *testing.T, req *http.Request, handlers ...context.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	c := &context.Context{}
	c.Reset(rec, req)
	handlers = append(handlers, func(c *context.Context) error { return c.String(http.StatusOK, "ok") })
	c.SetHandlers(handlers)
	c.Next()
	return rec
}

func reqWithOrigin(origin string) *http.Request {
	r := httptest.NewRequest("GET", "http://api.example.com/x", nil)
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	return r
}

func TestCORSOnlyReflectsListedOrigins(t *testing.T) {
	mw := CORS(CORSOptions{AllowedOrigins: []string{"https://app.example.com"}, AllowCredentials: true})
	rec := run(t, reqWithOrigin("https://evil.example"), mw)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" || rec.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("unlisted origin got CORS headers: %v", rec.Header())
	}
	rec = run(t, reqWithOrigin("https://app.example.com"), mw)
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" || rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("listed origin: %v", rec.Header())
	}
	if !strings.Contains(strings.Join(rec.Header().Values("Vary"), ","), "Origin") {
		t.Fatal("missing Vary: Origin")
	}
}

func TestCORSWildcardNeverSendsCredentials(t *testing.T) {
	mw := CORS(CORSOptions{AllowedOrigins: []string{"*"}, AllowCredentials: true})
	rec := run(t, reqWithOrigin("https://any.example"), mw)
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" || rec.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("wildcard: %v", rec.Header())
	}
}

func TestCORSAllowOriginFunc(t *testing.T) {
	mw := CORS(CORSOptions{AllowOriginFunc: func(o string) bool { return strings.HasPrefix(o, "http://localhost:") }})
	if got := run(t, reqWithOrigin("http://localhost:5173"), mw).Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("origin func: %q", got)
	}
}

func TestRecoveryDoesNotLeakPanicValue(t *testing.T) {
	rec := run(t, reqWithOrigin(""), Recovery(), func(c *context.Context) error { panic("db password=hunter2") })
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "hunter2") {
		t.Fatalf("recovery response: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSecureHeaders(t *testing.T) {
	rec := run(t, reqWithOrigin(""), SecureHeaders())
	if rec.Header().Get("X-XSS-Protection") != "0" || rec.Header().Get("Strict-Transport-Security") != "" {
		t.Fatalf("plain HTTP headers: %v", rec.Header())
	}
	r := reqWithOrigin("")
	r.Header.Set("X-Forwarded-Proto", "https")
	if run(t, r, SecureHeaders()).Header().Get("Strict-Transport-Security") == "" {
		t.Fatal("HSTS missing on HTTPS")
	}
}

func TestCSRFRejectsWrongToken(t *testing.T) {
	r := httptest.NewRequest("POST", "http://example.com/x", strings.NewReader("_token=wrong"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "right"})
	if rec := run(t, r, CSRF()); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong token: %d", rec.Code)
	}
	r = httptest.NewRequest("POST", "http://example.com/x", strings.NewReader("_token=right"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "right"})
	if rec := run(t, r, CSRF()); rec.Code != http.StatusOK {
		t.Fatalf("right token: %d", rec.Code)
	}
}

func TestRateLimiterUsesClientIP(t *testing.T) {
	mw := RateLimiter(1)
	// Two clients behind the same local proxy must not share a bucket.
	for _, ip := range []string{"198.51.100.1", "198.51.100.2"} {
		r := httptest.NewRequest("GET", "http://example.com/x", nil)
		r.RemoteAddr = "127.0.0.1:9000"
		r.Header.Set("X-Forwarded-For", ip)
		if rec := run(t, r, mw); rec.Code != http.StatusOK {
			t.Fatalf("%s: first request limited: %d", ip, rec.Code)
		}
	}
	r := httptest.NewRequest("GET", "http://example.com/x", nil)
	r.RemoteAddr = "127.0.0.1:9000"
	r.Header.Set("X-Forwarded-For", "198.51.100.1")
	if rec := run(t, r, mw); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request from the same client: %d", rec.Code)
	}
}

func TestSecureHeadersOptions(t *testing.T) {
	r := reqWithOrigin("")
	r.Header.Set("X-Forwarded-Proto", "https")
	h := run(t, r, SecureHeaders(SecureHeadersOptions{ContentSecurityPolicy: StrictCSP, FrameOptions: "DENY", HSTSMaxAge: 600, PermissionsPolicy: "camera=()"})).Header()
	if h.Get("Content-Security-Policy") != StrictCSP || h.Get("X-Frame-Options") != "DENY" ||
		h.Get("Strict-Transport-Security") != "max-age=600" || h.Get("Permissions-Policy") != "camera=()" {
		t.Fatalf("options not applied: %v", h)
	}
	h = run(t, r, SecureHeaders(SecureHeadersOptions{ContentSecurityPolicy: StrictCSP, CSPReportOnly: true, HSTSMaxAge: -1})).Header()
	if h.Get("Content-Security-Policy-Report-Only") != StrictCSP || h.Get("Content-Security-Policy") != "" || h.Get("Strict-Transport-Security") != "" {
		t.Fatalf("report-only / HSTS off: %v", h)
	}
}
