package offline_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/golaravel/golaravel/pkg/database"
	"github.com/golaravel/golaravel/pkg/offline"
	"github.com/golaravel/golaravel/pkg/offline/internal/testent"
	"github.com/golaravel/golaravel/pkg/offline/internal/testent/product"
	"github.com/golaravel/golaravel/pkg/offline/internal/testent/syncledger"
	"github.com/golaravel/golaravel/pkg/privacy"
)

type env struct {
	t      *testing.T
	client *testent.Client
	eng    *offline.Engine[*testent.Tx]
	now    time.Time
	// failApply makes the next n Apply calls fail with this error.
	failApply atomic.Pointer[error]
	failCount atomic.Int32
}

func newEnv(t *testing.T, configure ...func(*offline.Options[*testent.Tx])) *env {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "sync.db") + "?_fk=1&_busy_timeout=10000&_journal_mode=WAL&_txlock=immediate"
	client, err := testent.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()
	// Ent creates the sync tables from offline/schema; Migrate afterwards
	// must find them compatible and do nothing.
	if err := client.Schema.Create(ctx); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, client: client, now: time.Now()}
	opts := offline.Options[*testent.Tx]{
		Begin:   func(ctx context.Context) (*testent.Tx, error) { return client.Tx(ctx) },
		Dialect: database.SQLite,
		Now:     func() time.Time { return e.now },
	}
	for _, f := range configure {
		f(&opts)
	}
	eng, err := offline.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := eng.Register(e.productResource()); err != nil {
		t.Fatal(err)
	}
	client.Product.Intercept(privacy.TenantScope(func(q *testent.ProductQuery, tenant string) {
		q.Where(product.TenantID(tenant))
	}))
	client.Product.Use(privacy.TenantMutationHook())
	client.Product.Use(offline.ChangeHook(eng, "product"))
	e.eng = eng
	return e
}

func toRecord(p *testent.Product) *offline.Record {
	return &offline.Record{ID: p.ID, Version: p.SyncVersion, HLC: p.SyncHlc, FieldClocks: p.SyncClocks,
		Deleted: p.DeletedAt != nil,
		Data:    map[string]any{"name": p.Name, "price": p.Price, "stock": p.Stock}}
}

func (e *env) productResource() offline.Resource[*testent.Tx] {
	return offline.Resource[*testent.Tx]{
		Name:     "product",
		Fields:   []string{"name", "price", "stock"},
		Counters: []string{"stock"},
		Load: func(ctx context.Context, tx *testent.Tx, id string) (*offline.Record, error) {
			p, err := tx.Product.Get(ctx, id)
			if testent.IsNotFound(err) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			return toRecord(p), nil
		},
		Snapshot: func(ctx context.Context, tx *testent.Tx, after string, limit int) ([]*offline.Record, error) {
			ps, err := tx.Product.Query().Where(product.IDGT(after), product.DeletedAtIsNil()).
				Order(product.ByID()).Limit(limit).All(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]*offline.Record, len(ps))
			for i, p := range ps {
				out[i] = toRecord(p)
			}
			return out, nil
		},
		Apply: func(ctx context.Context, tx *testent.Tx, w *offline.Write) error {
			if n := e.failCount.Load(); n > 0 {
				e.failCount.Add(-1)
				return *e.failApply.Load()
			}
			var f struct {
				Name  *string  `json:"name"`
				Price *int64   `json:"price"`
				Stock *float64 `json:"stock"`
			}
			if err := w.Decode(&f); err != nil {
				return offline.Reject("invalid", err)
			}
			if f.Price != nil && *f.Price < 0 {
				return offline.Rejectf("invalid", "price must not be negative")
			}
			switch {
			case w.Op == offline.OpCreate && w.Current == nil:
				c := tx.Product.Create().SetID(w.EntityID).SetSyncVersion(w.Version).SetSyncHlc(w.HLC).
					SetSyncClocks(w.FieldClocks)
				if f.Name != nil {
					c.SetName(*f.Name)
				}
				if f.Price != nil {
					c.SetPrice(*f.Price)
				}
				stock := w.Inc["stock"]
				if f.Stock != nil {
					stock += *f.Stock
				}
				return c.SetStock(stock).Exec(ctx)
			case w.Op == offline.OpDelete:
				return tx.Product.UpdateOneID(w.EntityID).SetDeletedAt(time.Now()).SetSyncVersion(w.Version).
					SetSyncHlc(w.HLC).Exec(ctx)
			default: // update, or create over a tombstone (restore)
				u := tx.Product.UpdateOneID(w.EntityID).ClearDeletedAt().SetSyncVersion(w.Version).
					SetSyncHlc(w.HLC).SetSyncClocks(w.FieldClocks)
				if f.Name != nil {
					u.SetName(*f.Name)
				}
				if f.Price != nil {
					u.SetPrice(*f.Price)
				}
				if f.Stock != nil {
					u.SetStock(*f.Stock)
				}
				if d, ok := w.Inc["stock"]; ok {
					u.AddStock(d)
				}
				return u.Exec(ctx)
			}
		},
	}
}

func (e *env) failNext(n int, err error) {
	e.failApply.Store(&err)
	e.failCount.Store(int32(n))
}

func (e *env) device(tenant string) (offline.Principal, *offline.Device) {
	e.t.Helper()
	p := offline.Principal{TenantID: tenant, UserID: "u-" + tenant}
	d, err := e.eng.RegisterDevice(context.Background(), p, offline.DeviceInfo{Name: "till"})
	if err != nil {
		e.t.Fatal(err)
	}
	return p, d
}

func (e *env) push(p offline.Principal, d *offline.Device, muts ...offline.Mutation) []offline.Result {
	e.t.Helper()
	resp, err := e.eng.Push(context.Background(), p, &offline.PushRequest{DeviceID: d.ID, Mutations: muts})
	if err != nil {
		e.t.Fatal(err)
	}
	if len(resp.Results) != len(muts) {
		e.t.Fatalf("got %d results for %d mutations", len(resp.Results), len(muts))
	}
	return resp.Results
}

func (e *env) product(tenant, id string) *testent.Product {
	e.t.Helper()
	p, err := e.client.Product.Get(privacy.WithTenantID(context.Background(), tenant), id)
	if err != nil {
		e.t.Fatalf("product %s: %v", id, err)
	}
	return p
}

func (e *env) ledgerCount(tenant string) int {
	e.t.Helper()
	n, err := e.client.SyncLedger.Query().Where(syncledger.TenantID(tenant)).Count(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

// clock hands out increasing HLCs for a fake device.
type clock struct {
	node string
	wall int64
}

func newClock(node string, at time.Time) *clock { return &clock{node: node, wall: at.UnixMilli()} }

func (c *clock) next() string {
	c.wall++
	return offline.HLC{Wall: c.wall, Node: c.node}.String()
}

func set(kv ...any) map[string]json.RawMessage {
	m := map[string]json.RawMessage{}
	for i := 0; i < len(kv); i += 2 {
		b, _ := json.Marshal(kv[i+1])
		m[kv[i].(string)] = b
	}
	return m
}

func create(c *clock, id string, fields map[string]json.RawMessage) offline.Mutation {
	return offline.Mutation{ID: offline.NewID(), Entity: "product", EntityID: id, Op: offline.OpCreate, Set: fields, HLC: c.next()}
}

func update(c *clock, id string, fields map[string]json.RawMessage) offline.Mutation {
	return offline.Mutation{ID: offline.NewID(), Entity: "product", EntityID: id, Op: offline.OpUpdate, Set: fields, HLC: c.next()}
}

func inc(c *clock, id string, by float64) offline.Mutation {
	return offline.Mutation{ID: offline.NewID(), Entity: "product", EntityID: id, Op: offline.OpUpdate,
		Inc: map[string]float64{"stock": by}, HLC: c.next()}
}

func del(c *clock, id string) offline.Mutation {
	return offline.Mutation{ID: offline.NewID(), Entity: "product", EntityID: id, Op: offline.OpDelete, HLC: c.next()}
}

func wantStatus(t *testing.T, rs []offline.Result, want ...offline.Status) {
	t.Helper()
	for i, w := range want {
		if rs[i].Status != w {
			t.Fatalf("result %d: status %s (%s), want %s", i, rs[i].Status, rs[i].Reason, w)
		}
	}
}

var errTransient = errors.New("database is locked")
