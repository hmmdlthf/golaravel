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
	"sync"
	"time"

	"github.com/golaravel/golaravel/pkg/mail"
	"github.com/golaravel/golaravel/pkg/notification"
)

var (
	ErrPasswordResetTokenInvalid = errors.New("auth/reset: password reset token is invalid or expired")
	ErrEmailVerificationInvalid  = errors.New("auth/verify: email verification link is invalid or expired")
	ErrTwoFactorTokenExpired     = errors.New("auth/2fa: two-factor challenge token has expired or is invalid")
)

// PasswordResetBroker handles generation and validation of password reset tokens.
type PasswordResetBroker struct {
	mu     sync.RWMutex
	tokens map[string]passwordResetEntry
	ttl    time.Duration
}

type passwordResetEntry struct {
	tokenHash string
	expiresAt time.Time
}

// NewPasswordResetBroker creates a new password reset broker with a default 60-minute TTL.
func NewPasswordResetBroker(ttl ...time.Duration) *PasswordResetBroker {
	t := 60 * time.Minute
	if len(ttl) > 0 && ttl[0] > 0 {
		t = ttl[0]
	}
	return &PasswordResetBroker{
		tokens: make(map[string]passwordResetEntry),
		ttl:    t,
	}
}

// CreateToken generates a secure random token for the specified email and returns the plain token.
func (b *PasswordResetBroker) CreateToken(email string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)

	h := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(h[:])

	b.tokens[email] = passwordResetEntry{
		tokenHash: tokenHash,
		expiresAt: time.Now().Add(b.ttl),
	}

	return token, nil
}

// ValidateToken verifies a token for the given email without consuming it.
func (b *PasswordResetBroker) ValidateToken(email, token string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()

	entry, exists := b.tokens[email]
	if !exists {
		return false
	}

	if time.Now().After(entry.expiresAt) {
		return false
	}

	h := sha256.Sum256([]byte(token))
	targetHash := hex.EncodeToString(h[:])

	return subtle.ConstantTimeCompare([]byte(entry.tokenHash), []byte(targetHash)) == 1
}

// DeleteToken removes the token for an email upon successful password reset.
func (b *PasswordResetBroker) DeleteToken(email string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.tokens, email)
}

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
	return &TwoFactorChallengeBroker{
		jwt: NewJWTManager(secret, 5*time.Minute),
	}
}

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
