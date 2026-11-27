package offline

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Payload encryption
//
// HTTPS protects sync traffic in transit. Payload encryption adds a layer
// for sensitive ledgers: TLS-terminating proxies, request logs and
// middleware only see ciphertext, and a request can only be made by the
// device holding its private key.
//
// At registration the device sends an ECDH P-256 public key (WebCrypto
// "raw" export, base64url). The server answers with its own public key.
// Both derive the same AES-256-GCM key:
//
//	secret = ECDH(device, server)                  // 32-byte x coordinate
//	key    = HKDF-SHA256(secret, salt = device id, info = "golaravel-offline/1 aes-256-gcm")
//
// Bodies are then sent as an Envelope. The additional authenticated data
// binds each envelope to its direction, endpoint, tenant and device, so a
// captured envelope cannot be replayed to another endpoint, device or
// tenant. Replaying a push to the same endpoint is harmless, since pushes
// are idempotent.

// ServerKey is one of the server's ECDH keys. Keep it secret, like an app
// key; list old keys after the current one when rotating, so devices
// registered with them keep working.
type ServerKey struct {
	ID      string
	Private *ecdh.PrivateKey
}

// GenerateServerKey makes a new P-256 server key.
func GenerateServerKey(id string) (ServerKey, error) {
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	return ServerKey{ID: id, Private: k}, err
}

// ParseServerKey decodes a key saved with ServerKey.Encode.
func ParseServerKey(id, encoded string) (ServerKey, error) {
	b, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return ServerKey{}, fmt.Errorf("offline: server key: %w", err)
	}
	k, err := ecdh.P256().NewPrivateKey(b)
	if err != nil {
		return ServerKey{}, fmt.Errorf("offline: server key: %w", err)
	}
	return ServerKey{ID: id, Private: k}, nil
}

// Encode returns the private key as base64url, for an env variable.
func (k ServerKey) Encode() string { return base64.RawURLEncoding.EncodeToString(k.Private.Bytes()) }

// PublicKey is the base64url raw public key clients derive with.
func (k ServerKey) PublicKey() string {
	return base64.RawURLEncoding.EncodeToString(k.Private.PublicKey().Bytes())
}

// Envelope is an encrypted request or response body.
type Envelope struct {
	V        int    `json:"v"`
	KeyID    string `json:"kid"`
	DeviceID string `json:"device_id"`
	IV       string `json:"iv"` // 12 bytes, base64url
	CT       string `json:"ct"` // ciphertext + 16-byte tag, base64url
}

// ServerPublicKey is what a device needs to derive its key.
type ServerPublicKey struct {
	KeyID     string `json:"kid"`
	PublicKey string `json:"public_key"`
}

var (
	ErrBadEnvelope           = errors.New("offline: envelope does not decrypt")
	ErrEncryptionRequired    = errors.New("offline: this device must encrypt its requests")
	ErrBadPublicKey          = errors.New("offline: public key must be a base64url raw P-256 key")
	ErrEncryptionUnavailable = errors.New("offline: the server has no encryption keys configured")
	ErrUnknownServerKey      = errors.New("offline: the device's server key was retired; register the device again")
)

const kdfInfo = "golaravel-offline/1 aes-256-gcm"

// deviceKey is a device's stored key material: which server key it was
// registered against and its public key.
type deviceKey struct {
	KeyID  string `json:"kid"`
	Public string `json:"pub"`
}

func parseDevicePublicKey(s string) (*ecdh.PublicKey, error) {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		return nil, ErrBadPublicKey
	}
	k, err := ecdh.P256().NewPublicKey(b)
	if err != nil {
		return nil, ErrBadPublicKey
	}
	return k, nil
}

// DeriveKey computes the AES-256-GCM key a device and the server share.
// Exported so other clients (Go, Dart) can be tested against it.
func DeriveKey(priv *ecdh.PrivateKey, peer *ecdh.PublicKey, deviceID string) ([]byte, error) {
	secret, err := priv.ECDH(peer)
	if err != nil {
		return nil, err
	}
	return hkdf.Key(sha256.New, secret, []byte(deviceID), kdfInfo, 32)
}

// AAD is the additional authenticated data for an envelope.
// dir is "req" or "res"; endpoint is "push", "pull" or "snapshot".
func AAD(dir, endpoint, tenant, deviceID string) []byte {
	return []byte("golaravel-offline/1\n" + dir + "\n" + endpoint + "\n" + tenant + "\n" + deviceID)
}

// Seal encrypts plaintext with key.
func Seal(key []byte, kid, deviceID string, aad, plaintext []byte) (*Envelope, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	iv := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(iv); err != nil {
		return nil, err
	}
	ct := gcm.Seal(nil, iv, plaintext, aad)
	return &Envelope{V: 1, KeyID: kid, DeviceID: deviceID,
		IV: base64.RawURLEncoding.EncodeToString(iv), CT: base64.RawURLEncoding.EncodeToString(ct)}, nil
}

// Open decrypts an envelope with key.
func Open(key []byte, env *Envelope, aad []byte) ([]byte, error) {
	if env.V != 1 {
		return nil, ErrBadEnvelope
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	iv, err1 := base64.RawURLEncoding.DecodeString(env.IV)
	ct, err2 := base64.RawURLEncoding.DecodeString(env.CT)
	if err1 != nil || err2 != nil || len(iv) != gcm.NonceSize() {
		return nil, ErrBadEnvelope
	}
	pt, err := gcm.Open(nil, iv, ct, aad)
	if err != nil {
		return nil, ErrBadEnvelope
	}
	return pt, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// encryption returns whether payload encryption is configured.
func (e *Engine[T]) encryption() bool { return len(e.opts.Keys) > 0 }

func (e *Engine[T]) serverKey(id string) (ServerKey, bool) {
	for _, k := range e.opts.Keys {
		if k.ID == id {
			return k, true
		}
	}
	return ServerKey{}, false
}

// deviceSecret derives a device's AES key, or returns nil when the device
// has no key.
func (e *Engine[T]) deviceSecret(d *Device) ([]byte, string, error) {
	if d.PublicKey == "" {
		return nil, "", nil
	}
	var dk deviceKey
	if err := json.Unmarshal([]byte(d.PublicKey), &dk); err != nil {
		return nil, "", err
	}
	sk, ok := e.serverKey(dk.KeyID)
	if !ok {
		return nil, "", fmt.Errorf("%w (device %s, key %q)", ErrUnknownServerKey, d.ID, dk.KeyID)
	}
	pub, err := parseDevicePublicKey(dk.Public)
	if err != nil {
		return nil, "", err
	}
	key, err := DeriveKey(sk.Private, pub, d.ID)
	return key, dk.KeyID, err
}
