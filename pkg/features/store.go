package features

import (
	"context"
	"fmt"

	"github.com/golaravel/golaravel/pkg/database"
	"github.com/golaravel/golaravel/pkg/privacy"
)

// store reads and writes the tenant_settings table. A row's id is
// "tenant_id|name"; names cannot contain '|', so ids are unambiguous.
type store struct {
	db    ExecQuerier
	d     database.Dialect
	table string
}

func (s *store) q(query string) string { return s.d.Rebind(query) }

func (s *store) migrate(ctx context.Context) error {
	k := s.d.KeyType()
	text := "TEXT"
	if s.d == database.MySQL {
		text = "LONGTEXT"
	}
	st := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		id %s PRIMARY KEY, tenant_id %s NOT NULL, name %s NOT NULL, value %s NOT NULL,
		updated_by %s, updated_at BIGINT NOT NULL)`, s.table, k, k, k, text, k)
	if _, err := s.db.ExecContext(ctx, st); err != nil {
		return err
	}
	idx := s.table + "_tenant_idx"
	if s.d == database.MySQL {
		// MySQL has no CREATE INDEX IF NOT EXISTS.
		rows, err := s.db.QueryContext(ctx, `SELECT COUNT(*) FROM information_schema.statistics
			WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ?`, s.table, idx)
		if err != nil {
			return err
		}
		var n int
		if rows.Next() {
			err = rows.Scan(&n)
		}
		if cerr := rows.Close(); err == nil {
			err = cerr
		}
		if err != nil || n > 0 {
			return err
		}
		_, err = s.db.ExecContext(ctx, fmt.Sprintf("CREATE INDEX %s ON %s (tenant_id)", idx, s.table))
		return err
	}
	_, err := s.db.ExecContext(ctx, fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s (tenant_id)", idx, s.table))
	return err
}

// tenant returns the active tenant. ok is false when tenancy is bypassed:
// reads then return defaults.
func tenant(ctx context.Context) (id string, ok bool, err error) {
	if id, found := privacy.TenantFromContext(ctx); found && id != "" {
		return id, true, nil
	}
	if privacy.IsTenancyBypassed(ctx) {
		return "", false, nil
	}
	return "", false, privacy.ErrTenantMissing
}

// writeTenant is tenant for writes, which always need a tenant.
func writeTenant(ctx context.Context) (string, error) {
	id, ok, err := tenant(ctx)
	if err == nil && !ok {
		err = fmt.Errorf("%w (tenancy is bypassed; use features.ForTenant to pick one)", privacy.ErrTenantMissing)
	}
	return id, err
}

func (s *store) get(ctx context.Context, key string) ([]byte, bool, error) {
	t, ok, err := tenant(ctx)
	if err != nil || !ok {
		return nil, false, err
	}
	rows, err := s.db.QueryContext(ctx, s.q("SELECT value FROM "+s.table+" WHERE id = ?"), t+"|"+key)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, false, rows.Err()
	}
	var raw string
	if err := rows.Scan(&raw); err != nil {
		return nil, false, err
	}
	return []byte(raw), true, rows.Close()
}

func (s *store) all(ctx context.Context) (map[string][]byte, error) {
	out := map[string][]byte{}
	t, ok, err := tenant(ctx)
	if err != nil || !ok {
		return out, err
	}
	rows, err := s.db.QueryContext(ctx, s.q("SELECT name, value FROM "+s.table+" WHERE tenant_id = ?"), t)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name, raw string
		if err := rows.Scan(&name, &raw); err != nil {
			return nil, err
		}
		out[name] = []byte(raw)
	}
	return out, rows.Err()
}

func (s *store) set(ctx context.Context, m *Manager, key string, raw []byte) error {
	t, err := writeTenant(ctx)
	if err != nil {
		return err
	}
	var by any
	if m.actor != nil {
		if a := m.actor(ctx); a != "" {
			by = a
		}
	}
	_, err = s.db.ExecContext(ctx, s.d.Upsert(s.table, "id", "tenant_id", "name", "value", "updated_by", "updated_at"),
		t+"|"+key, t, key, string(raw), by, m.now().UnixMilli())
	return err
}

func (s *store) reset(ctx context.Context, _ *Manager, key string) error {
	t, err := writeTenant(ctx)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, s.q("DELETE FROM "+s.table+" WHERE id = ?"), t+"|"+key)
	return err
}
