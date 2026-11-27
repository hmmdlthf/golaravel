package offline

import (
	"context"
	"fmt"
	"strings"

	"entgo.io/ent"

	"github.com/golaravel/golaravel/pkg/privacy"
)

// ChangeHook records writes made outside sync (admin panel, jobs, other
// APIs) in the ledger, so devices pull them too. Attach it to every synced
// Ent schema:
//
//	client.Product.Use(offline.ChangeHook(eng, "product"))
//
// Inside an Ent transaction the ledger row is written in that transaction,
// so it commits or rolls back with the change. Outside one, it is written
// right after the change commits. The engine's own writes are skipped
// (they are already in the ledger). The tenant comes from the context
// (privacy.WithTenantID) or the mutation's tenant_id field; a write with
// neither fails, rather than leaving devices out of date.
//
// Synced entities need string ids (see schema.SyncableMixin).
func ChangeHook[T Tx](e *Engine[T], entity string) ent.Hook {
	return func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if Applying(ctx) {
				return next.Mutate(ctx, m)
			}
			tenant := tenantOf(ctx, m)
			if tenant == "" {
				return nil, fmt.Errorf("offline: %s write has no tenant to record it under: %w", entity, ErrNoTenant)
			}
			var ids []string
			if !m.Op().Is(ent.OpCreate) {
				im, ok := m.(interface {
					IDs(context.Context) ([]string, error)
				})
				if !ok {
					return nil, fmt.Errorf("offline: %s mutation has no string IDs; synced entities need string ids", entity)
				}
				var err error
				if ids, err = im.IDs(ctx); err != nil {
					return nil, err
				}
				if len(ids) == 0 {
					return next.Mutate(ctx, m)
				}
			}
			tx, inTx := txOf[T](m)
			if inTx {
				// Lock before writing the entity, in the same order as Push,
				// so the two cannot deadlock each other.
				if err := e.store.lockTenant(ctx, tx, tenant); err != nil {
					return nil, err
				}
			}
			if err := e.stamp(ctx, m); err != nil {
				return nil, err
			}
			v, err := next.Mutate(ctx, m)
			if err != nil {
				return v, err
			}
			if m.Op().Is(ent.OpCreate) {
				im, ok := m.(interface{ ID() (string, bool) })
				if !ok {
					return v, fmt.Errorf("offline: %s mutation has no string ID; synced entities need string ids", entity)
				}
				id, ok := im.ID()
				if !ok {
					return v, fmt.Errorf("offline: created %s has no id", entity)
				}
				ids = []string{id}
			}
			op := OpUpdate
			switch {
			case m.Op().Is(ent.OpCreate):
				op = OpCreate
			case m.Op().Is(ent.OpDelete | ent.OpDeleteOne):
				op = OpDelete
			}
			if inTx {
				return v, e.Record(ctx, tx, tenant, entity, op, ids...)
			}
			return v, e.inTx(ctx, func(t T) error { return e.Record(ctx, t, tenant, entity, op, ids...) })
		})
	}
}

// Record writes a ledger entry for a change made without Ent hooks (raw
// SQL, bulk imports), so devices pull it. Pass the transaction the change
// was made in.
func (e *Engine[T]) Record(ctx context.Context, x ExecQuerier, tenant, entity string, op Op, ids ...string) error {
	if tenant == "" {
		return ErrNoTenant
	}
	if err := e.store.lockTenant(ctx, x, tenant); err != nil {
		return err
	}
	for _, id := range ids {
		seq, err := e.store.nextSeq(ctx, x, tenant)
		if err != nil {
			return err
		}
		if err := e.store.insertLedger(ctx, x, &ledgerRow{TenantID: tenant, MutationID: NewID(), Seq: seq,
			DeviceID: "server", Entity: entity, EntityID: id, Op: op, HLC: e.clock.Now().String(),
			Status: StatusApplied, CreatedAt: e.now()}); err != nil {
			return err
		}
	}
	return nil
}

func tenantOf(ctx context.Context, m ent.Mutation) string {
	if t, ok := privacy.TenantFromContext(ctx); ok && t != "" {
		return t
	}
	if v, ok := m.Field("tenant_id"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// txOf returns the transaction an Ent mutation runs in, if any.
func txOf[T Tx](m ent.Mutation) (T, bool) {
	var zero T
	tm, ok := m.(interface{ Tx() (T, error) })
	if !ok {
		return zero, false
	}
	tx, err := tm.Tx()
	if err != nil {
		return zero, false
	}
	return tx, true
}

// stamp gives a write made outside sync the server's clock, so an older
// offline edit cannot overwrite it under last-write-wins. It sets the
// SyncableMixin fields when the schema has them:
//
//   - create: sync_hlc now, sync_version 1
//   - update one: sync_hlc now, version + 1, and the changed fields'
//     clocks now (other fields keep theirs)
//   - update many: sync_hlc now, version + 1, and the field clocks cleared,
//     so every field falls back to the row clock (per-row old clocks are
//     not known here)
func (e *Engine[T]) stamp(ctx context.Context, m ent.Mutation) error {
	if m.Op().Is(ent.OpDelete | ent.OpDeleteOne) {
		return nil // hard delete: nothing left to stamp
	}
	if _, ok := m.Field("sync_hlc"); ok {
		return nil // set explicitly by the caller
	}
	now := e.clock.Now().String()
	if err := m.SetField("sync_hlc", now); err != nil {
		return nil // not a SyncableMixin schema
	}
	if m.Op().Is(ent.OpCreate) {
		if _, ok := m.Field("sync_version"); !ok {
			_ = m.SetField("sync_version", int64(1))
		}
		return nil
	}
	_ = m.AddField("sync_version", int64(1))
	if m.Op().Is(ent.OpUpdate) {
		_ = m.ClearField("sync_clocks")
		return nil
	}
	clocks := map[string]string{}
	if old, err := m.OldField(ctx, "sync_clocks"); err == nil {
		if o, ok := old.(map[string]string); ok {
			for k, v := range o {
				clocks[k] = v
			}
		}
	}
	for _, f := range m.Fields() {
		if !strings.HasPrefix(f, "sync_") {
			clocks[f] = now
		}
	}
	return m.SetField("sync_clocks", clocks)
}
