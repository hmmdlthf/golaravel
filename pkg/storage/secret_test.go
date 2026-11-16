package storage

import (
	"testing"
	"time"
)

func TestLocalDriverHasNoPublicDefaultSecret(t *testing.T) {
	a, _ := NewLocalDriver(t.TempDir(), "http://files.test")
	public, _ := NewLocalDriver(t.TempDir(), "http://files.test", insecureDefaultSecret)
	if string(a.secret) == insecureDefaultSecret || string(public.secret) == insecureDefaultSecret {
		t.Fatal("the public fallback secret must never be used")
	}
	// A URL signed with the old public key must not verify.
	forger := &LocalDriver{baseURL: "http://files.test", secret: []byte(insecureDefaultSecret)}
	forged, _ := forger.TemporaryURL("invoices/1.pdf", time.Hour)
	if err := a.VerifySignedURL(forged); err == nil {
		t.Fatal("URL forged with the old default secret was accepted")
	}
	own, _ := a.TemporaryURL("invoices/1.pdf", time.Hour)
	if err := a.VerifySignedURL(own); err != nil {
		t.Fatalf("own signed URL: %v", err)
	}
}
