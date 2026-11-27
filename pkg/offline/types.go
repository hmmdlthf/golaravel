package offline

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// ExecQuerier runs SQL. *sql.DB, *sql.Tx, and Ent clients and transactions
// generated with the "sql/execquery" feature all satisfy it.
type ExecQuerier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Tx is a database transaction the engine applies mutations in. With Ent,
// enable the "sql/execquery" feature and use your generated *ent.Tx, so
// resources write through Ent (hooks and privacy rules included) in the
// same transaction as the sync ledger.
type Tx interface {
	ExecQuerier
	Commit() error
	Rollback() error
}

// SQLTx adapts *sql.Tx to Tx, for apps that do not use Ent.
type SQLTx struct{ *sql.Tx }

// BeginSQL returns a Begin function for Options backed by a plain *sql.DB.
func BeginSQL(db *sql.DB) func(context.Context) (SQLTx, error) {
	return func(ctx context.Context) (SQLTx, error) {
		tx, err := db.BeginTx(ctx, nil)
		return SQLTx{tx}, err
	}
}

// Principal is who is syncing: the tenant whose data it is and the user
// signed in on the device.
type Principal struct {
	TenantID string
	UserID   string
}

// Op is what a mutation does to its entity.
type Op string

const (
	OpCreate Op = "create"
	OpUpdate Op = "update"
	OpDelete Op = "delete"
)

func (o Op) valid() bool { return o == OpCreate || o == OpUpdate || o == OpDelete }

// Mutation is one entry of a client's outbox.
type Mutation struct {
	ID       string `json:"id"`        // client-generated ULID or UUIDv7
	Entity   string `json:"entity"`    // registered resource name
	EntityID string `json:"entity_id"` // client-generated for creates
	Op       Op     `json:"op"`
	// Set holds field values to write (create, update).
	Set map[string]json.RawMessage `json:"set,omitempty"`
	// Inc holds deltas for counter fields (stock: -2). Deltas from
	// different devices add up instead of overwriting each other.
	Inc map[string]float64 `json:"inc,omitempty"`
	// HLC is the client's clock when the change was made.
	HLC string `json:"hlc"`
	// Group ties mutations that must apply together (a sale, its lines and
	// the stock changes). Members must be adjacent in the batch.
	Group string `json:"group,omitempty"`
	// BaseVersion is the entity version the client last saw.
	BaseVersion int64 `json:"base_version,omitempty"`
	// At is the client's wall time (Unix ms), for auditing.
	At int64 `json:"at,omitempty"`
}

// Record is an entity's current state, as a resource loads it.
type Record struct {
	ID          string            `json:"id"`
	Version     int64             `json:"version"`
	HLC         string            `json:"hlc,omitempty"`
	FieldClocks map[string]string `json:"-"`
	Deleted     bool              `json:"deleted,omitempty"`
	// Data is what clients receive; anything that encodes to JSON.
	Data any `json:"data,omitempty"`
}

// Write is a resolved change for a resource's Apply to carry out.
type Write struct {
	// Op is OpCreate when the entity does not exist or is a tombstone
	// (restore it), OpUpdate when it exists, OpDelete to delete it.
	Op       Op
	EntityID string
	Set      map[string]json.RawMessage
	Inc      map[string]float64
	// Version, HLC and FieldClocks are the values to store on the row.
	Version     int64
	HLC         string
	FieldClocks map[string]string
	Current     *Record // nil when the entity does not exist
	Mutation    *Mutation
	Principal   Principal
}

// Decode unmarshals the Set fields into dst (a struct pointer or map).
func (w *Write) Decode(dst any) error {
	b, err := json.Marshal(w.Set)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

// Has reports whether field is being set.
func (w *Write) Has(field string) bool { _, ok := w.Set[field]; return ok }

// Resource describes an entity type that clients sync.
type Resource[T Tx] struct {
	// Name is the entity name clients use ("product").
	Name string
	// Load returns the entity, including soft-deleted rows as tombstones
	// (Deleted: true), or nil when it does not exist. The context carries
	// the tenant (privacy.WithTenantID).
	Load func(ctx context.Context, tx T, id string) (*Record, error)
	// LoadMany is an optional batch form of Load used by pulls; missing
	// ids mean the entity is gone.
	LoadMany func(ctx context.Context, tx T, ids []string) (map[string]*Record, error)
	// Apply writes the change. Return Reject(err) for permanent failures
	// (validation); other errors are classified by Options.Classify.
	Apply func(ctx context.Context, tx T, w *Write) error
	// Snapshot lists live entities ordered by id, after the given id, for
	// first-time hydration and resyncs. Optional.
	Snapshot func(ctx context.Context, tx T, after string, limit int) ([]*Record, error)
	// Resolver settles concurrent edits. Defaults to FieldLWW.
	Resolver Resolver
	// Fields, when set, limits which fields clients may set.
	Fields []string
	// Counters lists the fields clients may change with Inc.
	Counters []string
	// Authorize may veto a mutation (return an error to reject it).
	Authorize func(ctx context.Context, p Principal, m *Mutation, current *Record) error
}

// Status is the outcome of one pushed mutation.
type Status string

const (
	StatusApplied   Status = "applied"   // accepted as sent
	StatusResolved  Status = "resolved"  // accepted after conflict resolution
	StatusDuplicate Status = "duplicate" // seen before; Original says how it went
	StatusRejected  Status = "rejected"  // permanent failure; move to dead letters
	StatusRetry     Status = "retry"     // transient failure; push again later
)

// Result is the server's answer for one mutation.
type Result struct {
	ID       string  `json:"id"`
	Status   Status  `json:"status"`
	Original Status  `json:"original,omitempty"` // for duplicates
	Seq      int64   `json:"seq,omitempty"`
	Reason   string  `json:"reason,omitempty"`
	Record   *Record `json:"record,omitempty"` // server state after resolved/rejected
	Entity   string  `json:"entity,omitempty"`
	EntityID string  `json:"entity_id,omitempty"`
}

// PushRequest is a batch of outbox entries, oldest first.
type PushRequest struct {
	DeviceID  string     `json:"device_id"`
	BatchID   string     `json:"batch_id,omitempty"`
	Mutations []Mutation `json:"mutations"`
}

// PushResponse acknowledges a batch, one Result per mutation in order.
type PushResponse struct {
	Results   []Result `json:"results"`
	ServerHLC string   `json:"server_hlc"`
}

// Change is one entity in a pull: its state now, or a tombstone.
type Change struct {
	Entity   string `json:"entity"`
	EntityID string `json:"entity_id"`
	Seq      int64  `json:"seq"`
	Deleted  bool   `json:"deleted,omitempty"`
	Version  int64  `json:"version,omitempty"`
	HLC      string `json:"hlc,omitempty"`
	Data     any    `json:"data,omitempty"`
}

// PullResponse carries changes after a cursor.
type PullResponse struct {
	Changes   []Change `json:"changes"`
	Cursor    int64    `json:"cursor"`
	HasMore   bool     `json:"has_more"`
	ServerHLC string   `json:"server_hlc"`
}

// SnapshotResponse is one page of an entity's live records.
type SnapshotResponse struct {
	Entity  string    `json:"entity"`
	Records []*Record `json:"records"`
	Next    string    `json:"next,omitempty"`
	HasMore bool      `json:"has_more"`
	// Cursor is where to start pulling once every page is loaded. It is
	// only set on the first page (after == "").
	Cursor int64 `json:"cursor,omitempty"`
}

// Device is a registered client installation.
type Device struct {
	ID         string `json:"id"`
	TenantID   string `json:"tenant_id"`
	UserID     string `json:"user_id,omitempty"`
	Name       string `json:"name,omitempty"`
	Platform   string `json:"platform,omitempty"`
	AppVersion string `json:"app_version,omitempty"`
	Cursor     int64  `json:"cursor"`
	CreatedAt  int64  `json:"created_at"`
	LastPushAt int64  `json:"last_push_at,omitempty"`
	LastPullAt int64  `json:"last_pull_at,omitempty"`
	RevokedAt  int64  `json:"revoked_at,omitempty"`
	// Encrypted is set when the device registered a key; its request
	// bodies must then be envelopes and every response is one.
	Encrypted bool `json:"encrypted"`
	// ServerKey is returned once, at registration, for key derivation.
	ServerKey *ServerPublicKey `json:"server_key,omitempty"`
	// PublicKey is the stored key material (server key id + device key).
	PublicKey string `json:"-"`
}

// DeviceInfo describes a device being registered.
type DeviceInfo struct {
	Name       string `json:"name"`
	Platform   string `json:"platform"`
	AppVersion string `json:"app_version"`
	// PublicKey is the device's ECDH P-256 public key, base64url raw,
	// to turn on payload encryption.
	PublicKey string `json:"public_key,omitempty"`
}

var (
	ErrUnknownDevice  = errors.New("offline: unknown device")
	ErrDeviceRevoked  = errors.New("offline: device revoked")
	ErrResyncRequired = errors.New("offline: cursor is older than the retained history; resync")
	ErrUnknownEntity  = errors.New("offline: unknown entity")
	ErrBatchTooLarge  = errors.New("offline: batch too large")
	ErrNoTenant       = errors.New("offline: no tenant")
	ErrUnauthorized   = errors.New("offline: not signed in")
)

// RejectError marks a permanent failure: the mutation will never succeed,
// so the client should stop retrying it.
type RejectError struct {
	Code string
	Err  error
}

func (e *RejectError) Error() string {
	if e.Err == nil {
		return e.Code
	}
	return e.Code + ": " + e.Err.Error()
}

func (e *RejectError) Unwrap() error { return e.Err }

// Reject wraps err as a permanent failure with a short machine code.
func Reject(code string, err error) error { return &RejectError{Code: code, Err: err} }

// Rejectf is Reject with a formatted message.
func Rejectf(code, format string, args ...any) error {
	return &RejectError{Code: code, Err: fmt.Errorf(format, args...)}
}
