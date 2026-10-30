package session

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
)

var (
	ErrDecryptionFailed = errors.New("session/encrypter: payload authentication or decryption failed")
	ErrPayloadTooShort  = errors.New("session/encrypter: ciphertext payload is too short")
)

// Encrypter provides authenticated AES-256-GCM encryption and decryption.
type Encrypter struct {
	key []byte
}

// NewEncrypter creates a new AES-256-GCM encrypter.
// If the key is not exactly 32 bytes, SHA-256 is used to derive a 256-bit key.
func NewEncrypter(key string) *Encrypter {
	k := []byte(key)
	if len(k) != 32 {
		h := sha256.Sum256(k)
		k = h[:]
	}
	return &Encrypter{key: k}
}

// Encrypt encrypts plaintext bytes with AES-256-GCM and returns a base64url string.
func (e *Encrypter) Encrypt(plaintext []byte) (string, error) {
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// Decrypt decodes a base64url string and decrypts the authenticated AES-256-GCM payload.
func (e *Encrypter) Decrypt(ciphertext string) ([]byte, error) {
	data, err := base64.RawURLEncoding.DecodeString(ciphertext)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(e.key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	if len(data) < gcm.NonceSize() {
		return nil, ErrPayloadTooShort
	}

	nonce := data[:gcm.NonceSize()]
	payload := data[gcm.NonceSize():]

	plain, err := gcm.Open(nil, nonce, payload, nil)
	if err != nil {
		return nil, ErrDecryptionFailed
	}

	return plain, nil
}
