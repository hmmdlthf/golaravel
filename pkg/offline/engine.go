// Package offline is an offline-first sync engine: clients keep an
// append-only outbox of mutations, push it in batches, and pull the
// entities that changed since their cursor.
//
// The server keeps an append-only ledger of processed mutations. Pushes
// are idempotent (a retried mutation id is acknowledged, not re-applied),
// each mutation (or group of mutations) is applied in its own
// transaction together with its ledger row, and conflicts are settled by
// a per-resource Resolver using hybrid logical clocks.
//
//	eng, _ := offline.New(offline.Options[*ent.Tx]{
//		Begin:   func(ctx context.Context) (*ent.Tx, error) { return client.Tx(ctx) },
//		Dialect: database.SQLite,
//	})
//	eng.Register(offline.Resource[*ent.Tx]{Name: "product", Load: ..., Apply: ...})
//	eng.Routes(api.Group("/sync"))
package offline

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	gcontext "github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/database"
	"github.com/golaravel/golaravel/pkg/privacy"
)

// Class says whether a failed mutation may succeed if pushed again.
type Class int

const (
	Permanent Class = iota // reject; the client moves it to dead letters
	Transient              // retry later; the batch stops here
)

// Options configure an Engine.
type Options[T Tx] struct {
	// Begin starts a transaction. With Ent: client.Tx(ctx).
	Begin func(ctx context.Context) (T, error)
	// Dialect of the database the sync tables live in.
	Dialect database.Dialect
	// Prefix goes in front of the table names ("pos_" gives pos_sync_ledger).
	Prefix string
	// MaxBatch caps mutations per push (default 500).
	MaxBatch int
	// MaxPull caps changes per pull page (default 500).
	MaxPull int
	// MaxDrift is how far ahead of the server a device clock may be before
	// its timestamps are replaced with the server's (default 5 minutes).
	// Without it, one terminal with a wrong clock would win every conflict.
	MaxDrift time.Duration
	// Classify decides whether an Apply error is worth retrying. The
	// default retries connection loss, timeouts, deadlocks and lock
	// contention, and rejects everything else.
	Classify func(error) Class
	// Now replaces time.Now (tests).
	Now func() time.Time
	// Logger defaults to slog.Default().
	Logger *slog.Logger
	// Identify tells the HTTP handlers who is calling. The default takes
	// the tenant from privacy.TenantMiddleware and the user from the auth
	// guards (auth.ContextUserKey), and refuses requests missing either.
	Identify func(c *gcontext.Context) (Principal, error)
	// MaxBody caps push request bodies (default 4 MiB).
	MaxBody int64
}

// Engine processes pushes and pulls for registered resources.
type Engine[T Tx] struct {
	opts      Options[T]
	store     *store
	clock     *Clock
	resources map[string]*resource[T]
	log       *slog.Logger
}

type resource[T Tx] struct {
	Resource[T]
	fields   map[string]bool
	counters map[string]bool
}

// New creates an engine. Call Migrate (or run the Ent schemas from
// offline/schema) to create the tables.
func New[T Tx](opts Options[T]) (*Engine[T], error) {
	if opts.Begin == nil {
		return nil, errors.New("offline: Options.Begin is required")
	}
	if opts.Dialect == "" {
		opts.Dialect = database.SQLite
	}
	if opts.MaxBatch <= 0 {
		opts.MaxBatch = 500
	}
	if opts.MaxPull <= 0 {
		opts.MaxPull = 500
	}
	if opts.MaxDrift <= 0 {
		opts.MaxDrift = 5 * time.Minute
	}
	if opts.Classify == nil {
		opts.Classify = DefaultClassify
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Identify == nil {
		opts.Identify = DefaultIdentify
	}
	if opts.MaxBody <= 0 {
		opts.MaxBody = 4 << 20
	}
	st, err := newStore(opts.Dialect, opts.Prefix)
	if err != nil {
		return nil, err
	}
	return &Engine[T]{opts: opts, store: st, clock: NewClock("server-"+NewID()[16:], opts.Now),
		resources: map[string]*resource[T]{}, log: opts.Logger}, nil
}

// Register adds a syncable resource. Register everything before serving.
func (e *Engine[T]) Register(r Resource[T]) error {
	if !validKey(r.Name) {
		return fmt.Errorf("offline: invalid resource name %q", r.Name)
	}
	if r.Load == nil || r.Apply == nil {
		return fmt.Errorf("offline: resource %q needs Load and Apply", r.Name)
	}
	if _, dup := e.resources[r.Name]; dup {
		return fmt.Errorf("offline: resource %q registered twice", r.Name)
	}
	if r.Resolver == nil {
		r.Resolver = FieldLWW
	}
	res := &resource[T]{Resource: r, counters: map[string]bool{}}
	if len(r.Fields) > 0 {
		res.fields = map[string]bool{}
		for _, f := range r.Fields {
			res.fields[f] = true
		}
	}
	for _, f := range r.Counters {
		res.counters[f] = true
	}
	e.resources[r.Name] = res
	return nil
}

// Migrate creates the sync tables if they do not exist.
func (e *Engine[T]) Migrate(ctx context.Context) error {
	return e.inTx(ctx, func(tx T) error { return e.store.migrate(ctx, tx) })
}

// Clock is the server's hybrid logical clock.
func (e *Engine[T]) Clock() *Clock { return e.clock }

func (e *Engine[T]) now() int64 { return e.opts.Now().UnixMilli() }

func (e *Engine[T]) inTx(ctx context.Context, fn func(tx T) error) (err error) {
	tx, err := e.opts.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// scope puts the tenant on the context, as privacy hooks and scopes expect,
// and marks it so ChangeHook does not record the engine's own writes twice.
func scope(ctx context.Context, p Principal) context.Context {
	return context.WithValue(privacy.WithTenantID(ctx, p.TenantID), applyingKey{}, true)
}

type applyingKey struct{}

// Applying reports whether ctx belongs to a mutation the engine is applying.
func Applying(ctx context.Context) bool { v, _ := ctx.Value(applyingKey{}).(bool); return v }

// ---- devices ---------------------------------------------------------------

// RegisterDevice creates a device for the principal's tenant.
func (e *Engine[T]) RegisterDevice(ctx context.Context, p Principal, info DeviceInfo) (*Device, error) {
	if p.TenantID == "" {
		return nil, ErrNoTenant
	}
	d := &Device{ID: NewID(), TenantID: p.TenantID, UserID: p.UserID, Name: clip(info.Name, 191),
		Platform: clip(info.Platform, 64), AppVersion: clip(info.AppVersion, 64), CreatedAt: e.now()}
	err := e.inTx(ctx, func(tx T) error { return e.store.createDevice(ctx, tx, d) })
	if err != nil {
		return nil, err
	}
	return d, nil
}

// RevokeDevice stops a device from syncing.
func (e *Engine[T]) RevokeDevice(ctx context.Context, tenant, id string) error {
	return e.inTx(ctx, func(tx T) error {
		ok, err := e.store.revokeDevice(ctx, tx, tenant, id, e.now())
		if err == nil && !ok {
			err = ErrUnknownDevice
		}
		return err
	})
}

// Device returns a device of the tenant.
func (e *Engine[T]) Device(ctx context.Context, tenant, id string) (*Device, error) {
	var d *Device
	err := e.inTx(ctx, func(tx T) error {
		var err error
		d, err = e.checkDevice(ctx, tx, tenant, id, false)
		return err
	})
	return d, err
}

func (e *Engine[T]) checkDevice(ctx context.Context, x ExecQuerier, tenant, id string, live bool) (*Device, error) {
	if !validKey(id) {
		return nil, ErrUnknownDevice
	}
	d, err := e.store.device(ctx, x, id)
	if err != nil {
		return nil, err
	}
	if d.TenantID != tenant {
		return nil, ErrUnknownDevice
	}
	if live && d.RevokedAt != 0 {
		return nil, ErrDeviceRevoked
	}
	return d, nil
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// ---- errors ----------------------------------------------------------------

// DefaultClassify retries errors that look like contention or a lost
// connection and rejects the rest.
func DefaultClassify(err error) Class {
	var rej *RejectError
	if errors.As(err, &rej) {
		return Permanent
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
		errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone) || errors.Is(err, sql.ErrTxDone) {
		return Transient
	}
	msg := strings.ToLower(err.Error())
	for _, s := range []string{"deadlock", "database is locked", "database table is locked", "busy",
		"could not serialize", "serialization failure", "lock wait timeout", "connection reset",
		"connection refused", "broken pipe", "too many connections"} {
		if strings.Contains(msg, s) {
			return Transient
		}
	}
	return Permanent
}

// reasonOf is the reason sent to the client. Only RejectError text goes
// out; other errors may carry SQL or internals, so they are logged instead.
func reasonOf(err error) string {
	var rej *RejectError
	if errors.As(err, &rej) {
		return rej.Error()
	}
	return "apply_failed"
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
