package auth

import (
	"encoding/base64"
	"errors"
	"strconv"
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

func TestJWTIssuerAudienceAndNotBefore(t *testing.T) {
	a := NewJWTManager("shared-secret-shared", time.Hour).Issuer("auth.example").Audience("billing")
	b := NewJWTManager("shared-secret-shared", time.Hour).Issuer("auth.example").Audience("panel")
	tok, _ := a.GenerateToken("1")
	if _, err := a.VerifyToken(tok); err != nil {
		t.Fatalf("own token: %v", err)
	}
	if _, err := b.VerifyToken(tok); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("token for another audience accepted: %v", err)
	}
	claims, _ := a.VerifyToken(tok)
	if claims.Jti == "" || claims.Nbf == 0 || claims.Iss != "auth.example" {
		t.Fatalf("claims not stamped: %+v", claims)
	}

	// A token that is not valid yet.
	future := `{"sub":"1","exp":` + itoa(time.Now().Add(2*time.Hour).Unix()) + `,"nbf":` + itoa(time.Now().Add(time.Hour).Unix()) + `}`
	m := NewJWTManager("shared-secret-shared", time.Hour)
	if _, err := m.VerifyToken(signRaw(m, future)); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("nbf in the future accepted: %v", err)
	}
}

func TestJWTKeyRotation(t *testing.T) {
	old := NewJWTManager("old-secret-old-secret", time.Hour)
	tok, _ := old.GenerateToken("1")
	rotated := NewJWTManager("new-secret-new-secret", time.Hour).RotateFrom("old-secret-old-secret")
	if _, err := rotated.VerifyToken(tok); err != nil {
		t.Fatalf("token signed with the previous secret: %v", err)
	}
	if _, err := NewJWTManager("new-secret-new-secret", time.Hour).VerifyToken(tok); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("old token accepted without RotateFrom: %v", err)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func signRaw(m *JWTManager, claims string) string {
	h := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	c := base64.RawURLEncoding.EncodeToString([]byte(claims))
	return h + "." + c + "." + base64.RawURLEncoding.EncodeToString(m.sign(h+"."+c))
}
