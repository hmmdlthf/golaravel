package session

import (
	"bytes"
	"fmt"
	"log/slog"
	"testing"
)

func userPayload(uid string) []byte {
	return []byte(fmt.Sprintf(`{"id":"x","attributes":{%q:%q}}`, AuthUserKey, uid))
}

func exerciseUserDriver(t *testing.T, d UserSessionDriver) {
	t.Helper()
	ids := map[string]string{"aaaaaaaaaaaaaaaa1": "7", "aaaaaaaaaaaaaaaa2": "7", "bbbbbbbbbbbbbbbb1": "8"}
	for id, uid := range ids {
		if err := d.WriteUser(id, userPayload(uid), uid); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.DestroyUser("7", "aaaaaaaaaaaaaaaa1"); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]bool{"aaaaaaaaaaaaaaaa1": true, "aaaaaaaaaaaaaaaa2": false, "bbbbbbbbbbbbbbbb1": true} {
		if _, err := d.Read(id); (err == nil) != want {
			t.Fatalf("%s exists=%v want %v", id, err == nil, want)
		}
	}
}

func TestUserSessionDrivers(t *testing.T) {
	t.Run("memory", func(t *testing.T) { exerciseUserDriver(t, NewMemoryDriver()) })
	t.Run("file", func(t *testing.T) {
		d, err := NewFileDriver(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		exerciseUserDriver(t, d)
	})
	t.Run("sql", func(t *testing.T) { d, _ := newSQLDriver(t); exerciseUserDriver(t, d) })
	t.Run("redis", func(t *testing.T) {
		d := newRedisDriver(t)
		exerciseUserDriver(t, d)
		// A stale index entry (the session now belongs to someone else)
		// never ends the other user's session.
		_ = d.WriteUser("cccccccccccccccc1", userPayload("7"), "7")
		_ = d.WriteUser("cccccccccccccccc1", userPayload("9"), "9")
		_ = d.DestroyUser("7")
		if _, err := d.Read("cccccccccccccccc1"); err != nil {
			t.Fatal("stale index removed another user's session")
		}
	})
}

func TestCookieDriverReportsUnsupported(t *testing.T) {
	m := NewManager(Config{Driver: NewCookieDriver(), SecretKey: "k"})
	if err := m.DestroyUserSessions("7"); err != ErrUserSessionsUnsupported {
		t.Fatalf("got %v", err)
	}
}

func TestManagerRegenerateDeletesOldID(t *testing.T) {
	d := NewMemoryDriver()
	m := NewManager(Config{Driver: d, SecretKey: "k"})
	s := NewSession()
	old := s.ID()
	_ = d.Write(old, []byte("{}"))
	if err := m.Regenerate(s); err != nil {
		t.Fatal(err)
	}
	if s.ID() == old {
		t.Fatal("id not changed")
	}
	if _, err := d.Read(old); err == nil {
		t.Fatal("old session id still valid")
	}
}

func TestDefaultManagerIsLazyAndUsesAppKey(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)
	SetDefaultManager(nil)
	defer SetDefaultManager(nil)

	t.Setenv("APP_KEY", "base64:some-application-key")
	m := DefaultManager()
	if m.cfg.SecretKey != "base64:some-application-key" {
		t.Fatalf("default manager key = %q", m.cfg.SecretKey)
	}
	if buf.Len() != 0 {
		t.Fatalf("unexpected warning: %s", buf.String())
	}
	SetDefaultManager(nil)
	t.Setenv("APP_KEY", "")
	_ = DefaultManager()
	if !bytes.Contains(buf.Bytes(), []byte("no SecretKey")) {
		t.Fatal("missing warning when the default is used without a key")
	}
}
