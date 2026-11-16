package auth

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
	"time"
)

func testKeys(t *testing.T) map[string]crypto.Signer {
	t.Helper()
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ecKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	_, edKey, _ := ed25519.GenerateKey(rand.Reader)
	return map[string]crypto.Signer{"RS256": rsaKey, "ES256": ecKey, "EdDSA": edKey}
}

func TestJWTAsymmetricSignAndVerify(t *testing.T) {
	for alg, key := range testKeys(t) {
		issuer, err := NewJWTManagerWithKey(key, "k1", time.Hour)
		if err != nil {
			t.Fatalf("%s: %v", alg, err)
		}
		issuer.Issuer("auth.example").Audience("api")
		tok, err := issuer.GenerateToken("42", map[string]any{"role": "admin"})
		if err != nil {
			t.Fatalf("%s: generate: %v", alg, err)
		}
		hdr, _ := base64.RawURLEncoding.DecodeString(strings.Split(tok, ".")[0])
		if !strings.Contains(string(hdr), `"alg":"`+alg+`"`) || !strings.Contains(string(hdr), `"kid":"k1"`) {
			t.Fatalf("%s: header %s", alg, hdr)
		}
		claims, err := issuer.VerifyToken(tok)
		if err != nil || claims.Sub != "42" || claims.Custom["role"] != "admin" {
			t.Fatalf("%s: self-verify: %+v %v", alg, claims, err)
		}

		// Another service holding only the public key.
		verifier, err := NewJWTVerifier(key.Public(), "k1")
		if err != nil {
			t.Fatal(err)
		}
		verifier.Issuer("auth.example").Audience("api")
		if _, err := verifier.VerifyToken(tok); err != nil {
			t.Fatalf("%s: public-key verify: %v", alg, err)
		}
		if _, err := verifier.GenerateToken("1"); !errors.Is(err, ErrNoSigningKey) {
			t.Fatalf("%s: verifier must not sign: %v", alg, err)
		}

		// Tampering with the claims breaks the signature.
		parts := strings.Split(tok, ".")
		parts[1] = base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"1","exp":9999999999}`))
		if _, err := verifier.VerifyToken(strings.Join(parts, ".")); !errors.Is(err, ErrTokenInvalid) {
			t.Fatalf("%s: tampered token accepted: %v", alg, err)
		}
	}
}

func TestJWTWrongKeyAndAlgConfusion(t *testing.T) {
	keys := testKeys(t)
	signer, _ := NewJWTManagerWithKey(keys["RS256"], "", time.Hour)
	tok, _ := signer.GenerateToken("1")

	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	wrong, _ := NewJWTVerifier(other.Public(), "")
	if _, err := wrong.VerifyToken(tok); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("token verified with the wrong key: %v", err)
	}

	// Classic attack: HS256 signed with the PEM of the public key as secret.
	pubDER, _ := x509.MarshalPKIXPublicKey(keys["RS256"].Public())
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
	forged, _ := NewJWTManager(string(pubPEM), time.Hour).GenerateToken("admin")
	verifier, _ := NewJWTVerifier(keys["RS256"].Public(), "")
	if _, err := verifier.VerifyToken(forged); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("HS256 token accepted by a key-based verifier: %v", err)
	}
	// And the other way: a secret manager refuses RS256 tokens.
	if _, err := NewJWTManager("secret-secret-secret", time.Hour).VerifyToken(tok); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("RS256 token accepted by an HS256 manager: %v", err)
	}
}

func TestJWTKeyRotationAndJWKS(t *testing.T) {
	keys := testKeys(t)
	old, _ := NewJWTManagerWithKey(keys["ES256"], "2026-01", time.Hour)
	oldTok, _ := old.GenerateToken("1")

	cur, _ := NewJWTManagerWithKey(keys["EdDSA"], "2026-06", time.Hour)
	if _, err := cur.AcceptKey(keys["ES256"].Public(), "2026-01"); err != nil {
		t.Fatal(err)
	}
	newTok, _ := cur.GenerateToken("2")
	for _, tok := range []string{oldTok, newTok} {
		if _, err := cur.VerifyToken(tok); err != nil {
			t.Fatalf("rotation: %v", err)
		}
	}

	// A token naming a key id is only checked against that key.
	parts := strings.Split(oldTok, ".")
	parts[0] = base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256","typ":"JWT","kid":"unknown"}`))
	if _, err := cur.VerifyToken(strings.Join(parts, ".")); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("unknown kid accepted: %v", err)
	}

	jwks, err := cur.JWKS()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"kty":"OKP"`, `"crv":"Ed25519"`, `"kty":"EC"`, `"crv":"P-256"`, `"kid":"2026-06"`, `"kid":"2026-01"`} {
		if !strings.Contains(string(jwks), want) {
			t.Fatalf("JWKS missing %s: %s", want, jwks)
		}
	}
	remote, err := NewJWTVerifierFromJWKS(jwks)
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{oldTok, newTok} {
		if _, err := remote.VerifyToken(tok); err != nil {
			t.Fatalf("verify from JWKS: %v", err)
		}
	}

	rsaMgr, _ := NewJWTManagerWithKey(keys["RS256"], "r1", time.Hour)
	rsaJWKS, _ := rsaMgr.JWKS()
	rsaTok, _ := rsaMgr.GenerateToken("3")
	if v, err := NewJWTVerifierFromJWKS(rsaJWKS); err != nil {
		t.Fatal(err)
	} else if _, err := v.VerifyToken(rsaTok); err != nil {
		t.Fatalf("RSA via JWKS: %v", err)
	}

	if _, err := NewJWTManager("s", time.Hour).JWKS(); err == nil {
		t.Fatal("an HS256 secret must never be published as JWKS")
	}
}

func TestJWTKeyValidationAndPEM(t *testing.T) {
	small, _ := rsa.GenerateKey(rand.Reader, 1024)
	if _, err := NewJWTManagerWithKey(small, "", time.Hour); err == nil {
		t.Fatal("1024-bit RSA must be refused")
	}
	p384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if _, err := NewJWTManagerWithKey(p384, "", time.Hour); err == nil {
		t.Fatal("non-P-256 curves must be refused")
	}

	keys := testKeys(t)
	for alg, key := range keys {
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		priv, err := ParsePrivateKeyPEM(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
		if err != nil {
			t.Fatalf("%s: parse private: %v", alg, err)
		}
		pubDER, _ := x509.MarshalPKIXPublicKey(key.Public())
		pub, err := ParsePublicKeyPEM(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
		if err != nil {
			t.Fatalf("%s: parse public: %v", alg, err)
		}
		m, _ := NewJWTManagerWithKey(priv, "", time.Hour)
		v, _ := NewJWTVerifier(pub, "")
		tok, _ := m.GenerateToken("1")
		if _, err := v.VerifyToken(tok); err != nil {
			t.Fatalf("%s: PEM round trip: %v", alg, err)
		}
	}
	if _, err := ParsePrivateKeyPEM([]byte("nope")); err == nil {
		t.Fatal("garbage PEM accepted")
	}
}
