package features

import (
	"context"
	"strconv"
	"sync"
)

// Reads go through up to two layers before the database:
//
//  1. A request memo (WithMemo), so one request loads a tenant's values
//     once however many features it checks.
//  2. The shared cache (Options.Cache), holding each tenant's values as one
//     entry. Its key carries a per-tenant version that every write bumps,
//     so a reader that loaded old values just before a write caches them
//     under the old version, where no later read looks.

type memoCtxKey struct{}

type memoKey struct {
	m      *Manager
	tenant string
}

type memo struct {
	mu   sync.Mutex
	vals map[memoKey]map[string]string
}

// WithMemo returns ctx with a request-scoped memo: within it, each tenant's
// values are loaded at most once. Wrap each request's context (the HTTP
// middleware does this); writes through the same context clear the memo.
func WithMemo(ctx context.Context) context.Context {
	if _, ok := ctx.Value(memoCtxKey{}).(*memo); ok {
		return ctx
	}
	return context.WithValue(ctx, memoCtxKey{}, &memo{vals: map[memoKey]map[string]string{}})
}

func memoFrom(ctx context.Context) *memo {
	mm, _ := ctx.Value(memoCtxKey{}).(*memo)
	return mm
}

// values returns the active tenant's stored values (name → raw JSON). The
// map must not be modified. With tenancy bypassed it is empty.
func (m *Manager) values(ctx context.Context) (map[string]string, error) {
	vals, err := m.storedValues(ctx)
	if err != nil {
		return nil, err
	}
	return m.withFakes(ctx, vals)
}

func (m *Manager) storedValues(ctx context.Context) (map[string]string, error) {
	t, ok, err := tenant(ctx)
	if err != nil || !ok {
		return nil, err
	}
	mm := memoFrom(ctx)
	if mm != nil {
		mm.mu.Lock()
		v, hit := mm.vals[memoKey{m, t}]
		mm.mu.Unlock()
		if hit {
			return v, nil
		}
	}
	v, err := m.cached(ctx, t)
	if err != nil {
		return nil, err
	}
	if mm != nil {
		mm.mu.Lock()
		mm.vals[memoKey{m, t}] = v
		mm.mu.Unlock()
	}
	return v, nil
}

func (m *Manager) versionKey(t string) string { return m.cachePrefix + "v:" + t }

func (m *Manager) dataKey(t string, ver int64) string {
	return m.cachePrefix + "d:" + t + ":" + strconv.FormatInt(ver, 10)
}

func (m *Manager) cached(ctx context.Context, t string) (map[string]string, error) {
	if m.cache == nil {
		return m.store.load(ctx, t)
	}
	var ver int64
	if raw, ok := m.cache.Get(m.versionKey(t)); ok {
		ver = toInt64(raw)
	}
	var loadErr error
	v, err := m.cache.Remember(m.dataKey(t, ver), m.cacheTTL, func() (any, error) {
		vals, err := m.store.load(ctx, t)
		loadErr = err
		return vals, err
	})
	if loadErr != nil {
		return nil, loadErr
	}
	if err == nil {
		if vals, ok := asStringMap(v); ok {
			return vals, nil
		}
	}
	// The cache failed or holds something unexpected: read the database.
	m.log.WarnContext(ctx, "features: cache unavailable, reading database", "tenant", t, "error", err)
	return m.store.load(ctx, t)
}

// invalidate drops the tenant's memoized and cached values after a write.
func (m *Manager) invalidate(ctx context.Context, t string) {
	if mm := memoFrom(ctx); mm != nil {
		mm.mu.Lock()
		delete(mm.vals, memoKey{m, t})
		mm.mu.Unlock()
	}
	if m.cache == nil {
		return
	}
	if _, err := m.cache.Increment(m.versionKey(t)); err != nil {
		m.log.ErrorContext(ctx, "features: cache invalidation failed; tenant may see old values until the cache TTL",
			"tenant", t, "error", err)
	}
}

func toInt64(v any) int64 {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int64:
		return x
	case float64:
		return int64(x)
	case string:
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	}
	return 0
}

// asStringMap accepts the map as stored (memory cache) or JSON-decoded
// (SQL and Redis caches).
func asStringMap(v any) (map[string]string, bool) {
	switch x := v.(type) {
	case map[string]string:
		return x, true
	case map[string]any:
		out := make(map[string]string, len(x))
		for k, e := range x {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			out[k] = s
		}
		return out, true
	}
	return nil, false
}
