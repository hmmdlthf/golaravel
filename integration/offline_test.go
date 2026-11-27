package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golaravel/golaravel/pkg/offline"
	"github.com/golaravel/golaravel/pkg/privacy"
)

// The offline sync engine (pkg/offline) on each SQL backend, through its
// plain database/sql path (offline.SQLTx) with a hand-written resource,
// as an app without Ent would use it. This covers the dialect-specific
// SQL: index creation, insert-ignore, the tenant sequence lock, grouped
// pull queries and compaction.

type offlineEnv struct {
	t     *testing.T
	b     sqlBackend
	eng   *offline.Engine[offline.SQLTx]
	items string
	now   atomic.Int64 // Unix ms; 0 = real time
}

func newOfflineEnv(t *testing.T, b sqlBackend) *offlineEnv {
	t.Helper()
	prefix := "it" + suffix() + "_"
	e := &offlineEnv{t: t, b: b, items: prefix + "items"}
	dropTables(t, b.db, prefix+"sync_ledger", prefix+"sync_devices", prefix+"sync_tenants", e.items)
	k := b.dialect.KeyType()
	if _, err := b.db.Exec(fmt.Sprintf(`CREATE TABLE %s (id %s PRIMARY KEY, tenant_id %s NOT NULL, name %s,
		qty DOUBLE PRECISION NOT NULL DEFAULT 0, version BIGINT NOT NULL DEFAULT 0, hlc %s, clocks TEXT,
		deleted INTEGER NOT NULL DEFAULT 0)`, e.items, k, k, k, k)); err != nil {
		t.Fatal(err)
	}
	eng, err := offline.New(offline.Options[offline.SQLTx]{
		Begin:   offline.BeginSQL(b.db),
		Dialect: b.dialect,
		Prefix:  prefix,
		Now: func() time.Time {
			if ms := e.now.Load(); ms != 0 {
				return time.UnixMilli(ms)
			}
			return time.Now()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := 0; i < 2; i++ { // Migrate must be repeatable
		if err := eng.Migrate(ctx); err != nil {
			t.Fatalf("migrate (run %d): %v", i+1, err)
		}
	}
	if err := eng.Register(e.resource()); err != nil {
		t.Fatal(err)
	}
	e.eng = eng
	return e
}

func (e *offlineEnv) q(s string) string { return e.b.dialect.Rebind(s) }

func (e *offlineEnv) resource() offline.Resource[offline.SQLTx] {
	cols := "id, name, qty, version, hlc, clocks, deleted"
	scan := func(sc func(...any) error) (*offline.Record, error) {
		var id string
		var name, hlc, clocks sql.NullString
		var qty float64
		var version int64
		var deleted int
		if err := sc(&id, &name, &qty, &version, &hlc, &clocks, &deleted); err != nil {
			return nil, err
		}
		r := &offline.Record{ID: id, Version: version, HLC: hlc.String, Deleted: deleted != 0,
			Data: map[string]any{"name": name.String, "qty": qty}}
		if clocks.Valid {
			_ = json.Unmarshal([]byte(clocks.String), &r.FieldClocks)
		}
		return r, nil
	}
	tenantOf := func(ctx context.Context) string { t, _ := privacy.TenantFromContext(ctx); return t }
	return offline.Resource[offline.SQLTx]{
		Name:     "item",
		Fields:   []string{"name", "qty"},
		Counters: []string{"qty"},
		Load: func(ctx context.Context, tx offline.SQLTx, id string) (*offline.Record, error) {
			r, err := scan(tx.QueryRowContext(ctx, e.q("SELECT "+cols+" FROM "+e.items+" WHERE id = ? AND tenant_id = ?"), id, tenantOf(ctx)).Scan)
			if errors.Is(err, sql.ErrNoRows) {
				return nil, nil
			}
			return r, err
		},
		Snapshot: func(ctx context.Context, tx offline.SQLTx, after string, limit int) ([]*offline.Record, error) {
			rows, err := tx.QueryContext(ctx, e.q("SELECT "+cols+" FROM "+e.items+
				" WHERE tenant_id = ? AND id > ? AND deleted = 0 ORDER BY id LIMIT ?"), tenantOf(ctx), after, limit)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			var out []*offline.Record
			for rows.Next() {
				r, err := scan(rows.Scan)
				if err != nil {
					return nil, err
				}
				out = append(out, r)
			}
			return out, rows.Err()
		},
		Apply: func(ctx context.Context, tx offline.SQLTx, w *offline.Write) error {
			var f struct {
				Name *string  `json:"name"`
				Qty  *float64 `json:"qty"`
			}
			if err := w.Decode(&f); err != nil {
				return offline.Reject("invalid", err)
			}
			if f.Name != nil && *f.Name == "bad" {
				return offline.Rejectf("invalid", "name may not be bad")
			}
			clocks, _ := json.Marshal(w.FieldClocks)
			var err error
			switch {
			case w.Op == offline.OpCreate && w.Current == nil:
				name, qty := "", w.Inc["qty"]
				if f.Name != nil {
					name = *f.Name
				}
				if f.Qty != nil {
					qty += *f.Qty
				}
				_, err = tx.ExecContext(ctx, e.q("INSERT INTO "+e.items+
					" (id, tenant_id, name, qty, version, hlc, clocks, deleted) VALUES (?, ?, ?, ?, ?, ?, ?, 0)"),
					w.EntityID, w.Principal.TenantID, name, qty, w.Version, w.HLC, string(clocks))
			case w.Op == offline.OpDelete:
				_, err = tx.ExecContext(ctx, e.q("UPDATE "+e.items+" SET deleted = 1, version = ?, hlc = ? WHERE id = ? AND tenant_id = ?"),
					w.Version, w.HLC, w.EntityID, w.Principal.TenantID)
			default:
				set := "deleted = 0, version = ?, hlc = ?, clocks = ?, qty = qty + ?"
				args := []any{w.Version, w.HLC, string(clocks), w.Inc["qty"]}
				if f.Name != nil {
					set += ", name = ?"
					args = append(args, *f.Name)
				}
				if f.Qty != nil {
					set += ", qty = ?"
					args = append(args, *f.Qty+w.Inc["qty"])
				}
				args = append(args, w.EntityID, w.Principal.TenantID)
				_, err = tx.ExecContext(ctx, e.q("UPDATE "+e.items+" SET "+set+" WHERE id = ? AND tenant_id = ?"), args...)
			}
			return err
		},
	}
}

func (e *offlineEnv) device(tenant string) (offline.Principal, *offline.Device) {
	e.t.Helper()
	p := offline.Principal{TenantID: tenant, UserID: "u"}
	d, err := e.eng.RegisterDevice(context.Background(), p, offline.DeviceInfo{Name: "till"})
	if err != nil {
		e.t.Fatal(err)
	}
	return p, d
}

func (e *offlineEnv) push(p offline.Principal, d *offline.Device, muts ...offline.Mutation) []offline.Result {
	e.t.Helper()
	resp, err := e.eng.Push(context.Background(), p, &offline.PushRequest{DeviceID: d.ID, Mutations: muts})
	if err != nil {
		e.t.Fatal(err)
	}
	return resp.Results
}

func (e *offlineEnv) qty(tenant, id string) float64 {
	e.t.Helper()
	var q float64
	if err := e.b.db.QueryRow(e.q("SELECT qty FROM "+e.items+" WHERE id = ? AND tenant_id = ?"), id, tenant).Scan(&q); err != nil {
		e.t.Fatalf("qty %s: %v", id, err)
	}
	return q
}

type hlcGen struct {
	mu   sync.Mutex
	node string
	wall int64
}

func (g *hlcGen) next() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if now := time.Now().UnixMilli(); now > g.wall {
		g.wall = now
	} else {
		g.wall++
	}
	return offline.HLC{Wall: g.wall, Node: g.node}.String()
}

func mutation(g *hlcGen, op offline.Op, id string, set map[string]any, inc map[string]float64) offline.Mutation {
	m := offline.Mutation{ID: offline.NewID(), Entity: "item", EntityID: id, Op: op, HLC: g.next(), Inc: inc}
	if set != nil {
		m.Set = map[string]json.RawMessage{}
		for k, v := range set {
			b, _ := json.Marshal(v)
			m.Set[k] = b
		}
	}
	return m
}

func wantStatuses(t *testing.T, rs []offline.Result, want ...offline.Status) {
	t.Helper()
	if len(rs) != len(want) {
		t.Fatalf("%d results, want %d", len(rs), len(want))
	}
	for i, w := range want {
		if rs[i].Status != w {
			t.Fatalf("result %d: %s (%s), want %s", i, rs[i].Status, rs[i].Reason, w)
		}
	}
}

func TestOfflineSyncSQL(t *testing.T) {
	for _, b := range sqlBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			e := newOfflineEnv(t, b)
			ctx := context.Background()
			p, d := e.device("acme")
			g := &hlcGen{node: d.ID}

			// Idempotent push.
			batch := []offline.Mutation{
				mutation(g, offline.OpCreate, "i1", map[string]any{"name": "Tea", "qty": 10}, nil),
				mutation(g, offline.OpUpdate, "i1", nil, map[string]float64{"qty": -2}),
			}
			wantStatuses(t, e.push(p, d, batch...), offline.StatusApplied, offline.StatusApplied)
			again := e.push(p, d, batch...)
			wantStatuses(t, again, offline.StatusDuplicate, offline.StatusDuplicate)
			if again[1].Original != offline.StatusApplied {
				t.Fatalf("duplicate original %s", again[1].Original)
			}
			if q := e.qty("acme", "i1"); q != 8 {
				t.Fatalf("qty %v, want 8", q)
			}

			// A group with a rejected member rolls back entirely.
			a := mutation(g, offline.OpCreate, "i2", map[string]any{"name": "Cake"}, nil)
			bad := mutation(g, offline.OpUpdate, "i1", map[string]any{"name": "bad"}, nil)
			a.Group, bad.Group = "sale-1", "sale-1"
			wantStatuses(t, e.push(p, d, a, bad), offline.StatusRejected, offline.StatusRejected)
			var n int
			if err := b.db.QueryRow(e.q("SELECT COUNT(*) FROM "+e.items+" WHERE id = ?"), "i2").Scan(&n); err != nil || n != 0 {
				t.Fatalf("i2 exists after its group was rejected (%d, %v)", n, err)
			}
			retried := e.push(p, d, a, bad)
			wantStatuses(t, retried, offline.StatusDuplicate, offline.StatusDuplicate)
			if retried[0].Original != offline.StatusRejected {
				t.Fatalf("rejection not remembered: %+v", retried[0])
			}

			// Pull pages, one entity per change, tombstones included.
			wantStatuses(t, e.push(p, d,
				mutation(g, offline.OpCreate, "i3", map[string]any{"name": "Bun"}, nil),
				mutation(g, offline.OpCreate, "i4", map[string]any{"name": "Pie"}, nil),
				mutation(g, offline.OpDelete, "i3", nil, nil),
			), offline.StatusApplied, offline.StatusApplied, offline.StatusApplied)

			// A change made outside sync, recorded in the same transaction.
			tx, err := b.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(e.q("UPDATE "+e.items+" SET name = ?, version = version + 1 WHERE id = ?"), "Pie (admin)", "i4"); err != nil {
				t.Fatal(err)
			}
			if err := e.eng.Record(ctx, tx, "acme", "item", offline.OpUpdate, "i4"); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}

			_, reader := e.device("acme")
			var got []string
			cursor := int64(0)
			for pages := 0; ; pages++ {
				res, err := e.eng.Pull(ctx, p, reader.ID, cursor, 1)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range res.Changes {
					s := c.EntityID
					if c.Deleted {
						s += "(deleted)"
					} else if c.EntityID == "i4" {
						s += "=" + c.Data.(map[string]any)["name"].(string)
					}
					got = append(got, s)
				}
				if res.Cursor <= cursor && len(res.Changes) > 0 {
					t.Fatalf("cursor did not advance: %d -> %d", cursor, res.Cursor)
				}
				cursor = res.Cursor
				if !res.HasMore {
					break
				}
				if pages > 20 {
					t.Fatal("pull never finished")
				}
			}
			if fmt.Sprint(got) != "[i1 i3(deleted) i4=Pie (admin)]" {
				t.Fatalf("pulled %v", got)
			}

			// Snapshot pages.
			var ids []string
			after := ""
			for {
				page, err := e.eng.Snapshot(ctx, p, reader.ID, "item", after, 1)
				if err != nil {
					t.Fatal(err)
				}
				if after == "" && page.Cursor != cursor {
					t.Fatalf("snapshot cursor %d, pull ended at %d", page.Cursor, cursor)
				}
				for _, r := range page.Records {
					ids = append(ids, r.ID)
				}
				if !page.HasMore {
					break
				}
				after = page.Next
			}
			if fmt.Sprint(ids) != "[i1 i4]" {
				t.Fatalf("snapshot %v", ids)
			}

			// Tenants are isolated, devices can be listed and revoked.
			pb, db := e.device("globex")
			if res, err := e.eng.Pull(ctx, pb, db.ID, 0, 0); err != nil || len(res.Changes) != 0 {
				t.Fatalf("globex sees acme's data: %+v %v", res, err)
			}
			if _, err := e.eng.Pull(ctx, pb, reader.ID, 0, 0); !errors.Is(err, offline.ErrUnknownDevice) {
				t.Fatalf("cross-tenant device: %v", err)
			}
			devs, err := e.eng.Devices(ctx, "acme")
			if err != nil || len(devs) != 2 {
				t.Fatalf("devices: %d %v", len(devs), err)
			}
			if err := e.eng.RevokeDevice(ctx, "acme", reader.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := e.eng.Pull(ctx, p, reader.ID, 0, 0); !errors.Is(err, offline.ErrDeviceRevoked) {
				t.Fatalf("revoked device pulled: %v", err)
			}
		})
	}
}

// Many devices push at once, all incrementing one counter and creating
// rows, while another device pulls. On MySQL and Postgres this is real
// concurrency: the tenant sequence lock must keep pulls from skipping a
// late commit, and no increment may be lost.
func TestOfflineConcurrentSQL(t *testing.T) {
	for _, b := range sqlBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			e := newOfflineEnv(t, b)
			ctx := context.Background()
			p, owner := e.device("acme")
			g0 := &hlcGen{node: owner.ID}
			wantStatuses(t, e.push(p, owner, mutation(g0, offline.OpCreate, "shared", map[string]any{"name": "Stock"}, nil)),
				offline.StatusApplied)

			const writers, each = 6, 15
			var retries atomic.Int64
			var wg sync.WaitGroup
			errs := make(chan error, writers)
			for w := 0; w < writers; w++ {
				_, d := e.device("acme")
				wg.Add(1)
				go func(w int) {
					defer wg.Done()
					g := &hlcGen{node: d.ID}
					for i := 0; i < each; i++ {
						batch := []offline.Mutation{
							mutation(g, offline.OpCreate, fmt.Sprintf("w%d-%02d", w, i), map[string]any{"name": "x"}, nil),
							mutation(g, offline.OpUpdate, "shared", nil, map[string]float64{"qty": 1}),
						}
						// Like a client: resend whatever came back "retry".
						for attempt := 0; len(batch) > 0; attempt++ {
							if attempt > 50 {
								errs <- fmt.Errorf("writer %d: gave up", w)
								return
							}
							resp, err := e.eng.Push(ctx, p, &offline.PushRequest{DeviceID: d.ID, Mutations: batch})
							if err != nil {
								errs <- err
								return
							}
							var left []offline.Mutation
							for k, r := range resp.Results {
								switch r.Status {
								case offline.StatusApplied, offline.StatusDuplicate:
								case offline.StatusRetry:
									left = append(left, batch[k])
								default:
									errs <- fmt.Errorf("writer %d: %s %s", w, r.Status, r.Reason)
									return
								}
							}
							if len(left) > 0 {
								retries.Add(1)
								time.Sleep(time.Duration(attempt+1) * 5 * time.Millisecond)
							}
							batch = left
						}
					}
				}(w)
			}
			done := make(chan struct{})
			go func() { wg.Wait(); close(done) }()

			_, reader := e.device("acme")
			seen := map[string]bool{}
			var cursor int64
			pull := func() {
				res, err := e.eng.Pull(ctx, p, reader.ID, cursor, 7)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range res.Changes {
					seen[c.EntityID] = true
				}
				cursor = res.Cursor
			}
			for running := true; running; {
				select {
				case <-done:
					running = false
				default:
					pull()
				}
			}
			close(errs)
			for err := range errs {
				t.Fatal(err)
			}
			for i := 0; i < 30; i++ {
				pull()
			}
			for w := 0; w < writers; w++ {
				for i := 0; i < each; i++ {
					if id := fmt.Sprintf("w%d-%02d", w, i); !seen[id] {
						t.Fatalf("puller missed %s", id)
					}
				}
			}
			if q := e.qty("acme", "shared"); q != writers*each {
				t.Fatalf("shared counter %v, want %d (lost increments)", q, writers*each)
			}
			t.Logf("%s: %d pushes needed a retry", b.name, retries.Load())
		})
	}
}

func TestOfflineCompactionSQL(t *testing.T) {
	for _, b := range sqlBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			e := newOfflineEnv(t, b)
			ctx := context.Background()
			old := time.Now().Add(-40 * 24 * time.Hour)
			e.now.Store(old.UnixMilli())
			p, a := e.device("acme")
			_, idle := e.device("acme")
			g := &hlcGen{node: a.ID}
			for i := 0; i < 4; i++ {
				e.push(p, a, mutation(g, offline.OpCreate, fmt.Sprintf("c%d", i), map[string]any{"name": "x"}, nil))
			}
			e.push(p, a, mutation(g, offline.OpUpdate, "c0", map[string]any{"name": "bad"}, nil)) // rejected, seq 0

			e.now.Store(0)
			if _, err := e.eng.Pull(ctx, p, a.ID, 3, 0); err != nil {
				t.Fatal(err)
			}
			r, err := e.eng.Compact(ctx, offline.CompactOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if r.Deleted != 4 || r.Horizon["acme"] != 3 {
				t.Fatalf("compaction: %+v", r)
			}
			if res, err := e.eng.Pull(ctx, p, a.ID, 3, 0); err != nil || len(res.Changes) != 1 {
				t.Fatalf("active device continues: %+v %v", res, err)
			}
			if _, err := e.eng.Pull(ctx, p, idle.ID, 0, 0); !errors.Is(err, offline.ErrResyncRequired) {
				t.Fatalf("idle device must resync: %v", err)
			}
			if err := e.eng.CompactCLI([]string{"--tenant=acme"}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
