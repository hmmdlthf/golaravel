package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/goccy/go-json"
)

var (
	ErrTokenExpired = errors.New("auth: token has expired")
	ErrTokenInvalid = errors.New("auth: token signature or format is invalid")
	// ErrEmptySecret is returned when the manager has no signing secret:
	// an empty HMAC key would let anyone mint valid tokens.
	ErrEmptySecret = errors.New("auth: JWT secret is empty")
	// ErrTokenRevoked is returned when the RevokedUsing check rejects a token.
	ErrTokenRevoked = errors.New("auth: token has been revoked")
)

// JWTHeader represents the header of a JWT.
type JWTHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// JWTClaims represents standard registered claims.
type JWTClaims struct {
	Sub    string         `json:"sub"`              // Subject (User ID)
	Exp    int64          `json:"exp"`              // Expiration time
	Iat    int64          `json:"iat"`              // Issued at
	Nbf    int64          `json:"nbf,omitempty"`    // Not before
	Iss    string         `json:"iss,omitempty"`    // Issuer
	Aud    string         `json:"aud,omitempty"`    // Audience
	Jti    string         `json:"jti,omitempty"`    // Unique token id (for revocation lists)
	Custom map[string]any `json:"custom,omitempty"` // Custom claims
}

// JWTManager handles signing and validating JWT bearer tokens.
type JWTManager struct {
	secret   []byte
	previous [][]byte
	ttl      time.Duration
	issuer   string
	audience string
	leeway   time.Duration
	revoked  func(claims *JWTClaims) bool
}

// Issuer stamps iss on issued tokens and requires it on verified ones.
func (m *JWTManager) Issuer(iss string) *JWTManager {
	m.issuer = iss
	return m
}

// Audience stamps aud on issued tokens and requires it on verified ones, so
// a token minted for one service is not accepted by another sharing the key.
func (m *JWTManager) Audience(aud string) *JWTManager {
	m.audience = aud
	return m
}

// Leeway tolerates clock skew when checking exp and nbf.
func (m *JWTManager) Leeway(d time.Duration) *JWTManager {
	m.leeway = d
	return m
}

// RotateFrom keeps accepting tokens signed with older secrets while new
// tokens are signed with the current one. Drop the old secrets once their
// tokens have expired.
func (m *JWTManager) RotateFrom(oldSecrets ...string) *JWTManager {
	for _, s := range oldSecrets {
		if s != "" {
			m.previous = append(m.previous, []byte(s))
		}
	}
	return m
}

// RevokedUsing registers a check run on every verified token; returning true
// rejects the token with ErrTokenRevoked (for example after logout, a
// password change, or when the user's sessions are revoked).
func (m *JWTManager) RevokedUsing(fn func(claims *JWTClaims) bool) *JWTManager {
	m.revoked = fn
	return m
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
	if len(m.secret) == 0 {
		return "", ErrEmptySecret
	}
	header := JWTHeader{Alg: "HS256", Typ: "JWT"}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}

	now := time.Now()
	jti := make([]byte, 16)
	if _, err := rand.Read(jti); err != nil {
		return "", err
	}
	claims := JWTClaims{
		Sub: sub,
		Iat: now.Unix(),
		Nbf: now.Unix(),
		Exp: now.Add(m.ttl).Unix(),
		Iss: m.issuer,
		Aud: m.audience,
		Jti: hex.EncodeToString(jti),
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
	if len(m.secret) == 0 {
		return nil, ErrEmptySecret
	}
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, ErrTokenInvalid
	}
	// Only HS256 is ever issued; refuse anything else (e.g. "none").
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrTokenInvalid
	}
	var header JWTHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil || header.Alg != "HS256" {
		return nil, ErrTokenInvalid
	}

	message := parts[0] + "." + parts[1]
	actualSig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, ErrTokenInvalid
	}
	valid := hmac.Equal(signWith(m.secret, message), actualSig)
	for _, old := range m.previous {
		if valid {
			break
		}
		valid = hmac.Equal(signWith(old, message), actualSig)
	}
	if !valid {
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

	now := time.Now()
	leeway := int64(m.leeway / time.Second)
	if now.Unix() > claims.Exp+leeway {
		return nil, ErrTokenExpired
	}
	if claims.Nbf != 0 && now.Unix()+leeway < claims.Nbf {
		return nil, ErrTokenInvalid
	}
	if m.issuer != "" && claims.Iss != m.issuer {
		return nil, ErrTokenInvalid
	}
	if m.audience != "" && claims.Aud != m.audience {
		return nil, ErrTokenInvalid
	}
	if m.revoked != nil && m.revoked(&claims) {
		return nil, ErrTokenRevoked
	}

	return &claims, nil
}

func (m *JWTManager) sign(message string) []byte {
	return signWith(m.secret, message)
}

func signWith(secret []byte, message string) []byte {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(message))
	return h.Sum(nil)
}
