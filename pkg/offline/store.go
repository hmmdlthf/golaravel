package offline

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/golaravel/golaravel/pkg/database"
)

// store reads and writes the sync tables. Every method takes the
// ExecQuerier to run on, so writes join the caller's transaction.
type store struct {
	d       database.Dialect
	ledger  string
	devices string
	tenants string
}

func newStore(d database.Dialect, prefix string) (*store, error) {
	s := &store{d: d, ledger: prefix + "sync_ledger", devices: prefix + "sync_devices", tenants: prefix + "sync_tenants"}
	for _, t := range []string{s.ledger, s.devices, s.tenants} {
		if !database.ValidIdentifier(t) {
			return nil, fmt.Errorf("offline: invalid table name %q", t)
		}
	}
	return s, nil
}

func (s *store) q(query string) string { return s.d.Rebind(query) }

func (s *store) migrate(ctx context.Context, x ExecQuerier) error {
	k := s.d.KeyType()
	text := "TEXT"
	if s.d == database.MySQL {
		text = "LONGTEXT"
	}
	stmts := []string{
		// id is tenant_id|mutation_id: idempotency is per tenant.
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			id %s PRIMARY KEY, tenant_id %s NOT NULL, mutation_id %s NOT NULL, seq BIGINT NOT NULL DEFAULT 0,
			device_id %s NOT NULL, user_id %s, entity %s NOT NULL, entity_id %s NOT NULL, op %s NOT NULL,
			hlc %s, group_id %s, status %s NOT NULL, reason %s, payload %s, created_at BIGINT NOT NULL)`,
			s.ledger, k, k, k, k, k, k, k, k, k, k, k, text, text),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			id %s PRIMARY KEY, tenant_id %s NOT NULL, user_id %s, name %s, platform %s, app_version %s,
			public_key %s, cursor_seq BIGINT NOT NULL DEFAULT 0, created_at BIGINT NOT NULL,
			last_push_at BIGINT, last_pull_at BIGINT, revoked_at BIGINT)`,
			s.devices, k, k, k, k, k, k, text),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			id %s PRIMARY KEY, seq BIGINT NOT NULL DEFAULT 0, horizon BIGINT NOT NULL DEFAULT 0)`, s.tenants, k),
	}
	for _, st := range stmts {
		if _, err := x.ExecContext(ctx, st); err != nil {
			return err
		}
	}
	idx := [][3]string{
		{s.ledger, "seq", "tenant_id, seq"},
		{s.ledger, "entity", "tenant_id, entity, entity_id"},
		{s.devices, "tenant", "tenant_id"},
	}
	for _, i := range idx {
		name := i[0] + "_" + i[1] + "_idx"
		var st string
		if s.d == database.MySQL {
			// MySQL has no CREATE INDEX IF NOT EXISTS.
			var n int
			row := queryRow(ctx, x, s.q(`SELECT COUNT(*) FROM information_schema.statistics
				WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ?`), i[0], name)
			if err := row(&n); err != nil {
				return err
			}
			if n > 0 {
				continue
			}
			st = fmt.Sprintf("CREATE INDEX %s ON %s (%s)", name, i[0], i[2])
		} else {
			st = fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s (%s)", name, i[0], i[2])
		}
		if _, err := x.ExecContext(ctx, st); err != nil {
			return err
		}
	}
	return nil
}

// queryRow runs a single-row query; ExecQuerier has no QueryRowContext.
func queryRow(ctx context.Context, x ExecQuerier, query string, args ...any) func(dest ...any) error {
	return func(dest ...any) error {
		rows, err := x.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		if !rows.Next() {
			if err := rows.Err(); err != nil {
				return err
			}
			return sql.ErrNoRows
		}
		if err := rows.Scan(dest...); err != nil {
			return err
		}
		return rows.Close()
	}
}

// lockTenant takes the tenant's sequence row lock for the rest of the
// transaction, creating the row on first use. Everything that writes the
// ledger for a tenant holds this lock, so sequence numbers commit in
// order and a pull never skips a row that commits late.
func (s *store) lockTenant(ctx context.Context, x ExecQuerier, tenant string) error {
	res, err := x.ExecContext(ctx, s.q("UPDATE "+s.tenants+" SET seq = seq WHERE id = ?"), tenant)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		return nil
	}
	// No row yet (or MySQL, which reports unchanged rows as unaffected
	// though it still locks them). Create it and lock it explicitly.
	if _, err := x.ExecContext(ctx, s.d.InsertIgnore(s.tenants, "id", "seq", "horizon"), tenant, 0, 0); err != nil {
		return err
	}
	var seq int64
	return queryRow(ctx, x, s.q("SELECT seq FROM "+s.tenants+" WHERE id = ?"+s.d.ForUpdate()), tenant)(&seq)
}

// nextSeq bumps and returns the tenant's sequence. Call lockTenant first.
func (s *store) nextSeq(ctx context.Context, x ExecQuerier, tenant string) (int64, error) {
	if _, err := x.ExecContext(ctx, s.q("UPDATE "+s.tenants+" SET seq = seq + 1 WHERE id = ?"), tenant); err != nil {
		return 0, err
	}
	var seq int64
	err := queryRow(ctx, x, s.q("SELECT seq FROM "+s.tenants+" WHERE id = ?"), tenant)(&seq)
	return seq, err
}

// tenantState returns the tenant's current sequence and history horizon.
func (s *store) tenantState(ctx context.Context, x ExecQuerier, tenant string) (seq, horizon int64, err error) {
	err = queryRow(ctx, x, s.q("SELECT seq, horizon FROM "+s.tenants+" WHERE id = ?"), tenant)(&seq, &horizon)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, nil
	}
	return
}

type ledgerRow struct {
	TenantID, MutationID, DeviceID, UserID, Entity, EntityID string
	Op                                                       Op
	HLC, Group                                               string
	Status                                                   Status
	Reason, Payload                                          string
	Seq, CreatedAt                                           int64
}

var ledgerCols = []string{"id", "tenant_id", "mutation_id", "seq", "device_id", "user_id", "entity", "entity_id",
	"op", "hlc", "group_id", "status", "reason", "payload", "created_at"}

func (r *ledgerRow) args() []any {
	return []any{r.TenantID + "|" + r.MutationID, r.TenantID, r.MutationID, r.Seq, r.DeviceID, r.UserID,
		r.Entity, r.EntityID, string(r.Op), r.HLC, r.Group, string(r.Status), r.Reason, r.Payload, r.CreatedAt}
}

func (s *store) insertLedger(ctx context.Context, x ExecQuerier, r *ledgerRow) error {
	ph := strings.TrimSuffix(strings.Repeat("?, ", len(ledgerCols)), ", ")
	_, err := x.ExecContext(ctx, s.q("INSERT INTO "+s.ledger+" ("+strings.Join(ledgerCols, ", ")+") VALUES ("+ph+")"), r.args()...)
	return err
}

// insertLedgerIgnore records a row unless the mutation is already known.
func (s *store) insertLedgerIgnore(ctx context.Context, x ExecQuerier, r *ledgerRow) error {
	_, err := x.ExecContext(ctx, s.d.InsertIgnore(s.ledger, ledgerCols...), r.args()...)
	return err
}

// processed returns the stored outcome of a mutation, or nil.
func (s *store) processed(ctx context.Context, x ExecQuerier, tenant, mutationID string) (*ledgerRow, error) {
	r := &ledgerRow{TenantID: tenant, MutationID: mutationID}
	var status string
	var reason sql.NullString
	err := queryRow(ctx, x, s.q("SELECT status, seq, reason, entity, entity_id FROM "+s.ledger+" WHERE id = ?"),
		tenant+"|"+mutationID)(&status, &r.Seq, &reason, &r.Entity, &r.EntityID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.Status, r.Reason = Status(status), reason.String
	return r, nil
}

type changedRow struct {
	Entity, EntityID string
	Seq              int64
}

// changes lists entities changed after cursor, once each at their latest
// sequence, oldest first.
func (s *store) changes(ctx context.Context, x ExecQuerier, tenant string, cursor int64, limit int) ([]changedRow, error) {
	rows, err := x.QueryContext(ctx, s.q(`SELECT entity, entity_id, MAX(seq) AS m FROM `+s.ledger+`
		WHERE tenant_id = ? AND seq > ? GROUP BY entity, entity_id ORDER BY m LIMIT ?`), tenant, cursor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []changedRow
	for rows.Next() {
		var c changedRow
		if err := rows.Scan(&c.Entity, &c.EntityID, &c.Seq); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

var deviceCols = "id, tenant_id, user_id, name, platform, app_version, public_key, cursor_seq, created_at, last_push_at, last_pull_at, revoked_at"

func (s *store) createDevice(ctx context.Context, x ExecQuerier, d *Device) error {
	var pub any
	if d.PublicKey != "" {
		pub = d.PublicKey
	}
	_, err := x.ExecContext(ctx, s.q("INSERT INTO "+s.devices+" ("+deviceCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL, NULL)"),
		d.ID, d.TenantID, d.UserID, d.Name, d.Platform, d.AppVersion, pub, d.Cursor, d.CreatedAt)
	return err
}

func scanDevice(sc func(dest ...any) error) (*Device, error) {
	d := &Device{}
	var user, name, platform, ver, pub sql.NullString
	var push, pull, revoked sql.NullInt64
	if err := sc(&d.ID, &d.TenantID, &user, &name, &platform, &ver, &pub, &d.Cursor, &d.CreatedAt, &push, &pull, &revoked); err != nil {
		return nil, err
	}
	d.UserID, d.Name, d.Platform, d.AppVersion, d.PublicKey = user.String, name.String, platform.String, ver.String, pub.String
	d.LastPushAt, d.LastPullAt, d.RevokedAt = push.Int64, pull.Int64, revoked.Int64
	d.Encrypted = d.PublicKey != ""
	return d, nil
}

func (s *store) device(ctx context.Context, x ExecQuerier, id string) (*Device, error) {
	d, err := scanDevice(queryRow(ctx, x, s.q("SELECT "+deviceCols+" FROM "+s.devices+" WHERE id = ?"), id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUnknownDevice
	}
	return d, err
}

// listDevices returns a tenant's devices, newest first.
func (s *store) listDevices(ctx context.Context, x ExecQuerier, tenant string) ([]*Device, error) {
	rows, err := x.QueryContext(ctx, s.q("SELECT "+deviceCols+" FROM "+s.devices+" WHERE tenant_id = ? ORDER BY created_at DESC, id DESC"), tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Device
	for rows.Next() {
		d, err := scanDevice(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *store) touchPush(ctx context.Context, x ExecQuerier, id string, at int64) error {
	_, err := x.ExecContext(ctx, s.q("UPDATE "+s.devices+" SET last_push_at = ? WHERE id = ?"), at, id)
	return err
}

func (s *store) touchPull(ctx context.Context, x ExecQuerier, id string, cursor, at int64) error {
	_, err := x.ExecContext(ctx, s.q("UPDATE "+s.devices+" SET last_pull_at = ?, cursor_seq = ? WHERE id = ? AND cursor_seq <= ?"),
		at, cursor, id, cursor)
	return err
}

func (s *store) revokeDevice(ctx context.Context, x ExecQuerier, tenant, id string, at int64) (bool, error) {
	res, err := x.ExecContext(ctx, s.q("UPDATE "+s.devices+" SET revoked_at = ?, public_key = NULL WHERE id = ? AND tenant_id = ? AND revoked_at IS NULL"),
		at, id, tenant)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *store) tenantIDs(ctx context.Context, x ExecQuerier) ([]string, error) {
	rows, err := x.QueryContext(ctx, "SELECT id FROM "+s.tenants+" ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// minLiveCursor is the oldest cursor among a tenant's active devices (not
// revoked, seen since activeSince). ok is false when there are none.
func (s *store) minLiveCursor(ctx context.Context, x ExecQuerier, tenant string, activeSince int64) (cursor int64, ok bool, err error) {
	var c sql.NullInt64
	err = queryRow(ctx, x, s.q(`SELECT MIN(cursor_seq) FROM `+s.devices+`
		WHERE tenant_id = ? AND revoked_at IS NULL AND COALESCE(last_pull_at, created_at) >= ?`), tenant, activeSince)(&c)
	return c.Int64, c.Valid, err
}

const purgeWhere = " WHERE tenant_id = ? AND seq <= ? AND created_at < ?"

// purgeTop is the highest seq purgeLedger(maxSeq, before) would delete.
func (s *store) purgeTop(ctx context.Context, x ExecQuerier, tenant string, maxSeq, before int64) (int64, error) {
	var t sql.NullInt64
	err := queryRow(ctx, x, s.q("SELECT MAX(seq) FROM "+s.ledger+purgeWhere), tenant, maxSeq, before)(&t)
	return t.Int64, err
}

// purgeLedger deletes a tenant's rows at or below maxSeq older than before.
func (s *store) purgeLedger(ctx context.Context, x ExecQuerier, tenant string, maxSeq, before int64) (int64, error) {
	res, err := x.ExecContext(ctx, s.q("DELETE FROM "+s.ledger+purgeWhere), tenant, maxSeq, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *store) raiseHorizon(ctx context.Context, x ExecQuerier, tenant string, horizon int64) error {
	_, err := x.ExecContext(ctx, s.q("UPDATE "+s.tenants+" SET horizon = ? WHERE id = ? AND horizon < ?"), horizon, tenant, horizon)
	return err
}
