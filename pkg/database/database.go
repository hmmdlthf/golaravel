package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Config holds high-performance database connection pool settings.
type Config struct {
	Driver          string        `json:"driver"`           // "sqlite3", "postgres", "mysql"
	DSN             string        `json:"dsn"`              // Data source name
	MaxOpenConns    int           `json:"max_open_conns"`    // High-concurrency open connections limit
	MaxIdleConns    int           `json:"max_idle_conns"`    // Idle connections kept warm in the pool
	ConnMaxLifetime time.Duration `json:"conn_max_lifetime"` // Connection lifetime
	ConnMaxIdleTime time.Duration `json:"conn_max_idle_time"` // Idle connection eviction timeout
}

// DefaultConfig provides high-throughput connection pool defaults.
func DefaultConfig() Config {
	return Config{
		Driver:          "sqlite3",
		DSN:             ":memory:?_fk=1",
		MaxOpenConns:    100,
		MaxIdleConns:    25,
		ConnMaxLifetime: 15 * time.Minute,
		ConnMaxIdleTime: 5 * time.Minute,
	}
}

// OpenDB creates and optimizes an sql.DB connection pool for extreme throughput.
func OpenDB(cfg Config) (*sql.DB, error) {
	db, err := sql.Open(cfg.Driver, cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("database: failed to open connection: %w", err)
	}

	// Apply connection pool optimizations
	if cfg.MaxOpenConns > 0 {
		db.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		db.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	}
	if cfg.ConnMaxIdleTime > 0 {
		db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
	}

	return db, nil
}

// TxFunc defines the callback executed within a database transaction.
type TxFunc func(ctx context.Context, tx *sql.Tx) error

// WithTx executes the given function inside a database transaction with automated rollback on error.
func WithTx(ctx context.Context, db *sql.DB, fn TxFunc) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("database: failed to begin transaction: %w", err)
	}

	defer func() {
		if r := recover(); r != nil {
			_ = tx.Rollback()
			panic(r)
		} else if err != nil {
			_ = tx.Rollback()
		} else {
			err = tx.Commit()
		}
	}()

	err = fn(ctx, tx)
	return err
}
