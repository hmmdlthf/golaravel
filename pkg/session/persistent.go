package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/golaravel/golaravel/pkg/database"
	"github.com/golaravel/golaravel/pkg/redis"
)

// lifetimeAware drivers learn the session lifetime from the Manager, so it
// is configured in one place.
type lifetimeAware interface {
	setLifetime(d time.Duration)
}

// StartGC runs the driver's GC every interval until ctx is cancelled,
// deleting sessions idle longer than the configured Lifetime. Server-side
// drivers without native expiry (memory, file, SQL) need it; Redis expires
// keys itself.
func (m *Manager) StartGC(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = m.driver.GC(m.cfg.Lifetime)
			}
		}
	}()
}

// --- SQL Driver ---

// SQLDriverOptions configures NewSQLDriver.
type SQLDriverOptions struct {
	// Table name, default "sessions".
	Table string
}

// SQLDriver stores sessions in a database table, so they survive restarts
// and are shared by every instance using the same database. Works with
// SQLite, MySQL and Postgres; create the table with Migrate.
type SQLDriver struct {
	db       *sql.DB
	dialect  database.Dialect
	table    string
	lifetime time.Duration
}

// NewSQLDriver creates a SQL session driver.
func NewSQLDriver(db *sql.DB, dialect database.Dialect, opts ...SQLDriverOptions) (*SQLDriver, error) {
	table := "sessions"
	if len(opts) > 0 && opts[0].Table != "" {
		table = opts[0].Table
	}
	if !database.ValidIdentifier(table) {
		return nil, fmt.Errorf("session/sql: invalid table name %q", table)
	}
	return &SQLDriver{db: db, dialect: dialect, table: table, lifetime: DefaultConfig.Lifetime}, nil
}

func (d *SQLDriver) setLifetime(l time.Duration) { d.lifetime = l }

// Migrate creates the sessions table if it does not exist.
func (d *SQLDriver) Migrate(ctx context.Context) error {
	_, err := d.db.ExecContext(ctx, fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s (id %s PRIMARY KEY, payload %s NOT NULL, last_activity BIGINT NOT NULL)",
		d.table, d.dialect.KeyType(), d.dialect.BlobType()))
	if err != nil {
		return err
	}
	_, err = d.db.ExecContext(ctx, fmt.Sprintf("CREATE INDEX %s%s_last_activity ON %s (last_activity)", ifNotExists(d.dialect), d.table, d.table))
	if err != nil && d.dialect == database.MySQL {
		// MySQL has no CREATE INDEX IF NOT EXISTS; a duplicate index is fine.
		return nil
	}
	return err
}

func ifNotExists(d database.Dialect) string {
	if d == database.MySQL {
		return ""
	}
	return "IF NOT EXISTS "
}

func (d *SQLDriver) Read(id string) ([]byte, error) {
	var payload []byte
	var last int64
	err := d.db.QueryRowContext(context.Background(), d.dialect.Rebind(
		fmt.Sprintf("SELECT payload, last_activity FROM %s WHERE id = ?", d.table)), id).Scan(&payload, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	if d.lifetime > 0 && time.Since(time.Unix(last, 0)) > d.lifetime {
		return nil, ErrSessionNotFound
	}
	return payload, nil
}

func (d *SQLDriver) Write(id string, data []byte) error {
	_, err := d.db.ExecContext(context.Background(), d.dialect.Upsert(d.table, "id", "payload", "last_activity"),
		id, data, time.Now().Unix())
	return err
}

func (d *SQLDriver) Destroy(id string) error {
	_, err := d.db.ExecContext(context.Background(), d.dialect.Rebind(fmt.Sprintf("DELETE FROM %s WHERE id = ?", d.table)), id)
	return err
}

func (d *SQLDriver) GC(maxLifetime time.Duration) error {
	if maxLifetime <= 0 {
		maxLifetime = d.lifetime
	}
	_, err := d.db.ExecContext(context.Background(), d.dialect.Rebind(
		fmt.Sprintf("DELETE FROM %s WHERE last_activity < ?", d.table)), time.Now().Add(-maxLifetime).Unix())
	return err
}

// --- Redis Driver ---

// RedisDriverOptions configures NewRedisDriver.
type RedisDriverOptions struct {
	// Prefix for session keys, default "golaravel:session:".
	Prefix string
}

// RedisDriver stores sessions in Redis with a TTL equal to the session
// lifetime, so they survive restarts, are shared between instances and
// expire on their own.
type RedisDriver struct {
	client   *redis.Client
	prefix   string
	lifetime time.Duration
}

// NewRedisDriver creates a Redis session driver.
func NewRedisDriver(client *redis.Client, opts ...RedisDriverOptions) *RedisDriver {
	prefix := "golaravel:session:"
	if len(opts) > 0 && opts[0].Prefix != "" {
		prefix = opts[0].Prefix
	}
	return &RedisDriver{client: client, prefix: prefix, lifetime: DefaultConfig.Lifetime}
}

func (d *RedisDriver) setLifetime(l time.Duration) { d.lifetime = l }

func (d *RedisDriver) Read(id string) ([]byte, error) {
	v, ok, err := redis.String(d.client.Do("GET", d.prefix+id))
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrSessionNotFound
	}
	return []byte(v), nil
}

func (d *RedisDriver) Write(id string, data []byte) error {
	args := []string{"SET", d.prefix + id, string(data)}
	if d.lifetime > 0 {
		args = append(args, "PX", strconv.FormatInt(d.lifetime.Milliseconds(), 10))
	}
	_, err := d.client.Do(args...)
	return err
}

func (d *RedisDriver) Destroy(id string) error {
	_, err := d.client.Do("DEL", d.prefix+id)
	return err
}

// GC is a no-op: Redis expires sessions itself.
func (d *RedisDriver) GC(time.Duration) error { return nil }
