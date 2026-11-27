package features

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/golaravel/golaravel/pkg/database"
	"github.com/golaravel/golaravel/pkg/privacy"
)

// ExecQuerier runs SQL. *sql.DB, *sql.Tx, and Ent clients generated with
// the "sql/execquery" feature all satisfy it.
type ExecQuerier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Options configure a Manager.
type Options struct {
	// DB holds the tenant_settings table. Required.
	DB ExecQuerier
	// Dialect of DB. Defaults to SQLite.
	Dialect database.Dialect
	// Table name. Defaults to "tenant_settings".
	Table string
	// Actor returns the user making a change, stored as updated_by.
	// Optional.
	Actor func(ctx context.Context) string
	// Now defaults to time.Now.
	Now func() time.Time
	// Logger defaults to slog.Default().
	Logger *slog.Logger
}

// Manager holds the registered features and resolves their values per
// tenant.
type Manager struct {
	store *store
	actor func(ctx context.Context) string
	now   func() time.Time
	log   *slog.Logger

	mu   sync.RWMutex
	defs map[string]Definition
}

// New returns a Manager. Call Migrate (or create the table with the Ent
// schema in features/schema) and Register before use.
func New(opts Options) (*Manager, error) {
	if opts.DB == nil {
		return nil, fmt.Errorf("features: Options.DB is required")
	}
	if opts.Dialect == "" {
		opts.Dialect = database.SQLite
	}
	if opts.Table == "" {
		opts.Table = "tenant_settings"
	}
	if !database.ValidIdentifier(opts.Table) {
		return nil, fmt.Errorf("features: invalid table name %q", opts.Table)
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Manager{
		store: &store{db: opts.DB, d: opts.Dialect, table: opts.Table},
		actor: opts.Actor,
		now:   opts.Now,
		log:   opts.Logger,
		defs:  map[string]Definition{},
	}, nil
}

// Migrate creates the tenant_settings table if it does not exist.
func (m *Manager) Migrate(ctx context.Context) error { return m.store.migrate(ctx) }

// Register adds features. It fails on an invalid key or default, on a key
// registered twice, and on a feature already bound to another Manager.
func (m *Manager) Register(defs ...Definition) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range defs {
		if err := d.checkDefault(); err != nil {
			return err
		}
		key := d.Meta().Key
		if prev, ok := m.defs[key]; ok && prev != d {
			return fmt.Errorf("features: %s is registered twice", key)
		}
	}
	for _, d := range defs {
		if err := d.bind(m); err != nil {
			return err
		}
		m.defs[d.Meta().Key] = d
	}
	return nil
}

// Definitions lists the registered features, sorted by group then key.
func (m *Manager) Definitions() []Meta {
	m.mu.RLock()
	out := make([]Meta, 0, len(m.defs))
	for _, d := range m.defs {
		out = append(out, d.Meta())
	}
	m.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// Resolved is a feature with the active tenant's value.
type Resolved struct {
	Meta
	Value any `json:"value"`
	// Overridden is true when the tenant has its own valid value.
	Overridden bool `json:"overridden"`
}

// All returns every registered feature with the active tenant's value, in
// the order of Definitions. It reads the tenant's values in one query.
func (m *Manager) All(ctx context.Context) ([]Resolved, error) {
	rows, err := m.store.all(ctx)
	if err != nil {
		return nil, err
	}
	metas := m.Definitions()
	out := make([]Resolved, 0, len(metas))
	for _, meta := range metas {
		d := m.lookup(meta.Key)
		r := Resolved{Meta: meta, Value: d.defaultValue()}
		if raw, ok := rows[meta.Key]; ok {
			if v, err := d.decode(raw); err == nil {
				r.Value, r.Overridden = v, true
			} else {
				m.warnInvalid(ctx, meta.Key, err)
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// Public is All limited to features marked Public, as a key → value map
// for frontends and clients.
func (m *Manager) Public(ctx context.Context) (map[string]any, error) {
	all, err := m.All(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for _, r := range all {
		if r.Public {
			out[r.Key] = r.Value
		}
	}
	return out, nil
}

// Get returns the active tenant's value for key, untyped.
func (m *Manager) Get(ctx context.Context, key string) (any, error) {
	d, err := m.definition(key)
	if err != nil {
		return nil, err
	}
	v, _, err := m.resolve(ctx, d)
	return v, err
}

// Set stores an untyped value for key, as from a settings form or the
// CLI. Strings are parsed for non-string types ("true", "5", `{"a":1}`).
func (m *Manager) Set(ctx context.Context, key string, value any) error {
	d, err := m.definition(key)
	if err != nil {
		return err
	}
	raw, err := d.encode(value)
	if err != nil {
		return err
	}
	return m.store.set(ctx, m, key, raw)
}

// Reset removes the active tenant's value for key.
func (m *Manager) Reset(ctx context.Context, key string) error {
	if _, err := m.definition(key); err != nil {
		return err
	}
	return m.store.reset(ctx, m, key)
}

// ForTenant returns ctx scoped to tenantID, for reading and changing
// another tenant's features from a central (super admin) context.
func ForTenant(ctx context.Context, tenantID string) context.Context {
	return privacy.WithTenantID(ctx, tenantID)
}

func (m *Manager) lookup(key string) Definition {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.defs[key]
}

func (m *Manager) definition(key string) (Definition, error) {
	if d := m.lookup(key); d != nil {
		return d, nil
	}
	return nil, fmt.Errorf("%w: %s", ErrUnknownFeature, key)
}

// resolve returns the tenant's value for d and whether it is an override.
// An invalid stored value falls back to the default.
func (m *Manager) resolve(ctx context.Context, d Definition) (any, bool, error) {
	key := d.Meta().Key
	raw, ok, err := m.store.get(ctx, key)
	if err != nil || !ok {
		return d.defaultValue(), false, err
	}
	v, err := d.decode(raw)
	if err != nil {
		m.warnInvalid(ctx, key, err)
		return d.defaultValue(), false, nil
	}
	return v, true, nil
}

func (m *Manager) warnInvalid(ctx context.Context, key string, err error) {
	tenant, _ := privacy.TenantFromContext(ctx)
	m.log.WarnContext(ctx, "features: stored value is invalid, using default",
		"key", key, "tenant", tenant, "error", err)
}
