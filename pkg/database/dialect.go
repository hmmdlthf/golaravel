package database

import (
	"fmt"
	"strings"
)

// Dialect names the SQL flavour of a *sql.DB, for the few statements that
// differ between engines (placeholders, upserts, column types). It is used
// by GoLaravel's SQL-backed stores (sessions, cache).
type Dialect string

const (
	SQLite   Dialect = "sqlite"
	MySQL    Dialect = "mysql"
	Postgres Dialect = "postgres"
)

// ParseDialect maps a database/sql driver name ("sqlite3", "pgx", "mysql",
// ...) to a Dialect.
func ParseDialect(driver string) (Dialect, error) {
	switch strings.ToLower(driver) {
	case "sqlite", "sqlite3":
		return SQLite, nil
	case "mysql":
		return MySQL, nil
	case "postgres", "postgresql", "pgx", "pq":
		return Postgres, nil
	}
	return "", fmt.Errorf("database: unsupported dialect %q", driver)
}

// Rebind rewrites ? placeholders to $1, $2, ... for Postgres.
func (d Dialect) Rebind(query string) string {
	if d != Postgres {
		return query
	}
	var sb strings.Builder
	n := 0
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			n++
			fmt.Fprintf(&sb, "$%d", n)
			continue
		}
		sb.WriteByte(query[i])
	}
	return sb.String()
}

// Upsert returns an INSERT that replaces the row with the same key column.
func (d Dialect) Upsert(table, key string, cols ...string) string {
	all := append([]string{key}, cols...)
	ph := strings.TrimSuffix(strings.Repeat("?, ", len(all)), ", ")
	q := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, strings.Join(all, ", "), ph)
	sets := make([]string, len(cols))
	for i, c := range cols {
		if d == MySQL {
			sets[i] = fmt.Sprintf("%s = VALUES(%s)", c, c)
		} else {
			sets[i] = fmt.Sprintf("%s = excluded.%s", c, c)
		}
	}
	if d == MySQL {
		q += " ON DUPLICATE KEY UPDATE " + strings.Join(sets, ", ")
	} else {
		q += fmt.Sprintf(" ON CONFLICT (%s) DO UPDATE SET %s", key, strings.Join(sets, ", "))
	}
	return d.Rebind(q)
}

// InsertIgnore returns an INSERT that does nothing when the key exists.
func (d Dialect) InsertIgnore(table string, cols ...string) string {
	ph := strings.TrimSuffix(strings.Repeat("?, ", len(cols)), ", ")
	switch d {
	case MySQL:
		return fmt.Sprintf("INSERT IGNORE INTO %s (%s) VALUES (%s)", table, strings.Join(cols, ", "), ph)
	case Postgres:
		return d.Rebind(fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT DO NOTHING", table, strings.Join(cols, ", "), ph))
	}
	return fmt.Sprintf("INSERT OR IGNORE INTO %s (%s) VALUES (%s)", table, strings.Join(cols, ", "), ph)
}

// KeyType is a column type for short string primary keys.
func (d Dialect) KeyType() string {
	if d == MySQL {
		return "VARCHAR(191)"
	}
	return "VARCHAR(255)"
}

// BlobType is a column type for opaque payloads.
func (d Dialect) BlobType() string {
	switch d {
	case MySQL:
		return "LONGBLOB"
	case Postgres:
		return "BYTEA"
	}
	return "BLOB"
}

// ForUpdate is the row-lock suffix for SELECTs inside a transaction
// (empty for SQLite, which locks the whole database on write).
func (d Dialect) ForUpdate() string {
	if d == SQLite {
		return ""
	}
	return " FOR UPDATE"
}

// ValidIdentifier reports whether name is safe to use as a table name.
func ValidIdentifier(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for i, r := range name {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || (i > 0 && r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}
