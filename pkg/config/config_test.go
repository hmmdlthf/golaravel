package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeEnv(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadEnvParsing(t *testing.T) {
	keys := []string{"GLT_PLAIN", "GLT_DQ", "GLT_SQ", "GLT_COMMENT", "GLT_HASH_IN_QUOTES", "GLT_EXPORTED", "GLT_ESCAPES", "GLT_SQ_RAW", "GLT_EMPTY", "GLT_EQ"}
	for _, k := range keys {
		_ = os.Unsetenv(k)
	}
	t.Cleanup(func() {
		for _, k := range keys {
			_ = os.Unsetenv(k)
		}
	})
	p := writeEnv(t, `
# a comment line
GLT_PLAIN=hello
GLT_DQ="double quoted"
GLT_SQ='single quoted'
GLT_COMMENT=value # trailing comment
GLT_HASH_IN_QUOTES="a # b"
export GLT_EXPORTED=yes
GLT_ESCAPES="line1\nline2 \"q\""
GLT_SQ_RAW='raw\n'
GLT_EMPTY=
GLT_EQ=a=b=c
not a key value line
`)
	s := NewStore()
	if err := s.LoadEnv(p); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"GLT_PLAIN":          "hello",
		"GLT_DQ":             "double quoted",
		"GLT_SQ":             "single quoted",
		"GLT_COMMENT":        "value",
		"GLT_HASH_IN_QUOTES": "a # b",
		"GLT_EXPORTED":       "yes",
		"GLT_ESCAPES":        "line1\nline2 \"q\"",
		"GLT_SQ_RAW":         `raw\n`,
		"GLT_EQ":             "a=b=c",
	} {
		if got := s.Get(k, "?"); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if got := s.Get("GLT_EMPTY", "default"); got != "" {
		t.Errorf("empty value should be stored as empty, got %q", got)
	}
	if os.Getenv("GLT_PLAIN") != "hello" {
		t.Error("LoadEnv should export values to the process environment")
	}
}

func TestEnvironmentWinsOverFile(t *testing.T) {
	t.Setenv("GLT_PRECEDENCE", "from-env")
	p := writeEnv(t, "GLT_PRECEDENCE=from-file\n")
	s := NewStore()
	if err := s.LoadEnv(p); err != nil {
		t.Fatal(err)
	}
	if got := s.Get("GLT_PRECEDENCE", ""); got != "from-env" {
		t.Fatalf("real environment variables must win over .env: %q", got)
	}
}

func TestMissingFileIsOptional(t *testing.T) {
	if err := NewStore().LoadEnv(filepath.Join(t.TempDir(), "nope.env")); err != nil {
		t.Fatalf("a missing .env must not be an error: %v", err)
	}
}

func TestTypedGetters(t *testing.T) {
	s := NewStore()
	s.Set("GLT_INT", "42")
	s.Set("GLT_BADINT", "x")
	s.Set("GLT_BOOL", "Yes")
	s.Set("GLT_FALSE", "off")
	s.Set("GLT_DUR", "1m30s")
	if s.GetInt("GLT_INT", 0) != 42 || s.GetInt("GLT_BADINT", 7) != 7 || s.GetInt("GLT_NONE", 3) != 3 {
		t.Error("GetInt")
	}
	if !s.GetBool("GLT_BOOL", false) || s.GetBool("GLT_FALSE", true) || !s.GetBool("GLT_NONE", true) {
		t.Error("GetBool")
	}
	if s.GetDuration("GLT_DUR", 0) != 90*time.Second || s.GetDuration("GLT_NONE", time.Second) != time.Second {
		t.Error("GetDuration")
	}
}
