package storage

import (
	"bytes"
	"log/slog"
	"testing"
	"time"
)

func TestSigningWarningOnlyOnUse(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	d, err := NewLocalDriver(t.TempDir(), "/s")
	if err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("warned before use: %s", buf.String())
	}
	_, _ = d.TemporaryURL("a.txt", time.Minute)
	_, _ = d.TemporaryURL("b.txt", time.Minute)
	if n := bytes.Count(buf.Bytes(), []byte("no signing secret")); n != 1 {
		t.Fatalf("want one warning, got %d", n)
	}
}
