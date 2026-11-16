package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"
)

var (
	ErrInvalidSecret       = errors.New("auth/totp: invalid base32 secret")
	ErrInvalidCode         = errors.New("auth/totp: invalid 2fa verification code")
	ErrRecoveryCodeInvalid = errors.New("auth/totp: invalid or already used recovery code")
)

// TOTPOptions configures time-based one-time password generation and verification.
type TOTPOptions struct {
	Issuer     string        // Application name (e.g. "GoLaravel")
	Digits     int           // Length of TOTP code, typically 6
	Period     time.Duration // Time-step duration, typically 30s
	SkewWindow int           // Clock drift tolerance steps (1 allows -30s to +30s)
}

// DefaultTOTPOptions provides standard RFC 6238 TOTP configuration.
var DefaultTOTPOptions = TOTPOptions{
	Issuer:     "GoLaravel",
	Digits:     6,
	Period:     30 * time.Second,
	SkewWindow: 1,
}

// TOTPManager manages TOTP 2FA secret generation, code verification, and emergency recovery codes.
type TOTPManager struct {
	opts TOTPOptions
}

// NewTOTPManager creates a new TOTP manager with the provided options.
func NewTOTPManager(opts ...TOTPOptions) *TOTPManager {
	o := DefaultTOTPOptions
	if len(opts) > 0 {
		if opts[0].Issuer != "" {
			o.Issuer = opts[0].Issuer
		}
		if opts[0].Digits > 0 {
			o.Digits = opts[0].Digits
		}
		if opts[0].Period > 0 {
			o.Period = opts[0].Period
		}
		if opts[0].SkewWindow >= 0 {
			o.SkewWindow = opts[0].SkewWindow
		}
	}
	return &TOTPManager{opts: o}
}

// GenerateSecret creates a cryptographically secure Base32-encoded secret key (160-bit standard).
func (m *TOTPManager) GenerateSecret(numBytes ...int) (string, error) {
	size := 20 // 160 bits standard for SHA1
	if len(numBytes) > 0 && numBytes[0] > 0 {
		size = numBytes[0]
	}
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw), nil
}

// GenerateCode computes the TOTP code for a secret at a given timestamp.
func (m *TOTPManager) GenerateCode(secret string, at ...time.Time) (string, error) {
	t := time.Now()
	if len(at) > 0 {
		t = at[0]
	}

	key, err := decodeBase32Secret(secret)
	if err != nil {
		return "", err
	}

	counter := uint64(t.Unix() / int64(m.opts.Period.Seconds()))
	return computeTOTP(key, counter, m.opts.Digits), nil
}

// VerifyCode verifies a TOTP code against the secret, taking into account clock skew.
// VerifyCodeStep is VerifyCode that also returns the matched time step,
// so callers can refuse a second use of the same code.
func (m *TOTPManager) VerifyCodeStep(secret, code string, at ...time.Time) (bool, int64) {
	cleanCode := strings.TrimSpace(code)
	if len(cleanCode) != m.opts.Digits {
		return false, 0
	}
	key, err := decodeBase32Secret(secret)
	if err != nil {
		return false, 0
	}
	t := time.Now()
	if len(at) > 0 {
		t = at[0]
	}
	counter := int64(t.Unix() / int64(m.opts.Period.Seconds()))
	skew := int64(m.opts.SkewWindow)
	for i := -skew; i <= skew; i++ {
		candidate := computeTOTP(key, uint64(counter+i), m.opts.Digits)
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(cleanCode)) == 1 {
			return true, counter + i
		}
	}
	return false, 0
}

func (m *TOTPManager) VerifyCode(secret, code string, at ...time.Time) bool {
	cleanCode := strings.TrimSpace(code)
	if len(cleanCode) != m.opts.Digits {
		return false
	}

	key, err := decodeBase32Secret(secret)
	if err != nil {
		return false
	}

	t := time.Now()
	if len(at) > 0 {
		t = at[0]
	}

	counter := int64(t.Unix() / int64(m.opts.Period.Seconds()))
	skew := int64(m.opts.SkewWindow)

	// Check window: counter - skew to counter + skew
	for i := -skew; i <= skew; i++ {
		candidate := computeTOTP(key, uint64(counter+i), m.opts.Digits)
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(cleanCode)) == 1 {
			return true
		}
	}
	return false
}

// ProvisioningURI returns the standard otpauth:// URL for QR code generation.
func (m *TOTPManager) ProvisioningURI(accountName, secret string) string {
	cleanSecret := strings.ToUpper(strings.ReplaceAll(secret, " ", ""))
	label := url.PathEscape(m.opts.Issuer + ":" + accountName)

	v := url.Values{}
	v.Set("secret", cleanSecret)
	v.Set("issuer", m.opts.Issuer)
	v.Set("algorithm", "SHA1")
	v.Set("digits", fmt.Sprintf("%d", m.opts.Digits))
	v.Set("period", fmt.Sprintf("%d", int(m.opts.Period.Seconds())))

	return fmt.Sprintf("otpauth://totp/%s?%s", label, v.Encode())
}

// GenerateRecoveryCodes produces a set of emergency one-time recovery codes.
// Returns plain codes (to show to user once) and hashed codes (to persist in database).
func (m *TOTPManager) GenerateRecoveryCodes(count ...int) (plain []string, hashed []string, err error) {
	num := 8
	if len(count) > 0 && count[0] > 0 {
		num = count[0]
	}

	plain = make([]string, num)
	hashed = make([]string, num)

	for i := 0; i < num; i++ {
		b := make([]byte, 5) // 10 hex characters
		if _, err := rand.Read(b); err != nil {
			return nil, nil, err
		}
		rawHex := hex.EncodeToString(b)
		// Format: xxxxx-xxxxx (e.g. "a1b2c-3d4e5")
		formatted := fmt.Sprintf("%s-%s", rawHex[:5], rawHex[5:])
		plain[i] = formatted
		hashed[i] = hashRecoveryCode(formatted)
	}

	return plain, hashed, nil
}

// VerifyRecoveryCode checks a recovery code against stored hashes.
// If valid, returns true and the remaining hashed codes with the consumed code removed.
func (m *TOTPManager) VerifyRecoveryCode(plainCode string, hashedCodes []string) (bool, []string) {
	targetHash := hashRecoveryCode(plainCode)
	for i, h := range hashedCodes {
		if subtle.ConstantTimeCompare([]byte(h), []byte(targetHash)) == 1 {
			// Remove the used code (single-use guarantee)
			remaining := append(hashedCodes[:i], hashedCodes[i+1:]...)
			return true, remaining
		}
	}
	return false, hashedCodes
}

func hashRecoveryCode(code string) string {
	clean := strings.ToLower(strings.TrimSpace(code))
	h := sha256.Sum256([]byte(clean))
	return hex.EncodeToString(h[:])
}

func decodeBase32Secret(secret string) ([]byte, error) {
	clean := strings.ToUpper(strings.ReplaceAll(secret, " ", ""))
	// Try unpadded first
	data, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(clean)
	if err == nil {
		return data, nil
	}
	// Try standard padded
	data, err = base32.StdEncoding.DecodeString(clean)
	if err != nil {
		return nil, ErrInvalidSecret
	}
	return data, nil
}

func computeTOTP(key []byte, counter uint64, digits int) string {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(buf)
	hash := mac.Sum(nil)

	// Dynamic truncation (RFC 4226)
	offset := hash[len(hash)-1] & 0x0f
	binaryCode := (int(hash[offset]&0x7f) << 24) |
		(int(hash[offset+1]&0xff) << 16) |
		(int(hash[offset+2]&0xff) << 8) |
		int(hash[offset+3]&0xff)

	mod := int(math.Pow10(digits))
	otp := binaryCode % mod

	format := fmt.Sprintf("%%0%dd", digits)
	return fmt.Sprintf(format, otp)
}
