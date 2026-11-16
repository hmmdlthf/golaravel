package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/golaravel/golaravel/pkg/database"
)

// SQLResetTokenStore keeps password reset token hashes in a database table
// (Laravel's password_reset_tokens), so pending resets survive restarts and
// work across instances. SQLite, MySQL and Postgres are supported; create
// the table with Migrate.
type SQLResetTokenStore struct {
	db      *sql.DB
	dialect database.Dialect
	table   string
}

// NewSQLResetTokenStore creates the store; table defaults to
// "password_reset_tokens".
func NewSQLResetTokenStore(db *sql.DB, dialect database.Dialect, table ...string) (*SQLResetTokenStore, error) {
	t := "password_reset_tokens"
	if len(table) > 0 && table[0] != "" {
		t = table[0]
	}
	if !database.ValidIdentifier(t) {
		return nil, fmt.Errorf("auth: invalid table name %q", t)
	}
	return &SQLResetTokenStore{db: db, dialect: dialect, table: t}, nil
}

// Migrate creates the table if it does not exist.
func (s *SQLResetTokenStore) Migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s (email %s PRIMARY KEY, token %s NOT NULL, expires_at BIGINT NOT NULL, created_at BIGINT NOT NULL)",
		s.table, s.dialect.KeyType(), s.dialect.KeyType()))
	return err
}

func (s *SQLResetTokenStore) Put(email, tokenHash string, expiresAt time.Time) error {
	_, err := s.db.ExecContext(context.Background(), s.dialect.Upsert(s.table, "email", "token", "expires_at", "created_at"),
		email, tokenHash, expiresAt.Unix(), time.Now().Unix())
	return err
}

func (s *SQLResetTokenStore) Get(email string) (string, time.Time, bool, error) {
	var tok string
	var exp int64
	err := s.db.QueryRowContext(context.Background(), s.dialect.Rebind(
		fmt.Sprintf("SELECT token, expires_at FROM %s WHERE email = ?", s.table)), email).Scan(&tok, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, false, nil
	}
	if err != nil {
		return "", time.Time{}, false, err
	}
	return tok, time.Unix(exp, 0), true, nil
}

func (s *SQLResetTokenStore) Delete(email string) error {
	_, err := s.db.ExecContext(context.Background(), s.dialect.Rebind(
		fmt.Sprintf("DELETE FROM %s WHERE email = ?", s.table)), email)
	return err
}

// PruneExpired deletes expired tokens (run it from the scheduler).
func (s *SQLResetTokenStore) PruneExpired(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, s.dialect.Rebind(
		fmt.Sprintf("DELETE FROM %s WHERE expires_at < ?", s.table)), time.Now().Unix())
	return err
}
