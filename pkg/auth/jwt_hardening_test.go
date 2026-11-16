package auth

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestJWTEmptySecretIsRefused(t *testing.T) {
	m := NewJWTManager("", time.Hour)
	if _, err := m.GenerateToken("1"); !errors.Is(err, ErrEmptySecret) {
		t.Fatalf("generate with empty secret: %v", err)
	}
	if _, err := m.VerifyToken("a.b.c"); !errors.Is(err, ErrEmptySecret) {
		t.Fatalf("verify with empty secret: %v", err)
	}
}

func TestJWTRejectsOtherAlgorithms(t *testing.T) {
	m := NewJWTManager("secret-secret-secret", time.Hour)
	tok, _ := m.GenerateToken("1")
	parts := strings.Split(tok, ".")
	parts[0] = base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	// Re-sign so only the header differs.
	forged := parts[0] + "." + parts[1]
	forged += "." + base64.RawURLEncoding.EncodeToString(m.sign(forged))
	if _, err := m.VerifyToken(forged); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("alg none accepted: %v", err)
	}
}

func TestJWTRevocationHook(t *testing.T) {
	revoked := false
	m := NewJWTManager("secret-secret-secret", time.Hour).RevokedUsing(func(c *JWTClaims) bool { return revoked && c.Sub == "1" })
	tok, _ := m.GenerateToken("1")
	if _, err := m.VerifyToken(tok); err != nil {
		t.Fatalf("valid token: %v", err)
	}
	revoked = true
	if _, err := m.VerifyToken(tok); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("revoked token: %v", err)
	}
}
