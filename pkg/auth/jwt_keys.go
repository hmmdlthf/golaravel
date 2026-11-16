package auth

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/goccy/go-json"
)

// ErrNoSigningKey is returned by GenerateToken on a verify-only manager
// (NewJWTVerifier / NewJWTVerifierFromJWKS).
var ErrNoSigningKey = errors.New("auth: JWT manager has no signing key (verify-only)")

// jwtVerifier checks a signature for one algorithm and key id.
type jwtVerifier struct {
	alg    string
	kid    string
	public crypto.PublicKey
	verify func(message, sig []byte) bool
}

// NewJWTManagerWithKey signs tokens with a private key, so other services
// can verify them with only the public key (see JWKS and NewJWTVerifier).
// The algorithm follows the key: *rsa.PrivateKey (2048 bits or more) ->
// RS256, *ecdsa.PrivateKey on P-256 -> ES256, ed25519.PrivateKey -> EdDSA.
// kid names the key in the token header, for rotation.
func NewJWTManagerWithKey(key crypto.Signer, kid string, ttl time.Duration) (*JWTManager, error) {
	alg, err := algFor(key.Public())
	if err != nil {
		return nil, err
	}
	m := &JWTManager{ttl: ttl, keyed: true, alg: alg, kid: kid}
	m.signer = func(message []byte) ([]byte, error) { return signAsym(key, alg, message) }
	v, err := newVerifier(key.Public(), kid)
	if err != nil {
		return nil, err
	}
	m.verifiers = []jwtVerifier{v}
	return m, nil
}

// NewJWTVerifier creates a verify-only manager for tokens signed by another
// service holding the matching private key.
func NewJWTVerifier(public crypto.PublicKey, kid string) (*JWTManager, error) {
	v, err := newVerifier(public, kid)
	if err != nil {
		return nil, err
	}
	return &JWTManager{keyed: true, verifiers: []jwtVerifier{v}}, nil
}

// AcceptKey also accepts tokens signed with another public key, for key
// rotation: keep the old key while its tokens are still valid.
func (m *JWTManager) AcceptKey(public crypto.PublicKey, kid string) (*JWTManager, error) {
	if !m.keyed {
		return nil, errors.New("auth: AcceptKey needs a key-based manager; use RotateFrom for HS256 secrets")
	}
	v, err := newVerifier(public, kid)
	if err != nil {
		return nil, err
	}
	m.verifiers = append(m.verifiers, v)
	return m, nil
}

func algFor(public crypto.PublicKey) (string, error) {
	switch k := public.(type) {
	case *rsa.PublicKey:
		if k.N.BitLen() < 2048 {
			return "", fmt.Errorf("auth: RSA key of %d bits is too small (use 2048 or more)", k.N.BitLen())
		}
		return "RS256", nil
	case *ecdsa.PublicKey:
		if k.Curve != elliptic.P256() {
			return "", errors.New("auth: only P-256 ECDSA keys are supported (ES256)")
		}
		return "ES256", nil
	case ed25519.PublicKey:
		return "EdDSA", nil
	}
	return "", fmt.Errorf("auth: unsupported JWT key type %T", public)
}

func newVerifier(public crypto.PublicKey, kid string) (jwtVerifier, error) {
	alg, err := algFor(public)
	if err != nil {
		return jwtVerifier{}, err
	}
	v := jwtVerifier{alg: alg, kid: kid, public: public}
	switch k := public.(type) {
	case *rsa.PublicKey:
		v.verify = func(msg, sig []byte) bool {
			h := sha256.Sum256(msg)
			return rsa.VerifyPKCS1v15(k, crypto.SHA256, h[:], sig) == nil
		}
	case *ecdsa.PublicKey:
		v.verify = func(msg, sig []byte) bool {
			if len(sig) != 64 {
				return false
			}
			h := sha256.Sum256(msg)
			r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
			return ecdsa.Verify(k, h[:], r, s)
		}
	case ed25519.PublicKey:
		v.verify = func(msg, sig []byte) bool { return ed25519.Verify(k, msg, sig) }
	}
	return v, nil
}

func signAsym(key crypto.Signer, alg string, message []byte) ([]byte, error) {
	switch alg {
	case "RS256":
		h := sha256.Sum256(message)
		return key.Sign(rand.Reader, h[:], crypto.SHA256)
	case "ES256":
		priv, ok := key.(*ecdsa.PrivateKey)
		if !ok {
			return nil, errors.New("auth: ES256 needs an *ecdsa.PrivateKey")
		}
		h := sha256.Sum256(message)
		r, s, err := ecdsa.Sign(rand.Reader, priv, h[:])
		if err != nil {
			return nil, err
		}
		// JOSE wants the fixed-size r || s form, not ASN.1.
		out := make([]byte, 64)
		r.FillBytes(out[:32])
		s.FillBytes(out[32:])
		return out, nil
	case "EdDSA":
		return key.Sign(rand.Reader, message, crypto.Hash(0))
	}
	return nil, fmt.Errorf("auth: unsupported algorithm %s", alg)
}

// ---------------------------------------------------------------------------
// PEM helpers

// ParsePrivateKeyPEM reads a PKCS#8, PKCS#1 (RSA) or SEC 1 (EC) private key.
func ParsePrivateKeyPEM(data []byte) (crypto.Signer, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("auth: no PEM block found")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if s, ok := k.(crypto.Signer); ok {
			return s, nil
		}
		return nil, fmt.Errorf("auth: unsupported private key type %T", k)
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	if k, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	return nil, errors.New("auth: unrecognised private key format")
}

// ParsePublicKeyPEM reads a PKIX ("PUBLIC KEY") or PKCS#1 RSA public key.
func ParsePublicKeyPEM(data []byte) (crypto.PublicKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("auth: no PEM block found")
	}
	if k, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		return k, nil
	}
	if k, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
		return k, nil
	}
	return nil, errors.New("auth: unrecognised public key format")
}

// ---------------------------------------------------------------------------
// JWKS

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid,omitempty"`
	Use string `json:"use,omitempty"`
	Alg string `json:"alg,omitempty"`
	N   string `json:"n,omitempty"`
	E   string `json:"e,omitempty"`
	Crv string `json:"crv,omitempty"`
	X   string `json:"x,omitempty"`
	Y   string `json:"y,omitempty"`
}

var b64 = base64.RawURLEncoding

// JWKS returns the manager's public keys as a JSON Web Key Set, for
// serving at /.well-known/jwks.json so other services can verify tokens.
func (m *JWTManager) JWKS() ([]byte, error) {
	if !m.keyed {
		return nil, errors.New("auth: JWKS needs a key-based manager (HS256 secrets must not be published)")
	}
	set := struct {
		Keys []jwk `json:"keys"`
	}{Keys: []jwk{}}
	for _, v := range m.verifiers {
		k := jwk{Kid: v.kid, Use: "sig", Alg: v.alg}
		switch p := v.public.(type) {
		case *rsa.PublicKey:
			k.Kty, k.N, k.E = "RSA", b64.EncodeToString(p.N.Bytes()), b64.EncodeToString(big.NewInt(int64(p.E)).Bytes())
		case *ecdsa.PublicKey:
			ecdh, err := p.ECDH()
			if err != nil {
				return nil, err
			}
			raw := ecdh.Bytes() // 0x04 || X || Y
			k.Kty, k.Crv, k.X, k.Y = "EC", "P-256", b64.EncodeToString(raw[1:33]), b64.EncodeToString(raw[33:])
		case ed25519.PublicKey:
			k.Kty, k.Crv, k.X = "OKP", "Ed25519", b64.EncodeToString(p)
		}
		set.Keys = append(set.Keys, k)
	}
	return json.Marshal(set)
}

// NewJWTVerifierFromJWKS creates a verify-only manager from a JSON Web Key
// Set (for example fetched from the issuer's /.well-known/jwks.json).
func NewJWTVerifierFromJWKS(data []byte) (*JWTManager, error) {
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.Unmarshal(data, &set); err != nil {
		return nil, fmt.Errorf("auth: invalid JWKS: %w", err)
	}
	m := &JWTManager{keyed: true}
	for _, k := range set.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		pub, err := k.publicKey()
		if err != nil {
			return nil, err
		}
		v, err := newVerifier(pub, k.Kid)
		if err != nil {
			return nil, err
		}
		m.verifiers = append(m.verifiers, v)
	}
	if len(m.verifiers) == 0 {
		return nil, errors.New("auth: JWKS has no signing keys")
	}
	return m, nil
}

func (k jwk) publicKey() (crypto.PublicKey, error) {
	dec := func(s string) ([]byte, error) { return b64.DecodeString(s) }
	switch k.Kty {
	case "RSA":
		n, err := dec(k.N)
		if err != nil {
			return nil, err
		}
		e, err := dec(k.E)
		if err != nil {
			return nil, err
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}, nil
	case "EC":
		if k.Crv != "P-256" {
			return nil, fmt.Errorf("auth: unsupported EC curve %q", k.Crv)
		}
		x, err := dec(k.X)
		if err != nil {
			return nil, err
		}
		y, err := dec(k.Y)
		if err != nil {
			return nil, err
		}
		if len(x) != 32 || len(y) != 32 {
			return nil, errors.New("auth: invalid P-256 coordinates")
		}
		raw := append(append([]byte{4}, x...), y...)
		pub, err := ecdsaFromUncompressed(raw)
		if err != nil {
			return nil, err
		}
		return pub, nil
	case "OKP":
		if k.Crv != "Ed25519" {
			return nil, fmt.Errorf("auth: unsupported OKP curve %q", k.Crv)
		}
		x, err := dec(k.X)
		if err != nil || len(x) != ed25519.PublicKeySize {
			return nil, errors.New("auth: invalid Ed25519 key")
		}
		return ed25519.PublicKey(x), nil
	}
	return nil, fmt.Errorf("auth: unsupported key type %q", k.Kty)
}

// ecdsaFromUncompressed parses a P-256 point via PKIX, which validates
// that it lies on the curve.
func ecdsaFromUncompressed(raw []byte) (*ecdsa.PublicKey, error) {
	// SubjectPublicKeyInfo prefix for an uncompressed P-256 key.
	prefix := []byte{0x30, 0x59, 0x30, 0x13, 0x06, 0x07, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x02, 0x01,
		0x06, 0x08, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x03, 0x01, 0x07, 0x03, 0x42, 0x00}
	k, err := x509.ParsePKIXPublicKey(append(prefix, raw...))
	if err != nil {
		return nil, fmt.Errorf("auth: invalid P-256 key: %w", err)
	}
	pub, ok := k.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.New("auth: invalid P-256 key")
	}
	return pub, nil
}
