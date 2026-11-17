package auth

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

func TestTOTP_GenerateSecret(t *testing.T) {
	m := NewTOTPManager()
	secret, err := m.GenerateSecret()
	if err != nil {
		t.Fatalf("failed generating secret: %v", err)
	}

	if len(secret) < 16 {
		t.Fatalf("secret too short: %s", secret)
	}

	// Secret must be valid Base32
	_, err = decodeBase32Secret(secret)
	if err != nil {
		t.Fatalf("secret is not valid Base32: %v", err)
	}
}

func TestTOTP_GenerateAndVerify(t *testing.T) {
	m := NewTOTPManager()
	secret, _ := m.GenerateSecret()

	now := time.Now()
	code, err := m.GenerateCode(secret, now)
	if err != nil {
		t.Fatalf("failed generating code: %v", err)
	}

	if len(code) != 6 {
		t.Fatalf("expected 6-digit code, got %s", code)
	}

	// Immediate verification must succeed
	if !m.VerifyCode(secret, code, now) {
		t.Fatalf("verification failed for valid code %s", code)
	}

	// Test clock skew within window (default 1 step = 30s)
	// 20s later (same or next window)
	future := now.Add(20 * time.Second)
	if !m.VerifyCode(secret, code, future) {
		t.Fatalf("expected code to be valid within skew window")
	}

	// Past skew window (e.g. 100s later) must fail
	tooLate := now.Add(100 * time.Second)
	if m.VerifyCode(secret, code, tooLate) {
		t.Fatalf("expected code to fail after skew window exceeded")
	}

	// Invalid code must fail
	if m.VerifyCode(secret, "000000", now) && code != "000000" {
		t.Fatalf("expected invalid code to fail")
	}
}

func TestTOTP_RFC6238_TestVectors(t *testing.T) {
	// RFC 6238 Appendix B test vector for SHA1:
	// Secret: "12345678901234567890" (ASCII, 20 bytes)
	rawSecret := []byte("12345678901234567890")
	secretB32 := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(rawSecret)

	m := NewTOTPManager(TOTPOptions{
		Digits:     8,
		Period:     30 * time.Second,
		SkewWindow: 0,
	})

	// Time = 59s -> Counter = 1 -> RFC expects "94287082"
	t1 := time.Unix(59, 0)
	c1, err := m.GenerateCode(secretB32, t1)
	if err != nil {
		t.Fatal(err)
	}
	if c1 != "94287082" {
		t.Errorf("RFC 6238 test vector failed at 59s: expected 94287082, got %s", c1)
	}

	// Time = 1111111109s -> Counter = 37037036 -> RFC expects "07081804"
	t2 := time.Unix(1111111109, 0)
	c2, err := m.GenerateCode(secretB32, t2)
	if err != nil {
		t.Fatal(err)
	}
	if c2 != "07081804" {
		t.Errorf("RFC 6238 test vector failed at 1111111109s: expected 07081804, got %s", c2)
	}
}

func TestTOTP_ProvisioningURI(t *testing.T) {
	m := NewTOTPManager(TOTPOptions{Issuer: "Acme Corp"})
	uri := m.ProvisioningURI("alice@example.com", "JBSWY3DPEHPK3PXP")

	if !strings.HasPrefix(uri, "otpauth://totp/Acme%20Corp:alice@example.com?") {
		t.Errorf("unexpected prefix: %s", uri)
	}
	if !strings.Contains(uri, "secret=JBSWY3DPEHPK3PXP") {
		t.Errorf("missing secret in uri: %s", uri)
	}
	if !strings.Contains(uri, "issuer=Acme+Corp") && !strings.Contains(uri, "issuer=Acme%20Corp") {
		t.Errorf("missing issuer in uri: %s", uri)
	}
}

func TestTOTP_RecoveryCodes(t *testing.T) {
	m := NewTOTPManager()
	plain, hashed, err := m.GenerateRecoveryCodes(8)
	if err != nil {
		t.Fatalf("failed generating recovery codes: %v", err)
	}

	if len(plain) != 8 || len(hashed) != 8 {
		t.Fatalf("expected 8 recovery codes, got %d", len(plain))
	}

	// Verify first code
	codeToUse := plain[0]
	ok, remaining := m.VerifyRecoveryCode(codeToUse, hashed)
	if !ok {
		t.Fatalf("expected valid recovery code verification")
	}
	if len(remaining) != 7 {
		t.Fatalf("expected 7 remaining recovery codes, got %d", len(remaining))
	}

	// Single-use: using the same code again on remaining must fail
	ok2, _ := m.VerifyRecoveryCode(codeToUse, remaining)
	if ok2 {
		t.Fatalf("recovery code must be single-use and cannot be used twice")
	}

	// Invalid code must fail
	ok3, _ := m.VerifyRecoveryCode("invalid-code", remaining)
	if ok3 {
		t.Fatalf("invalid recovery code succeeded")
	}
}

func TestTOTP_SkewDefaultWhenOptionsOmitIt(t *testing.T) {
	now := time.Unix(1_800_000_015, 0)
	for _, tc := range []struct {
		name string
		opts TOTPOptions
		skew bool
	}{
		{"issuer only", TOTPOptions{Issuer: "Acme"}, true},
		{"explicit 2", TOTPOptions{SkewWindow: 2}, true},
		{"NoSkew", TOTPOptions{SkewWindow: NoSkew}, false},
	} {
		m := NewTOTPManager(tc.opts)
		secret, _ := m.GenerateSecret()
		prev, _ := m.GenerateCode(secret, now.Add(-30*time.Second))
		next, _ := m.GenerateCode(secret, now.Add(30*time.Second))
		cur, _ := m.GenerateCode(secret, now)
		if !m.VerifyCode(secret, cur, now) {
			t.Errorf("%s: current code refused", tc.name)
		}
		if m.VerifyCode(secret, prev, now) != tc.skew || m.VerifyCode(secret, next, now) != tc.skew {
			t.Errorf("%s: neighbouring steps accepted=%v, want %v", tc.name, !tc.skew, tc.skew)
		}
	}
}
