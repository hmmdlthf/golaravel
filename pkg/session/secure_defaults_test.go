package session

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golaravel/golaravel/pkg/context"
)

func TestNewManagerKeepsSecureDefaultsWithPartialConfig(t *testing.T) {
	m := NewManager(Config{Lifetime: 1})
	if !m.cfg.HttpOnly || !m.cfg.Encrypt {
		t.Fatalf("partial Config must not turn off HttpOnly / encryption: %+v", m.cfg)
	}
	if m.cfg.SecretKey == "" || m.cfg.SecretKey == insecureDefaultSecret {
		t.Fatalf("missing key must be replaced by a random one, got %q", m.cfg.SecretKey)
	}
	if NewManager().cfg.SecretKey == NewManager().cfg.SecretKey {
		t.Fatal("random keys must differ between managers")
	}
	m = NewManager(Config{AllowScriptAccess: true, DisableEncryption: true, SecretKey: "k"})
	if m.cfg.HttpOnly || m.cfg.Encrypt {
		t.Fatalf("explicit opt-outs ignored: %+v", m.cfg)
	}
	m = NewManager(Config{Driver: NewCookieDriver(), DisableEncryption: true, SecretKey: "k"})
	if !m.cfg.Encrypt {
		t.Fatal("cookie driver must always encrypt")
	}
}

func TestInsecureDefaultSecretIsReplaced(t *testing.T) {
	if NewManager(Config{SecretKey: insecureDefaultSecret}).cfg.SecretKey == insecureDefaultSecret {
		t.Fatal("the public example key must not be used")
	}
}

func TestSessionCookieIsHttpOnlyByDefault(t *testing.T) {
	m := NewManager(Config{SecretKey: "testing-secret-key-32-chars-long"})
	rec := httptest.NewRecorder()
	c := &context.Context{}
	c.Reset(rec, httptest.NewRequest("GET", "/", nil))
	if err := m.Save(c, m.Start(c)); err != nil {
		t.Fatal(err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie flags: %+v", cookies)
	}
}

func TestFileDriverRejectsPathLikeIDs(t *testing.T) {
	base := t.TempDir()
	d, err := NewFileDriver(filepath.Join(base, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "outside")
	_ = os.WriteFile(outside, []byte(`{"id":"x"}`), 0o600)
	for _, id := range []string{"../outside", "..", "a/b/c/d/e/f/g/h/i", strings.Repeat("a", 200)} {
		if _, err := d.Read(id); err == nil {
			t.Fatalf("Read(%q) should fail", id)
		}
		if err := d.Write(id, []byte("x")); err == nil {
			t.Fatalf("Write(%q) should fail", id)
		}
	}
	if b, _ := os.ReadFile(outside); string(b) != `{"id":"x"}` {
		t.Fatal("file outside the session dir was modified")
	}
	id := generateSessionID()
	if err := d.Write(id, []byte("ok")); err != nil {
		t.Fatalf("valid id: %v", err)
	}
	if b, err := d.Read(id); err != nil || string(b) != "ok" {
		t.Fatalf("valid id read: %q %v", b, err)
	}
}
