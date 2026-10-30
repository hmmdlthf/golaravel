package database

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// MigratorConfig configures the Atlas migration engine.
type MigratorConfig struct {
	Dir      string // Migration files directory (e.g., "database/migrations")
	URL      string // Target database URL (e.g., "sqlite://database.sqlite")
	DevURL   string // Dev database URL for computing diffs (e.g., "sqlite://file?mode=memory&cache=shared")
	Schema   string // Desired schema provider (e.g., "ent://app/schema")
	AtlasBin string // Custom path to atlas binary (optional)
}

// DefaultMigratorConfig provides standard defaults for GoLaravel projects.
func DefaultMigratorConfig() MigratorConfig {
	return MigratorConfig{
		Dir:    "database/migrations",
		URL:    "sqlite://database.sqlite",
		DevURL: "sqlite://file?mode=memory&cache=shared",
		Schema: "ent://app/schema",
	}
}

// Migrator manages database migrations using Atlas and Ent.
type Migrator struct {
	cfg MigratorConfig
}

// NewMigrator creates a new migration engine instance.
func NewMigrator(cfg MigratorConfig) *Migrator {
	if cfg.Dir == "" {
		cfg.Dir = "database/migrations"
	}
	if cfg.DevURL == "" {
		cfg.DevURL = "sqlite://file?mode=memory&cache=shared"
	}
	if cfg.Schema == "" {
		cfg.Schema = "ent://app/schema"
	}
	return &Migrator{cfg: cfg}
}

// FindAtlasBin discovers the Atlas CLI binary on the system.
func (m *Migrator) FindAtlasBin() (string, error) {
	if m.cfg.AtlasBin != "" {
		if _, err := os.Stat(m.cfg.AtlasBin); err == nil {
			return m.cfg.AtlasBin, nil
		}
	}

	if path, err := exec.LookPath("atlas"); err == nil {
		return path, nil
	}

	home, err := os.UserHomeDir()
	if err == nil {
		localBin := filepath.Join(home, ".local", "bin", "atlas")
		if _, err := os.Stat(localBin); err == nil {
			return localBin, nil
		}
	}

	return "", fmt.Errorf("atlas CLI not found in PATH or ~/.local/bin/atlas. Install via 'curl -sSf https://atlasgo.sh | sh'")
}

// Apply executes pending migration files on the connected database.
func (m *Migrator) Apply(ctx context.Context, dryRun bool) (string, error) {
	bin, err := m.FindAtlasBin()
	if err != nil {
		return "", err
	}

	dirURL := "file://" + m.cfg.Dir
	args := []string{"migrate", "apply", "--dir", dirURL, "--url", m.cfg.URL}
	if dryRun {
		args = append(args, "--dry-run")
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("migration apply failed: %s: %w", strings.TrimSpace(stderr.String()), err)
	}

	return strings.TrimSpace(stdout.String()), nil
}

// Diff generates a new migration file by comparing the current migration directory with Ent schema.
func (m *Migrator) Diff(ctx context.Context, name string) (string, error) {
	bin, err := m.FindAtlasBin()
	if err != nil {
		return "", err
	}

	dirURL := "file://" + m.cfg.Dir
	args := []string{
		"migrate", "diff", name,
		"--dir", dirURL,
		"--to", m.cfg.Schema,
		"--dev-url", m.cfg.DevURL,
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("migration diff failed: %s: %w", strings.TrimSpace(stderr.String()), err)
	}

	out := stdout.String()
	if strings.Contains(out, "The migration directory is already in sync") ||
		strings.Contains(stderr.String(), "already in sync") {
		return "The migration directory is already in sync with schema.", nil
	}

	return strings.TrimSpace(out), nil
}

// Status inspects the current migration status.
func (m *Migrator) Status(ctx context.Context) (string, error) {
	bin, err := m.FindAtlasBin()
	if err != nil {
		return "", err
	}

	dirURL := "file://" + m.cfg.Dir
	args := []string{"migrate", "status", "--dir", dirURL, "--url", m.cfg.URL}

	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("migration status failed: %s: %w", strings.TrimSpace(stderr.String()), err)
	}

	return strings.TrimSpace(stdout.String()), nil
}

// Rollback reverts applied migrations (using atlas migrate down or rollback count).
func (m *Migrator) Rollback(ctx context.Context, count int) (string, error) {
	bin, err := m.FindAtlasBin()
	if err != nil {
		return "", err
	}

	dirURL := "file://" + m.cfg.Dir
	args := []string{"migrate", "down", "--dir", dirURL, "--url", m.cfg.URL}
	if count > 0 {
		args = append(args, fmt.Sprintf("%d", count))
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("migration rollback failed: %s: %w", strings.TrimSpace(stderr.String()), err)
	}

	return strings.TrimSpace(stdout.String()), nil
}

// Hash recalculates the migration directory atlas.sum hash.
func (m *Migrator) Hash(ctx context.Context) error {
	bin, err := m.FindAtlasBin()
	if err != nil {
		return err
	}

	dirURL := "file://" + m.cfg.Dir
	cmd := exec.CommandContext(ctx, bin, "migrate", "hash", "--dir", dirURL)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("migration hash failed: %s: %w", strings.TrimSpace(stderr.String()), err)
	}

	return nil
}

// Create generates a new blank migration file with standard Atlas timestamp.
func (m *Migrator) Create(ctx context.Context, name string) (string, error) {
	if err := os.MkdirAll(m.cfg.Dir, 0755); err != nil {
		return "", err
	}

	safeName := sanitizeMigrationName(name)
	timestamp := time.Now().Format("20060102150405")
	filename := fmt.Sprintf("%s_%s.sql", timestamp, safeName)
	targetPath := filepath.Join(m.cfg.Dir, filename)

	content := fmt.Sprintf(`-- Migration: %s
-- Created at: %s

-- Write raw SQL statements here for custom operations.
`, name, time.Now().Format(time.RFC3339))

	if err := os.WriteFile(targetPath, []byte(content), 0644); err != nil {
		return "", err
	}

	// Update hash if atlas is available
	_ = m.Hash(ctx)

	return targetPath, nil
}

func sanitizeMigrationName(s string) string {
	s = strings.ToLower(s)
	reg := regexp.MustCompile("[^a-z0-9]+")
	s = reg.ReplaceAllString(s, "_")
	collapse := regexp.MustCompile("_+")
	s = collapse.ReplaceAllString(s, "_")
	return strings.Trim(s, "_")
}
