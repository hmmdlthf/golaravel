package features

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/golaravel/golaravel/pkg/cache"
	"github.com/golaravel/golaravel/pkg/database"
	"github.com/golaravel/golaravel/pkg/events"
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
	// Cache, when set, keeps each tenant's values so reads skip the
	// database. Writes invalidate it, also across instances sharing a SQL or
	// Redis cache. Optional.
	Cache cache.Store
	// CacheTTL bounds how long a cached tenant entry lives. Defaults to one
	// hour; it only matters if an invalidation is lost (cache down during a
	// write).
	CacheTTL time.Duration
	// Events receives a Changed event after every Set and Reset. Defaults
	// to the global dispatcher in pkg/events.
	Events Dispatcher
	// Now defaults to time.Now.
	Now func() time.Time
	// Logger defaults to slog.Default().
	Logger *slog.Logger
}

// Manager holds the registered features and resolves their values per
// tenant.
type Manager struct {
	store       backend
	cache       cache.Store
	cacheTTL    time.Duration
	cachePrefix string
	events      Dispatcher
	actor       func(ctx context.Context) string
	now         func() time.Time
	log         *slog.Logger

	mu   sync.RWMutex
	defs map[string]Definition
}

// New returns a Manager. Call Migrate (or create the table with the Ent
// schema in features/schema) and Register before use.
func New(opts Options) (*Manager, error) {
	if opts.DB == nil {
		return nil, fmt.Errorf("features: Options.DB is required")
	}
	return newManager(opts, nil)
}

// NewInMemory returns a Manager that keeps values in memory instead of a
// database, for tests and tools. Options.DB, Dialect and Table are ignored.
func NewInMemory(opts Options) *Manager {
	m, _ := newManager(opts, &memStore{vals: map[string]map[string]string{}})
	return m
}

func newManager(opts Options, b backend) (*Manager, error) {
	if opts.Dialect == "" {
		opts.Dialect = database.SQLite
	}
	if opts.Table == "" {
		opts.Table = "tenant_settings"
	}
	if b == nil {
		if !database.ValidIdentifier(opts.Table) {
			return nil, fmt.Errorf("features: invalid table name %q", opts.Table)
		}
		b = &sqlStore{db: opts.DB, d: opts.Dialect, table: opts.Table}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.CacheTTL <= 0 {
		opts.CacheTTL = time.Hour
	}
	if opts.Events == nil {
		opts.Events = globalEvents{}
	}
	return &Manager{
		store:       b,
		cache:       opts.Cache,
		cacheTTL:    opts.CacheTTL,
		cachePrefix: "features:" + opts.Table + ":",
		events:      opts.Events,
		actor:       opts.Actor,
		now:         opts.Now,
		log:         opts.Logger,
		defs:        map[string]Definition{},
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
	vals, err := m.values(ctx)
	if err != nil {
		return nil, err
	}
	metas := m.Definitions()
	out := make([]Resolved, 0, len(metas))
	for _, meta := range metas {
		v, over := m.pick(ctx, m.lookup(meta.Key), vals)
		out = append(out, Resolved{Meta: meta, Value: v, Overridden: over})
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
	return m.write(ctx, d, raw)
}

// Reset removes the active tenant's value for key.
func (m *Manager) Reset(ctx context.Context, key string) error {
	d, err := m.definition(key)
	if err != nil {
		return err
	}
	return m.write(ctx, d, nil)
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
	if raw, ok, err := m.fake(ctx, d); err != nil || ok {
		if err != nil {
			return d.defaultValue(), false, err
		}
		v, over := m.pick(ctx, d, map[string]string{d.Meta().Key: raw})
		return v, over, nil
	}
	vals, err := m.storedValues(ctx)
	if err != nil {
		return d.defaultValue(), false, err
	}
	v, over := m.pick(ctx, d, vals)
	return v, over, nil
}

// pick resolves d from a tenant's stored values.
func (m *Manager) pick(ctx context.Context, d Definition, vals map[string]string) (any, bool) {
	key := d.Meta().Key
	raw, ok := vals[key]
	if !ok {
		return d.defaultValue(), false
	}
	v, err := d.decode([]byte(raw))
	if err != nil {
		m.warnInvalid(ctx, key, err)
		return d.defaultValue(), false
	}
	return v, true
}

// write stores raw as the active tenant's value for d, or deletes it when
// raw is nil, then invalidates caches and dispatches Changed.
func (m *Manager) write(ctx context.Context, d Definition, raw []byte) error {
	t, err := writeTenant(ctx)
	if err != nil {
		return err
	}
	key := d.Meta().Key
	old, oldOver, err := m.resolve(ctx, d)
	if err != nil {
		return err
	}
	var by string
	if m.actor != nil {
		by = m.actor(ctx)
	}
	if raw == nil {
		err = m.store.delete(ctx, t, key)
	} else {
		var byArg any
		if by != "" {
			byArg = by
		}
		err = m.store.upsert(ctx, t, key, string(raw), byArg, m.now().UnixMilli())
	}
	// Invalidate even on error: the write may have reached the database.
	m.invalidate(ctx, t)
	if err != nil {
		return err
	}
	ev := Changed{Tenant: t, Key: key, Old: old, WasOverridden: oldOver, New: d.defaultValue(), By: by, At: m.now()}
	if raw != nil {
		ev.New, ev.Overridden = m.pick(ctx, d, map[string]string{key: string(raw)})
	}
	if err := m.events.Dispatch(EventChanged, ev); err != nil {
		// The value is already stored; a failing listener does not undo it.
		m.log.ErrorContext(ctx, "features: Changed listener failed", "key", key, "tenant", t, "error", err)
	}
	return nil
}

func (m *Manager) warnInvalid(ctx context.Context, key string, err error) {
	tenant, _ := privacy.TenantFromContext(ctx)
	m.log.WarnContext(ctx, "features: stored value is invalid, using default",
		"key", key, "tenant", tenant, "error", err)
}

// EventChanged is the event name Changed is dispatched under.
const EventChanged = "features.changed"

// Changed is dispatched after a tenant's value is set or reset.
type Changed struct {
	Tenant string
	Key    string
	// Old and New are the resolved values before and after (the default
	// when not overridden).
	Old, New any
	// WasOverridden and Overridden say whether the tenant had its own value
	// before and has one after.
	WasOverridden, Overridden bool
	// By is the actor (Options.Actor), if any.
	By string
	At time.Time
}

// Dispatcher receives events. *events.Dispatcher satisfies it.
type Dispatcher interface {
	Dispatch(eventName string, payload any) error
}

type globalEvents struct{}

func (globalEvents) Dispatch(name string, payload any) error { return events.Dispatch(name, payload) }
