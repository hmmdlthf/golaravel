package offline

import (
	"context"
	"sort"
)

// Pull returns entities changed after cursor, each once with its current
// state (or a tombstone), oldest change first. Clients store Cursor and
// call again while HasMore is set. Passing a cursor also acknowledges it,
// which is what history compaction goes by.
//
// It returns ErrResyncRequired when cursor is older than the history the
// server keeps; the client then reloads with Snapshot.
func (e *Engine[T]) Pull(ctx context.Context, p Principal, deviceID string, cursor int64, limit int) (*PullResponse, error) {
	if p.TenantID == "" {
		return nil, ErrNoTenant
	}
	if cursor < 0 {
		cursor = 0
	}
	if limit <= 0 || limit > e.opts.MaxPull {
		limit = e.opts.MaxPull
	}
	ctx = scope(ctx, p)
	out := &PullResponse{Changes: []Change{}, Cursor: cursor}
	err := e.inTx(ctx, func(tx T) error {
		dev, err := e.checkDevice(ctx, tx, p.TenantID, deviceID, true)
		if err != nil {
			return err
		}
		_, horizon, err := e.store.tenantState(ctx, tx, p.TenantID)
		if err != nil {
			return err
		}
		if cursor < horizon {
			return ErrResyncRequired
		}
		rows, err := e.store.changes(ctx, tx, p.TenantID, cursor, limit+1)
		if err != nil {
			return err
		}
		if len(rows) > limit {
			rows, out.HasMore = rows[:limit], true
		}
		byEntity := map[string][]string{}
		for _, r := range rows {
			byEntity[r.Entity] = append(byEntity[r.Entity], r.EntityID)
		}
		loaded := map[string]map[string]*Record{}
		names := make([]string, 0, len(byEntity))
		for n := range byEntity {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			res, ok := e.resources[n]
			if !ok {
				continue // no longer synced
			}
			recs, err := e.loadMany(ctx, tx, res, byEntity[n])
			if err != nil {
				return err
			}
			loaded[n] = recs
		}
		for _, r := range rows {
			out.Cursor = r.Seq
			recs, ok := loaded[r.Entity]
			if !ok {
				continue
			}
			c := Change{Entity: r.Entity, EntityID: r.EntityID, Seq: r.Seq}
			// Missing means gone or no longer visible to this tenant/user:
			// either way the device should drop it.
			if rec := recs[r.EntityID]; rec == nil || rec.Deleted {
				c.Deleted = true
				if rec != nil {
					c.Version, c.HLC = rec.Version, rec.HLC
				}
			} else {
				c.Version, c.HLC, c.Data = rec.Version, rec.HLC, rec.Data
			}
			out.Changes = append(out.Changes, c)
		}
		return e.store.touchPull(ctx, tx, dev.ID, cursor, e.now())
	})
	if err != nil {
		return nil, err
	}
	out.ServerHLC = e.clock.Now().String()
	return out, nil
}

// Snapshot returns a page of an entity's live records, for a new device or
// a resync. The first page (after == "") carries the cursor to pull from
// once every entity has been loaded; changes made while paging are pulled
// again from there, which is harmless.
func (e *Engine[T]) Snapshot(ctx context.Context, p Principal, deviceID, entity, after string, limit int) (*SnapshotResponse, error) {
	if p.TenantID == "" {
		return nil, ErrNoTenant
	}
	res, ok := e.resources[entity]
	if !ok || res.Snapshot == nil {
		return nil, ErrUnknownEntity
	}
	if limit <= 0 || limit > e.opts.MaxPull {
		limit = e.opts.MaxPull
	}
	ctx = scope(ctx, p)
	out := &SnapshotResponse{Entity: entity, Records: []*Record{}}
	err := e.inTx(ctx, func(tx T) error {
		if _, err := e.checkDevice(ctx, tx, p.TenantID, deviceID, true); err != nil {
			return err
		}
		if after == "" {
			// Read the sequence before the rows: anything committed after
			// this point is pulled again later.
			seq, _, err := e.store.tenantState(ctx, tx, p.TenantID)
			if err != nil {
				return err
			}
			out.Cursor = seq
		}
		recs, err := res.Snapshot(ctx, tx, after, limit+1)
		if err != nil {
			return err
		}
		if len(recs) > limit {
			recs, out.HasMore = recs[:limit], true
		}
		for _, r := range recs {
			if r != nil && !r.Deleted {
				out.Records = append(out.Records, r)
			}
		}
		if out.HasMore && len(recs) > 0 {
			out.Next = recs[len(recs)-1].ID
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
