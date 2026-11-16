package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golaravel/golaravel/pkg/cache"
	"github.com/golaravel/golaravel/pkg/mail"
	"github.com/golaravel/golaravel/pkg/notification"
)

var (
	ErrPasswordResetTokenInvalid = errors.New("auth/reset: password reset token is invalid or expired")
	ErrEmailVerificationInvalid  = errors.New("auth/verify: email verification link is invalid or expired")
	ErrTwoFactorTokenExpired     = errors.New("auth/2fa: two-factor challenge token has expired or is invalid")
)

// ResetTokenStore keeps password reset token hashes. The default memory
// store loses pending resets on restart and is per process; use
// CacheResetTokenStore (with a SQL or Redis cache store) or your own store
// when running more than one instance.
type ResetTokenStore interface {
	Put(email, tokenHash string, expiresAt time.Time) error
	Get(email string) (tokenHash string, expiresAt time.Time, ok bool, err error)
	Delete(email string) error
}

// PasswordResetBroker handles generation and validation of password reset tokens.
// Only a SHA-256 hash of each token is stored.
type PasswordResetBroker struct {
	store ResetTokenStore
	ttl   time.Duration
}

// NewPasswordResetBroker creates a new password reset broker with a default
// 60-minute TTL, keeping tokens in memory.
func NewPasswordResetBroker(ttl ...time.Duration) *PasswordResetBroker {
	return NewPasswordResetBrokerWithStore(NewMemoryResetTokenStore(), ttl...)
}

// NewPasswordResetBrokerWithStore creates a broker that keeps tokens in store.
func NewPasswordResetBrokerWithStore(store ResetTokenStore, ttl ...time.Duration) *PasswordResetBroker {
	t := 60 * time.Minute
	if len(ttl) > 0 && ttl[0] > 0 {
		t = ttl[0]
	}
	if store == nil {
		store = NewMemoryResetTokenStore()
	}
	return &PasswordResetBroker{store: store, ttl: t}
}

func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// CreateToken generates a secure random token for the specified email and returns the plain token.
func (b *PasswordResetBroker) CreateToken(email string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	if err := b.store.Put(normalizeEmail(email), hashToken(token), time.Now().Add(b.ttl)); err != nil {
		return "", err
	}
	return token, nil
}

// ValidateToken verifies a token for the given email without consuming it.
func (b *PasswordResetBroker) ValidateToken(email, token string) bool {
	stored, expiresAt, ok, err := b.store.Get(normalizeEmail(email))
	if err != nil || !ok || time.Now().After(expiresAt) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(stored), []byte(hashToken(token))) == 1
}

// DeleteToken removes the token for an email upon successful password reset.
func (b *PasswordResetBroker) DeleteToken(email string) {
	_ = b.store.Delete(normalizeEmail(email))
}

// --- Memory store ---

type passwordResetEntry struct {
	tokenHash string
	expiresAt time.Time
}

type memoryResetTokenStore struct {
	mu     sync.RWMutex
	tokens map[string]passwordResetEntry
}

// NewMemoryResetTokenStore keeps reset tokens in process memory.
func NewMemoryResetTokenStore() ResetTokenStore {
	return &memoryResetTokenStore{tokens: make(map[string]passwordResetEntry)}
}

func (s *memoryResetTokenStore) Put(email, tokenHash string, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, e := range s.tokens {
		if now.After(e.expiresAt) {
			delete(s.tokens, k)
		}
	}
	s.tokens[email] = passwordResetEntry{tokenHash: tokenHash, expiresAt: expiresAt}
	return nil
}

func (s *memoryResetTokenStore) Get(email string) (string, time.Time, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.tokens[email]
	return e.tokenHash, e.expiresAt, ok, nil
}

func (s *memoryResetTokenStore) Delete(email string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, email)
	return nil
}

// --- Cache-backed store ---

type cacheResetTokenStore struct {
	c      cache.Store
	prefix string
}

// CacheResetTokenStore keeps reset tokens in a cache store, for example
// cache.NewSQLStore or cache.NewRedisStore, so pending resets survive
// restarts and work on every instance. Keys hold a hash of the email, not
// the email itself.
func CacheResetTokenStore(c cache.Store, prefix ...string) ResetTokenStore {
	p := "password_reset:"
	if len(prefix) > 0 && prefix[0] != "" {
		p = prefix[0]
	}
	return &cacheResetTokenStore{c: c, prefix: p}
}

func (s *cacheResetTokenStore) key(email string) string { return s.prefix + hashToken(email) }

func (s *cacheResetTokenStore) Put(email, tokenHash string, expiresAt time.Time) error {
	ttl := time.Until(expiresAt)
	if ttl <= 0 {
		return s.Delete(email)
	}
	return s.c.Put(s.key(email), map[string]any{"hash": tokenHash, "exp": expiresAt.Unix()}, ttl)
}

func (s *cacheResetTokenStore) Get(email string) (string, time.Time, bool, error) {
	v, ok := s.c.Get(s.key(email))
	if !ok {
		return "", time.Time{}, false, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return "", time.Time{}, false, nil
	}
	h, _ := m["hash"].(string)
	var exp int64
	switch x := m["exp"].(type) {
	case int64:
		exp = x
	case float64:
		exp = int64(x)
	case int:
		exp = int64(x)
	}
	if h == "" || exp == 0 {
		return "", time.Time{}, false, nil
	}
	return h, time.Unix(exp, 0), true, nil
}

func (s *cacheResetTokenStore) Delete(email string) error { return s.c.Forget(s.key(email)) }

// EmailVerificationBroker generates and validates HMAC-signed verification URLs.
type EmailVerificationBroker struct {
	secret []byte
}

// NewEmailVerificationBroker creates a broker with a signing secret key.
func NewEmailVerificationBroker(secret string) *EmailVerificationBroker {
	return &EmailVerificationBroker{secret: []byte(secret)}
}

// GenerateSignedURL creates a tamper-proof verification URL with expiration.
func (b *EmailVerificationBroker) GenerateSignedURL(baseURL, id, email string, ttl time.Duration) string {
	expires := time.Now().Add(ttl).Unix()
	hash := sha256.Sum256([]byte(email))
	hashHex := hex.EncodeToString(hash[:])

	sig := b.computeSignature(id, hashHex, expires)

	u, _ := url.Parse(baseURL)
	q := u.Query()
	q.Set("id", id)
	q.Set("hash", hashHex)
	q.Set("expires", strconv.FormatInt(expires, 10))
	q.Set("signature", sig)
	u.RawQuery = q.Encode()

	return u.String()
}

// VerifySignature validates a signed email verification URL.
func (b *EmailVerificationBroker) VerifySignature(id, email string, expires int64, signature string) bool {
	if time.Now().Unix() > expires {
		return false
	}

	hash := sha256.Sum256([]byte(email))
	hashHex := hex.EncodeToString(hash[:])

	expectedSig := b.computeSignature(id, hashHex, expires)
	return subtle.ConstantTimeCompare([]byte(expectedSig), []byte(signature)) == 1
}

func (b *EmailVerificationBroker) computeSignature(id, hashHex string, expires int64) string {
	payload := fmt.Sprintf("%s|%s|%d", id, hashHex, expires)
	mac := hmac.New(sha256.New, b.secret)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// TwoFactorChallengeBroker manages short-lived signed tokens for users awaiting 2FA completion.
type TwoFactorChallengeBroker struct {
	jwt *JWTManager
}

// NewTwoFactorChallengeBroker creates a broker with 5-minute challenge token TTL.
func NewTwoFactorChallengeBroker(secret string) *TwoFactorChallengeBroker {
	// Challenge tokens are proof of the password only. They are signed with
	// a key derived from the secret, so a manager verifying access tokens
	// with the plain secret can never accept one as a login.
	key := ""
	if secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte("golaravel:2fa-challenge"))
		key = hex.EncodeToString(mac.Sum(nil))
	}
	return &TwoFactorChallengeBroker{
		jwt: NewJWTManager(key, 5*time.Minute).Audience(challengeAudience),
	}
}

const challengeAudience = "golaravel:2fa-challenge"

// GenerateChallengeToken creates a temporary token containing the user ID and 2fa challenge action.
func (b *TwoFactorChallengeBroker) GenerateChallengeToken(userID string) (string, error) {
	return b.jwt.GenerateToken(userID, map[string]any{"action": "2fa_challenge"})
}

// VerifyChallengeToken validates a challenge token and returns the user ID.
func (b *TwoFactorChallengeBroker) VerifyChallengeToken(tokenString string) (string, error) {
	claims, err := b.jwt.VerifyToken(tokenString)
	if err != nil {
		return "", ErrTwoFactorTokenExpired
	}
	if claims.Custom["action"] != "2fa_challenge" {
		return "", ErrTwoFactorTokenExpired
	}
	return claims.Sub, nil
}

// ResetPasswordNotification delivers a password reset link to the recipient.
type ResetPasswordNotification struct {
	ResetURL string
	AppName  string
}

func NewResetPasswordNotification(resetURL string, appName ...string) *ResetPasswordNotification {
	name := "GoLaravel"
	if len(appName) > 0 && appName[0] != "" {
		name = appName[0]
	}
	return &ResetPasswordNotification{ResetURL: resetURL, AppName: name}
}

func (n *ResetPasswordNotification) Via(notifiable notification.Notifiable) []string {
	return []string{notification.ChannelMail}
}

func (n *ResetPasswordNotification) ToMail(notifiable notification.Notifiable) (*mail.Message, error) {
	msg := mail.NewMessage().
		Subject(fmt.Sprintf("%s: Reset Your Password", n.AppName)).
		Text(fmt.Sprintf("Hello,\n\nYou recently requested to reset your password for %s. Click the link below to proceed:\n\n%s\n\nIf you did not request a password reset, please ignore this email.\n", n.AppName, n.ResetURL))
	return msg, nil
}

// VerifyEmailNotification delivers an email verification link.
type VerifyEmailNotification struct {
	VerifyURL string
	AppName   string
}

func NewVerifyEmailNotification(verifyURL string, appName ...string) *VerifyEmailNotification {
	name := "GoLaravel"
	if len(appName) > 0 && appName[0] != "" {
		name = appName[0]
	}
	return &VerifyEmailNotification{VerifyURL: verifyURL, AppName: name}
}

func (n *VerifyEmailNotification) Via(notifiable notification.Notifiable) []string {
	return []string{notification.ChannelMail}
}

func (n *VerifyEmailNotification) ToMail(notifiable notification.Notifiable) (*mail.Message, error) {
	msg := mail.NewMessage().
		Subject(fmt.Sprintf("%s: Verify Your Email Address", n.AppName)).
		Text(fmt.Sprintf("Hello,\n\nPlease verify your email address by clicking the link below:\n\n%s\n\nThank you for joining %s!\n", n.VerifyURL, n.AppName))
	return msg, nil
}
