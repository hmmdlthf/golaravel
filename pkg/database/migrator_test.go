package database

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMigratorSanitizeName(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"create_users_table", "create_users_table"},
		{"Create Users Table!", "create_users_table"},
		{"add-index-on-email", "add_index_on_email"},
		{"___foo___bar___", "foo_bar"},
	}

	for _, c := range cases {
		res := sanitizeMigrationName(c.input)
		if res != c.expected {
			t.Errorf("sanitizeMigrationName(%q) = %q, expected %q", c.input, res, c.expected)
		}
	}
}

func TestMigratorCreate(t *testing.T) {
	ctx := context.Background()
	tempDir, err := os.MkdirTemp("", "migrations-*")
	if err != nil {
		t.Fatalf("failed creating temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	m := NewMigrator(MigratorConfig{
		Dir: tempDir,
	})

	filePath, err := m.Create(ctx, "create_orders_table")
	if err != nil {
		t.Fatalf("unexpected error creating migration: %v", err)
	}

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Fatalf("expected file to exist at %s", filePath)
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed reading file: %v", err)
	}

	if len(content) == 0 {
		t.Errorf("expected non-empty migration content")
	}

	// Verify filename format
	base := filepath.Base(filePath)
	if len(base) < 15 || base[14] != '_' {
		t.Errorf("unexpected filename format: %s", base)
	}
}

func TestMigratorFindAtlasBin(t *testing.T) {
	m := NewMigrator(DefaultMigratorConfig())
	bin, err := m.FindAtlasBin()
	if err != nil {
		t.Logf("atlas binary not found in standard paths: %v", err)
	} else {
		t.Logf("found atlas binary at: %s", bin)
		if bin == "" {
			t.Errorf("expected non-empty atlas binary path")
		}
	}
}
