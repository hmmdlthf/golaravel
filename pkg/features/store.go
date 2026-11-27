package features

import (
	"context"
	"fmt"
	"sync"

	"github.com/golaravel/golaravel/pkg/database"
	"github.com/golaravel/golaravel/pkg/privacy"
)

// backend stores tenant values. sqlStore is the real one; memStore backs
// NewInMemory for tests and tools.
type backend interface {
	migrate(ctx context.Context) error
	// load returns every stored value of a tenant, name → raw JSON.
	load(ctx context.Context, tenant string) (map[string]string, error)
	upsert(ctx context.Context, tenant, key, raw string, by any, at int64) error
	delete(ctx context.Context, tenant, key string) error
	// rows lists every stored (tenant, name) pair, across tenants.
	rows(ctx context.Context) ([][2]string, error)
}

// sqlStore reads and writes the tenant_settings table. A row's id is
// "tenant_id|name"; names cannot contain '|', so ids are unambiguous.
type sqlStore struct {
	db    ExecQuerier
	d     database.Dialect
	table string
}

func (s *sqlStore) q(query string) string { return s.d.Rebind(query) }

func (s *sqlStore) migrate(ctx context.Context) error {
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

func (s *sqlStore) load(ctx context.Context, tenant string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, s.q("SELECT name, value FROM "+s.table+" WHERE tenant_id = ?"), tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, raw string
		if err := rows.Scan(&name, &raw); err != nil {
			return nil, err
		}
		out[name] = raw
	}
	return out, rows.Err()
}

func (s *sqlStore) upsert(ctx context.Context, tenant, key, raw string, by any, at int64) error {
	_, err := s.db.ExecContext(ctx, s.d.Upsert(s.table, "id", "tenant_id", "name", "value", "updated_by", "updated_at"),
		tenant+"|"+key, tenant, key, raw, by, at)
	return err
}

func (s *sqlStore) delete(ctx context.Context, tenant, key string) error {
	_, err := s.db.ExecContext(ctx, s.q("DELETE FROM "+s.table+" WHERE id = ?"), tenant+"|"+key)
	return err
}

func (s *sqlStore) rows(ctx context.Context) ([][2]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT tenant_id, name FROM "+s.table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var r [2]string
		if err := rows.Scan(&r[0], &r[1]); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// memStore keeps values in memory.
type memStore struct {
	mu   sync.Mutex
	vals map[string]map[string]string
}

func (s *memStore) migrate(context.Context) error { return nil }

func (s *memStore) load(_ context.Context, tenant string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.vals[tenant]))
	for k, v := range s.vals[tenant] {
		out[k] = v
	}
	return out, nil
}

func (s *memStore) upsert(_ context.Context, tenant, key, raw string, _ any, _ int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.vals[tenant] == nil {
		s.vals[tenant] = map[string]string{}
	}
	s.vals[tenant][key] = raw
	return nil
}

func (s *memStore) delete(_ context.Context, tenant, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.vals[tenant], key)
	return nil
}

func (s *memStore) rows(context.Context) ([][2]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out [][2]string
	for t, vals := range s.vals {
		for k := range vals {
			out = append(out, [2]string{t, k})
		}
	}
	return out, nil
}
