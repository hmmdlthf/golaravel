// Package integration runs GoLaravel's SQL and Redis stores against real
// servers. Each backend is used when its environment variable is set:
//
//	GOLARAVEL_TEST_MYSQL_DSN     user:pass@tcp(127.0.0.1:3306)/golaravel
//	GOLARAVEL_TEST_POSTGRES_DSN  postgres://user:pass@127.0.0.1:5432/golaravel?sslmode=disable
//	GOLARAVEL_TEST_REDIS_ADDR    127.0.0.1:6379
//
// SQLite always runs. With GOLARAVEL_INTEGRATION_REQUIRED=1 (set in CI) a
// missing backend fails the run instead of being skipped.
package integration

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/mattn/go-sqlite3"

	"github.com/golaravel/golaravel/pkg/database"
	"github.com/golaravel/golaravel/pkg/redis"
)

type sqlBackend struct {
	name    string
	db      *sql.DB
	dialect database.Dialect
}

func required() bool { return os.Getenv("GOLARAVEL_INTEGRATION_REQUIRED") == "1" }

// suffix makes table names unique per test run, so parallel CI jobs and
// re-runs against the same database never collide.
func suffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func sqlBackends(t *testing.T) []sqlBackend {
	t.Helper()
	sqlite, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "it.db")+"?_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	sqlite.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlite.Close() })
	out := []sqlBackend{{"sqlite", sqlite, database.SQLite}}

	for _, c := range []struct {
		name, env, driver string
		dialect           database.Dialect
	}{
		{"mysql", "GOLARAVEL_TEST_MYSQL_DSN", "mysql", database.MySQL},
		{"postgres", "GOLARAVEL_TEST_POSTGRES_DSN", "pgx", database.Postgres},
	} {
		dsn := os.Getenv(c.env)
		if dsn == "" {
			if required() {
				t.Fatalf("%s is required (GOLARAVEL_INTEGRATION_REQUIRED=1)", c.env)
			}
			t.Logf("%s not set: skipping %s", c.env, c.name)
			continue
		}
		db, err := sql.Open(c.driver, dsn)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if err := db.Ping(); err != nil {
			t.Fatalf("%s: ping: %v", c.name, err)
		}
		t.Cleanup(func() { db.Close() })
		out = append(out, sqlBackend{c.name, db, c.dialect})
	}
	return out
}

func redisClient(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("GOLARAVEL_TEST_REDIS_ADDR")
	if addr == "" {
		if required() {
			t.Fatal("GOLARAVEL_TEST_REDIS_ADDR is required (GOLARAVEL_INTEGRATION_REQUIRED=1)")
		}
		t.Skip("GOLARAVEL_TEST_REDIS_ADDR not set")
	}
	c, err := redis.New(redis.Config{Addr: addr})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func dropTables(t *testing.T, db *sql.DB, tables ...string) {
	t.Cleanup(func() {
		for _, tbl := range tables {
			_, _ = db.Exec("DROP TABLE IF EXISTS " + tbl)
		}
	})
}
