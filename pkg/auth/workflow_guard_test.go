package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golaravel/golaravel/pkg/golaravel"
	"github.com/golaravel/golaravel/pkg/hash"
	"github.com/golaravel/golaravel/pkg/notification"
	"github.com/golaravel/golaravel/pkg/ratelimit"
)

type guardFixture struct {
	app    *golaravel.Engine
	user   *SimpleWorkflowUser
	secret string
	mails  *int
}

func newGuardFixture(t *testing.T) guardFixture {
	t.Helper()
	ratelimit.SetStore(ratelimit.NewMemoryStore())
	pw, _ := hash.Make("correct-horse")
	totp := NewTOTPManager()
	secret, _ := totp.GenerateSecret()
	_, hashed, _ := totp.GenerateRecoveryCodes(2)
	u := &SimpleWorkflowUser{ID: "u1", Email: "ann@example.com", PasswordHash: pw, TwoFactorEnabled: true, TwoFactorSecret: secret, RecoveryCodes: hashed}
	find := func(_ context.Context, email string) (AuthenticatableUser, error) {
		if email == u.Email {
			return u, nil
		}
		return nil, fmt.Errorf("not found")
	}
	mails := 0
	notification.RegisterChannel("mail", countingChannel{n: &mails})
	ctrl := NewWorkflowController(WorkflowConfig{
		SecretKey: "guard-test-secret-32-characters!!",
		Callbacks: WorkflowCallbacks{
			FindByEmail: find,
			FindByID: func(_ context.Context, id string) (AuthenticatableUser, error) {
				if id == u.ID {
					return u, nil
				}
				return nil, fmt.Errorf("not found")
			},
			DisableTwoFactor: func(context.Context, string) error { u.TwoFactorEnabled = false; return nil },
		},
	})
	app := golaravel.New()
	ctrl.RegisterRoutes(app.Router)
	return guardFixture{app: app, user: u, secret: secret, mails: &mails}
}

func (f guardFixture) post(path, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "198.51.100.9:1000"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	f.app.ServeHTTP(rec, req)
	return rec
}

func TestLoginIsThrottled(t *testing.T) {
	f := newGuardFixture(t)
	for i := 0; i < LoginAttempts; i++ {
		if rec := f.post("/auth/login", `{"email":"ann@example.com","password":"wrong"}`, ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i, rec.Code)
		}
	}
	if rec := f.post("/auth/login", `{"email":"ann@example.com","password":"correct-horse"}`, ""); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("login after %d failures must be throttled, got %d", LoginAttempts, rec.Code)
	}
	if rec := f.post("/auth/login", `{"email":"nobody@example.com","password":"x"}`, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown user: %d", rec.Code)
	}
}

func challenge(t *testing.T, f guardFixture) string {
	t.Helper()
	ratelimit.SetStore(ratelimit.NewMemoryStore())
	rec := f.post("/auth/login", `{"email":"ann@example.com","password":"correct-horse"}`, "")
	var m map[string]any
	_ = jsonUnmarshal(rec.Body.Bytes(), &m)
	tok, _ := m["two_factor_token"].(string)
	if tok == "" {
		t.Fatalf("no 2FA challenge: %s", rec.Body.String())
	}
	return tok
}

func TestTwoFactorThrottleAndReplay(t *testing.T) {
	f := newGuardFixture(t)
	tok := challenge(t, f)
	for i := 0; i < TwoFactorAttempts; i++ {
		f.post("/auth/two-factor-challenge", fmt.Sprintf(`{"two_factor_token":%q,"code":"000000"}`, tok), "")
	}
	code, _ := NewTOTPManager().GenerateCode(f.secret)
	if rec := f.post("/auth/two-factor-challenge", fmt.Sprintf(`{"two_factor_token":%q,"code":%q}`, tok, code), ""); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("2FA brute force must be throttled: %d", rec.Code)
	}

	ratelimit.SetStore(ratelimit.NewMemoryStore())
	if rec := f.post("/auth/two-factor-challenge", fmt.Sprintf(`{"two_factor_token":%q,"code":%q}`, tok, code), ""); rec.Code != http.StatusOK {
		t.Fatalf("valid code: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.post("/auth/two-factor-challenge", fmt.Sprintf(`{"two_factor_token":%q,"code":%q}`, tok, code), ""); rec.Code == http.StatusOK {
		t.Fatal("the same TOTP code must not work twice")
	}
	if rec := f.post("/auth/two-factor-challenge", fmt.Sprintf(`{"two_factor_token":%q,"recovery_code":"anything"}`, tok), ""); rec.Code == http.StatusOK {
		t.Fatal("recovery code accepted without a way to consume it")
	}
}

func TestForgotPasswordOnlyMailsAccounts(t *testing.T) {
	f := newGuardFixture(t)
	if rec := f.post("/auth/forgot-password", `{"email":"stranger@example.com"}`, ""); rec.Code != http.StatusOK {
		t.Fatalf("unknown address must get the same answer: %d", rec.Code)
	}
	if *f.mails != 0 {
		t.Fatal("reset mail sent to an address without an account")
	}
	f.post("/auth/forgot-password", `{"email":"ann@example.com"}`, "")
	if *f.mails != 1 {
		t.Fatalf("reset mail for the account: %d", *f.mails)
	}
	for i := 0; i < ResetEmails; i++ {
		f.post("/auth/forgot-password", `{"email":"ann@example.com"}`, "")
	}
	if *f.mails > ResetEmails {
		t.Fatalf("reset mails are not throttled: %d", *f.mails)
	}
}

func TestTwoFactorDisableNeedsConfirmation(t *testing.T) {
	f := newGuardFixture(t)
	token, _ := NewJWTManager("guard-test-secret-32-characters!!", time.Hour).GenerateToken("u1")
	if rec := f.post("/auth/two-factor/disable", `{}`, token); rec.Code != http.StatusUnprocessableEntity || !f.user.TwoFactorEnabled {
		t.Fatalf("disable without password: %d enabled=%v", rec.Code, f.user.TwoFactorEnabled)
	}
	if rec := f.post("/auth/two-factor/disable", `{"password":"correct-horse"}`, token); rec.Code != http.StatusOK || f.user.TwoFactorEnabled {
		t.Fatalf("disable with password: %d %s", rec.Code, rec.Body.String())
	}
}

type countingChannel struct{ n *int }

func (c countingChannel) Send(context.Context, notification.Notifiable, notification.Notification) error {
	*c.n++
	return nil
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// Regression: the 2FA challenge token (issued after the password only)
// was accepted by BearerGuard as a full access token.
func TestChallengeTokenIsNotAnAccessToken(t *testing.T) {
	f := newGuardFixture(t)
	tok := challenge(t, f)
	ratelimit.SetStore(ratelimit.NewMemoryStore())
	rec := f.post("/auth/two-factor/disable", `{"password":"correct-horse"}`, tok)
	if rec.Code != http.StatusUnauthorized || !f.user.TwoFactorEnabled {
		t.Fatalf("challenge token used as Bearer: %d enabled=%v", rec.Code, f.user.TwoFactorEnabled)
	}
	if _, _, err := AuthenticateCookieValueForTest(tok); err == nil {
		t.Fatal("challenge token accepted by the cookie guard path")
	}
}

func AuthenticateCookieValueForTest(tok string) (Authenticatable, *JWTClaims, error) {
	m := NewJWTManager("guard-test-secret-32-characters!!", time.Hour)
	claims, err := m.VerifyToken(tok)
	if err != nil {
		return nil, nil, err
	}
	if isChallengeToken(claims) {
		return nil, nil, ErrTokenInvalid
	}
	return &SimpleUser{ID: claims.Sub}, claims, nil
}
