package offline

import (
	"context"
	"flag"
	"fmt"
	"time"
)

// CompactOptions control how much ledger history Compact keeps.
type CompactOptions struct {
	// Retain keeps every ledger row younger than this (default 30 days).
	// Rows double as the idempotency record, so Retain must be longer
	// than any device may hold a pushed-but-unacknowledged mutation: a
	// mutation retried after its row is gone is applied again.
	Retain time.Duration
	// ActiveWithin: devices that have not pulled for this long (default
	// 30 days) no longer hold history back; they resync when they return.
	ActiveWithin time.Duration
	// Tenant limits compaction to one tenant (default: all).
	Tenant string
}

// CompactResult reports what Compact removed.
type CompactResult struct {
	Tenants int              `json:"tenants"`
	Deleted int64            `json:"deleted"`
	Horizon map[string]int64 `json:"horizon"` // new horizon per compacted tenant
}

// Compact deletes ledger history that no active device still needs: rows
// older than Retain, at or below the oldest cursor among the tenant's
// active devices. It then raises the tenant's horizon; devices whose
// cursor is below it get ErrResyncRequired (410) and reload with Snapshot.
//
// Schedule it daily:
//
//	schedule.Call(eng.CompactTask(offline.CompactOptions{})).Daily()
func (e *Engine[T]) Compact(ctx context.Context, opts CompactOptions) (*CompactResult, error) {
	if opts.Retain <= 0 {
		opts.Retain = 30 * 24 * time.Hour
	}
	if opts.ActiveWithin <= 0 {
		opts.ActiveWithin = 30 * 24 * time.Hour
	}
	now := e.opts.Now()
	before := now.Add(-opts.Retain).UnixMilli()
	activeSince := now.Add(-opts.ActiveWithin).UnixMilli()

	tenants := []string{opts.Tenant}
	if opts.Tenant == "" {
		if err := e.inTx(ctx, func(tx T) error {
			var err error
			tenants, err = e.store.tenantIDs(ctx, tx)
			return err
		}); err != nil {
			return nil, err
		}
	}
	out := &CompactResult{Horizon: map[string]int64{}}
	for _, t := range tenants {
		n, h, err := e.compactTenant(ctx, t, before, activeSince)
		if err != nil {
			return out, fmt.Errorf("offline: compact %s: %w", t, err)
		}
		out.Tenants++
		out.Deleted += n
		if h > 0 {
			out.Horizon[t] = h
		}
	}
	return out, nil
}

func (e *Engine[T]) compactTenant(ctx context.Context, tenant string, before, activeSince int64) (int64, int64, error) {
	// top is the highest seq that goes: at or below every active device's
	// cursor, and old enough. Rows with seq 0 (rejections, no-ops) are
	// never pulled, so they only need to be old enough.
	var top int64
	if err := e.inTx(ctx, func(tx T) error {
		seq, _, err := e.store.tenantState(ctx, tx, tenant)
		if err != nil {
			return err
		}
		keep, ok, err := e.store.minLiveCursor(ctx, tx, tenant, activeSince)
		if err != nil {
			return err
		}
		if !ok {
			keep = seq // no active devices: all of it may go
		}
		top, err = e.store.purgeTop(ctx, tx, tenant, keep, before)
		return err
	}); err != nil {
		return 0, 0, err
	}
	// Raise the horizon before deleting, in its own transaction. Pull
	// reads the horizon after reading changes, so a pull that could have
	// missed a deleted row is sure to see the new horizon and resync.
	if top > 0 {
		if err := e.inTx(ctx, func(tx T) error { return e.store.raiseHorizon(ctx, tx, tenant, top) }); err != nil {
			return 0, 0, err
		}
	}
	var n int64
	err := e.inTx(ctx, func(tx T) error {
		var err error
		n, err = e.store.purgeLedger(ctx, tx, tenant, top, before)
		return err
	})
	return n, top, err
}

// CompactTask returns Compact as a scheduler action:
// schedule.Call(eng.CompactTask(opts)).Daily().
func (e *Engine[T]) CompactTask(opts CompactOptions) func(context.Context) error {
	return func(ctx context.Context) error {
		r, err := e.Compact(ctx, opts)
		if err == nil {
			e.log.Info("offline: compacted sync ledger", "tenants", r.Tenants, "deleted", r.Deleted)
		}
		return err
	}
}

// CompactCLI runs Compact from command-line arguments (--retain=720h
// --active-within=720h --tenant=acme). Register it as an Artisan command:
//
//	registry.Register(&artisan.Command{Name: "offline:compact",
//		Description: "Delete sync ledger history no active device needs", Run: eng.CompactCLI})
func (e *Engine[T]) CompactCLI(args []string) error {
	fs := flag.NewFlagSet("offline:compact", flag.ContinueOnError)
	var opts CompactOptions
	fs.DurationVar(&opts.Retain, "retain", 0, "keep rows younger than this (default 720h)")
	fs.DurationVar(&opts.ActiveWithin, "active-within", 0, "devices idle longer must resync (default 720h)")
	fs.StringVar(&opts.Tenant, "tenant", "", "only this tenant")
	if err := fs.Parse(args); err != nil {
		return err
	}
	r, err := e.Compact(context.Background(), opts)
	if err != nil {
		return err
	}
	fmt.Printf("Compacted %d tenant(s), deleted %d ledger row(s).\n", r.Tenants, r.Deleted)
	return nil
}
