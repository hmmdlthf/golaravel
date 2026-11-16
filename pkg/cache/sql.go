package cache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/golaravel/golaravel/pkg/database"
)

// SQLOptions configures NewSQLStore.
type SQLOptions struct {
	Table     string // default "cache"
	LockTable string // default "cache_locks"
}

// SQLBackend keeps cache entries and locks in database tables (SQLite,
// MySQL, Postgres). Expired rows are ignored on read and removed by Prune.
type SQLBackend struct {
	db      *sql.DB
	d       database.Dialect
	table   string
	locks   string
	timeout time.Duration
}

// NewSQLStore returns a Store backed by SQL tables; call Migrate first.
func NewSQLStore(db *sql.DB, dialect database.Dialect, opts ...SQLOptions) (*BackendStore, *SQLBackend, error) {
	b := &SQLBackend{db: db, d: dialect, table: "cache", locks: "cache_locks", timeout: 5 * time.Second}
	if len(opts) > 0 {
		if opts[0].Table != "" {
			b.table = opts[0].Table
		}
		if opts[0].LockTable != "" {
			b.locks = opts[0].LockTable
		}
	}
	if !database.ValidIdentifier(b.table) || !database.ValidIdentifier(b.locks) {
		return nil, nil, fmt.Errorf("cache/sql: invalid table name %q / %q", b.table, b.locks)
	}
	return NewBackendStore(b), b, nil
}

func (b *SQLBackend) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), b.timeout)
}

// Migrate creates the cache and lock tables if they do not exist.
func (b *SQLBackend) Migrate(ctx context.Context) error {
	stmts := []string{
		fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (cache_key %s PRIMARY KEY, value %s NOT NULL, expires_at BIGINT NOT NULL)",
			b.table, b.d.KeyType(), b.d.BlobType()),
		fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (name %s PRIMARY KEY, owner %s NOT NULL, expires_at BIGINT NOT NULL)",
			b.locks, b.d.KeyType(), b.d.KeyType()),
	}
	for _, q := range stmts {
		if _, err := b.db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// Prune deletes expired entries and locks.
func (b *SQLBackend) Prune(ctx context.Context) error {
	now := time.Now().UnixMilli()
	if _, err := b.db.ExecContext(ctx, b.d.Rebind(fmt.Sprintf("DELETE FROM %s WHERE expires_at <> 0 AND expires_at <= ?", b.table)), now); err != nil {
		return err
	}
	_, err := b.db.ExecContext(ctx, b.d.Rebind(fmt.Sprintf("DELETE FROM %s WHERE expires_at <> 0 AND expires_at <= ?", b.locks)), now)
	return err
}

func expiresAt(ttl time.Duration) int64 {
	if ttl <= 0 {
		return 0
	}
	return time.Now().Add(ttl).UnixMilli()
}

func live(exp int64) bool { return exp == 0 || exp > time.Now().UnixMilli() }

func (b *SQLBackend) GetRaw(key string) ([]byte, bool, error) {
	ctx, cancel := b.ctx()
	defer cancel()
	var val []byte
	var exp int64
	err := b.db.QueryRowContext(ctx, b.d.Rebind(fmt.Sprintf("SELECT value, expires_at FROM %s WHERE cache_key = ?", b.table)), key).Scan(&val, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !live(exp) {
		return nil, false, nil
	}
	return val, true, nil
}

func (b *SQLBackend) SetRaw(key string, val []byte, ttl time.Duration) error {
	ctx, cancel := b.ctx()
	defer cancel()
	_, err := b.db.ExecContext(ctx, b.d.Upsert(b.table, "cache_key", "value", "expires_at"), key, val, expiresAt(ttl))
	return err
}

func (b *SQLBackend) AddRaw(key string, val []byte, ttl time.Duration) (bool, error) {
	ctx, cancel := b.ctx()
	defer cancel()
	// Drop an expired row first so the insert can claim the key.
	if _, err := b.db.ExecContext(ctx, b.d.Rebind(fmt.Sprintf("DELETE FROM %s WHERE cache_key = ? AND expires_at <> 0 AND expires_at <= ?", b.table)), key, time.Now().UnixMilli()); err != nil {
		return false, err
	}
	res, err := b.db.ExecContext(ctx, b.d.InsertIgnore(b.table, "cache_key", "value", "expires_at"), key, val, expiresAt(ttl))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// IncrBy retries a few times on deadlocks and serialization failures,
// which concurrent increments of one key can cause (MySQL, Postgres) or on
// SQLite's "database is locked".
func (b *SQLBackend) IncrBy(key string, by int64) (int64, error) {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		var n int64
		if n, err = b.incrOnce(key, by); err == nil || !retryable(err) {
			return n, err
		}
		time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
	}
	return 0, err
}

func retryable(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, s := range []string{"deadlock", "40001", "40p01", "could not serialize", "database is locked", "lock wait timeout"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

func (b *SQLBackend) incrOnce(key string, by int64) (int64, error) {
	ctx, cancel := b.ctx()
	defer cancel()
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// Make sure the row exists before locking it: SELECT ... FOR UPDATE
	// locks nothing when the row is missing, so two first increments could
	// both read 0 and one update would be lost (seen on Postgres). A
	// concurrent insert of the same key waits for this transaction.
	if _, err := tx.ExecContext(ctx, b.d.InsertIgnore(b.table, "cache_key", "value", "expires_at"), key, []byte("0"), int64(0)); err != nil {
		return 0, err
	}
	var raw []byte
	var exp int64
	cur := int64(0)
	err = tx.QueryRowContext(ctx, b.d.Rebind(fmt.Sprintf("SELECT value, expires_at FROM %s WHERE cache_key = ?%s", b.table, b.d.ForUpdate())), key).Scan(&raw, &exp)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		exp = 0
	case err != nil:
		return 0, err
	case !live(exp):
		exp = 0
	default:
		if cur, err = strconv.ParseInt(string(raw), 10, 64); err != nil {
			return 0, fmt.Errorf("cache: value at %q is not an integer", key)
		}
	}
	cur += by
	if _, err := tx.ExecContext(ctx, b.d.Upsert(b.table, "cache_key", "value", "expires_at"), key, []byte(strconv.FormatInt(cur, 10)), exp); err != nil {
		return 0, err
	}
	return cur, tx.Commit()
}

func (b *SQLBackend) Delete(key string) error {
	ctx, cancel := b.ctx()
	defer cancel()
	_, err := b.db.ExecContext(ctx, b.d.Rebind(fmt.Sprintf("DELETE FROM %s WHERE cache_key = ?", b.table)), key)
	return err
}

func (b *SQLBackend) Flush() error {
	ctx, cancel := b.ctx()
	defer cancel()
	_, err := b.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s", b.table))
	return err
}

// Acquire takes the lock, or refreshes it when owner already holds it.
func (b *SQLBackend) Acquire(name, owner string, duration time.Duration) (bool, error) {
	ctx, cancel := b.ctx()
	defer cancel()
	now := time.Now().UnixMilli()
	exp := expiresAt(duration)
	// Same owner: refresh.
	res, err := b.db.ExecContext(ctx, b.d.Rebind(fmt.Sprintf("UPDATE %s SET expires_at = ? WHERE name = ? AND owner = ? AND (expires_at = 0 OR expires_at > ?)", b.locks)), exp, name, owner, now)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return true, nil
	}
	if _, err := b.db.ExecContext(ctx, b.d.Rebind(fmt.Sprintf("DELETE FROM %s WHERE name = ? AND expires_at <> 0 AND expires_at <= ?", b.locks)), name, now); err != nil {
		return false, err
	}
	res, err = b.db.ExecContext(ctx, b.d.InsertIgnore(b.locks, "name", "owner", "expires_at"), name, owner, exp)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 1 {
		return n == 1, err
	}
	// MySQL reports 0 affected rows for an UPDATE that changes nothing (a
	// lock without expiry refreshed by its owner), so check the owner.
	var cur string
	var curExp int64
	if err := b.db.QueryRowContext(ctx, b.d.Rebind(fmt.Sprintf("SELECT owner, expires_at FROM %s WHERE name = ?", b.locks)), name).Scan(&cur, &curExp); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return cur == owner && live(curExp), nil
}

// Release frees the lock; ErrLockNotOwned if another owner holds it.
func (b *SQLBackend) Release(name, owner string) error {
	ctx, cancel := b.ctx()
	defer cancel()
	res, err := b.db.ExecContext(ctx, b.d.Rebind(fmt.Sprintf("DELETE FROM %s WHERE name = ? AND owner = ?", b.locks)), name, owner)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}
	var other string
	var exp int64
	err = b.db.QueryRowContext(ctx, b.d.Rebind(fmt.Sprintf("SELECT owner, expires_at FROM %s WHERE name = ?", b.locks)), name).Scan(&other, &exp)
	if err == nil && live(exp) {
		return ErrLockNotOwned
	}
	return nil
}
