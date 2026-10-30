package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/goccy/go-json"
)

var (
	ErrTokenExpired = errors.New("auth: token has expired")
	ErrTokenInvalid = errors.New("auth: token signature or format is invalid")
)

// JWTHeader represents the header of a JWT.
type JWTHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// JWTClaims represents standard registered claims.
type JWTClaims struct {
	Sub       string         `json:"sub"`                 // Subject (User ID)
	Exp       int64          `json:"exp"`                 // Expiration time
	Iat       int64          `json:"iat"`                 // Issued at
	Custom    map[string]any `json:"custom,omitempty"`    // Custom claims
}

// JWTManager handles signing and validating JWT bearer tokens.
type JWTManager struct {
	secret []byte
	ttl    time.Duration
}

// NewJWTManager creates a new JWT manager with the given secret and token time-to-live.
func NewJWTManager(secret string, ttl time.Duration) *JWTManager {
	return &JWTManager{
		secret: []byte(secret),
		ttl:    ttl,
	}
}

// GenerateToken creates a signed JWT string for the given subject (user ID) and optional custom claims.
func (m *JWTManager) GenerateToken(sub string, custom ...map[string]any) (string, error) {
	header := JWTHeader{Alg: "HS256", Typ: "JWT"}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}

	now := time.Now()
	claims := JWTClaims{
		Sub: sub,
		Iat: now.Unix(),
		Exp: now.Add(m.ttl).Unix(),
	}
	if len(custom) > 0 {
		claims.Custom = custom[0]
	}

	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}

	part1 := base64.RawURLEncoding.EncodeToString(headerJSON)
	part2 := base64.RawURLEncoding.EncodeToString(claimsJSON)
	message := part1 + "." + part2

	sig := m.sign(message)
	part3 := base64.RawURLEncoding.EncodeToString(sig)

	return message + "." + part3, nil
}

// VerifyToken validates a JWT and returns the parsed claims.
func (m *JWTManager) VerifyToken(tokenString string) (*JWTClaims, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, ErrTokenInvalid
	}

	message := parts[0] + "." + parts[1]
	expectedSig := m.sign(message)
	actualSig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(expectedSig, actualSig) {
		return nil, ErrTokenInvalid
	}

	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrTokenInvalid
	}

	var claims JWTClaims
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		return nil, ErrTokenInvalid
	}

	if time.Now().Unix() > claims.Exp {
		return nil, ErrTokenExpired
	}

	return &claims, nil
}

func (m *JWTManager) sign(message string) []byte {
	h := hmac.New(sha256.New, m.secret)
	h.Write([]byte(message))
	return h.Sum(nil)
}
