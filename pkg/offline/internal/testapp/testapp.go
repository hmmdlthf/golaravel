// Package testapp is a small synced app shared by the Go tests and the
// end-to-end server the TypeScript client is tested against.
package testapp

import (
	"context"
	"time"

	"github.com/golaravel/golaravel/pkg/offline"
	"github.com/golaravel/golaravel/pkg/offline/internal/testent"
	"github.com/golaravel/golaravel/pkg/offline/internal/testent/product"
	"github.com/golaravel/golaravel/pkg/privacy"
)

// Wire attaches tenant scoping and the sync change hook to the client.
func Wire(client *testent.Client, eng *offline.Engine[*testent.Tx]) {
	client.Product.Intercept(privacy.TenantScope(func(q *testent.ProductQuery, tenant string) {
		q.Where(product.TenantID(tenant))
	}))
	client.Product.Use(privacy.TenantMutationHook())
	client.Product.Use(offline.ChangeHook(eng, "product"))
}

// ToRecord converts a product to a sync record.
func ToRecord(p *testent.Product) *offline.Record {
	return &offline.Record{ID: p.ID, Version: p.SyncVersion, HLC: p.SyncHlc, FieldClocks: p.SyncClocks,
		Deleted: p.DeletedAt != nil,
		Data:    map[string]any{"name": p.Name, "price": p.Price, "stock": p.Stock}}
}

// Product is a synced resource for tests: products with a name, a price
// and a stock counter. beforeApply, when set, runs first in every Apply
// and can fail it.
func Product(beforeApply func() error) offline.Resource[*testent.Tx] {
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
			return ToRecord(p), nil
		},
		Snapshot: func(ctx context.Context, tx *testent.Tx, after string, limit int) ([]*offline.Record, error) {
			ps, err := tx.Product.Query().Where(product.IDGT(after), product.DeletedAtIsNil()).
				Order(product.ByID()).Limit(limit).All(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]*offline.Record, len(ps))
			for i, p := range ps {
				out[i] = ToRecord(p)
			}
			return out, nil
		},
		Apply: func(ctx context.Context, tx *testent.Tx, w *offline.Write) error {
			if beforeApply != nil {
				if err := beforeApply(); err != nil {
					return err
				}
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
					u.SetStock(*f.Stock + w.Inc["stock"])
				} else if d, ok := w.Inc["stock"]; ok {
					u.AddStock(d)
				}
				return u.Exec(ctx)
			}
		},
	}
}
