package hash

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

// Default bcrypt cost
const DefaultBcryptCost = 10

// Argon2id parameters
type Argon2Params struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

// DefaultArgon2Params provides standard secure parameters for Argon2id.
var DefaultArgon2Params = Argon2Params{
	Memory:      64 * 1024, // 64 MB
	Iterations:  3,
	Parallelism: 2,
	SaltLength:  16,
	KeyLength:   32,
}

// BcryptMake hashes a password using bcrypt.
func BcryptMake(password string, cost ...int) (string, error) {
	c := DefaultBcryptCost
	if len(cost) > 0 {
		c = cost[0]
	}
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), c)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// BcryptCheck verifies a plain password against a bcrypt hash.
func BcryptCheck(password, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// Argon2Make hashes a password using Argon2id.
func Argon2Make(password string, params ...Argon2Params) (string, error) {
	p := DefaultArgon2Params
	if len(params) > 0 {
		p = params[0]
	}

	salt := make([]byte, p.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}

	hash := argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLength)

	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)

	encoded := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Iterations, p.Parallelism, b64Salt, b64Hash)

	return encoded, nil
}

// Argon2Check verifies a plain password against an Argon2id encoded string.
func Argon2Check(password, encodedHash string) bool {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}

	var version int
	_, err := fmt.Sscanf(parts[2], "v=%d", &version)
	if err != nil || version != argon2.Version {
		return false
	}

	var memory, iterations uint32
	var parallelism uint8
	_, err = fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism)
	if err != nil {
		return false
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}

	expectedHash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}

	computedHash := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(expectedHash)))

	return subtle.ConstantTimeCompare(computedHash, expectedHash) == 1
}

// Make automatically creates a secure password hash (defaults to bcrypt).
func Make(password string) (string, error) {
	return BcryptMake(password)
}

// Check checks a password against either bcrypt or argon2id based on prefix.
func Check(password, hash string) bool {
	if strings.HasPrefix(hash, "$argon2id$") {
		return Argon2Check(password, hash)
	}
	if strings.HasPrefix(hash, "$2a$") || strings.HasPrefix(hash, "$2b$") || strings.HasPrefix(hash, "$2y$") {
		return BcryptCheck(password, hash)
	}
	return false
}

// NeedsRehash checks if a hash needs to be upgraded.
func NeedsRehash(hash string, cost int) bool {
	if strings.HasPrefix(hash, "$2a$") || strings.HasPrefix(hash, "$2b$") || strings.HasPrefix(hash, "$2y$") {
		c, err := bcrypt.Cost([]byte(hash))
		if err != nil || c < cost {
			return true
		}
	}
	return false
}

// ErrInvalidHash indicates that the hash structure was malformed.
var ErrInvalidHash = errors.New("hash: invalid hash format")
