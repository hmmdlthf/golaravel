package offline

import (
	"context"
	"errors"
	"math"
	"strings"
)

// Push applies a batch of mutations, oldest first, and returns one Result
// per mutation.
//
// Each mutation runs in its own transaction, and adjacent mutations that
// share a Group run in one transaction together. Each one is either
// applied or rejected as a whole. A mutation id that was already processed
// comes back as a duplicate with its original outcome and changes nothing.
// A transient failure (lost connection, deadlock) stops the batch: that
// unit and everything after it come back as "retry", so the client
// re-sends them in order.
func (e *Engine[T]) Push(ctx context.Context, p Principal, req *PushRequest) (*PushResponse, error) {
	if p.TenantID == "" {
		return nil, ErrNoTenant
	}
	if len(req.Mutations) > e.opts.MaxBatch {
		return nil, ErrBatchTooLarge
	}
	ctx = scope(ctx, p)
	var dev *Device
	if err := e.inTx(ctx, func(tx T) error {
		var err error
		if dev, err = e.checkDevice(ctx, tx, p.TenantID, req.DeviceID, true); err != nil {
			return err
		}
		return e.store.touchPush(ctx, tx, dev.ID, e.now())
	}); err != nil {
		return nil, err
	}

	muts := req.Mutations
	results := make([]Result, len(muts))
	for i := 0; i < len(muts); {
		j := i + 1
		if g := muts[i].Group; g != "" {
			for j < len(muts) && muts[j].Group == g {
				j++
			}
		}
		if e.pushUnit(ctx, p, dev, muts[i:j], results[i:j]) {
			for k := j; k < len(muts); k++ {
				results[k] = Result{ID: muts[k].ID, Status: StatusRetry, Reason: "blocked",
					Entity: muts[k].Entity, EntityID: muts[k].EntityID}
			}
			break
		}
		i = j
	}
	e.attachRecords(ctx, muts, results)
	return &PushResponse{Results: results, ServerHLC: e.clock.Now().String()}, nil
}

// pushUnit applies one mutation or group. It reports whether the batch
// must stop (transient failure).
func (e *Engine[T]) pushUnit(ctx context.Context, p Principal, dev *Device, unit []Mutation, out []Result) (stop bool) {
	for k := range unit {
		out[k] = Result{ID: unit[k].ID, Entity: unit[k].Entity, EntityID: unit[k].EntityID}
	}
	// Malformed mutations are rejected without touching the database;
	// the same input would fail the same way on every retry.
	for k := range unit {
		if err := e.validate(&unit[k]); err != nil {
			e.rejectUnit(out, k, reasonOf(err))
			return false
		}
	}

	failed := -1
	err := e.inTx(ctx, func(tx T) error {
		if err := e.store.lockTenant(ctx, tx, p.TenantID); err != nil {
			return err
		}
		for k := range unit {
			m := &unit[k]
			prev, err := e.store.processed(ctx, tx, p.TenantID, m.ID)
			if err != nil {
				failed = k
				return err
			}
			if prev != nil {
				out[k].Status, out[k].Original, out[k].Seq, out[k].Reason = StatusDuplicate, prev.Status, prev.Seq, prev.Reason
				continue
			}
			r, err := e.applyOne(ctx, tx, p, dev, m)
			if err != nil {
				failed = k
				return err
			}
			out[k].Status, out[k].Seq, out[k].Reason = r.Status, r.Seq, r.Reason
		}
		return nil
	})
	if err == nil {
		return false
	}

	if e.opts.Classify(err) == Transient {
		e.log.Warn("offline: transient failure, client will retry", "tenant", p.TenantID, "device", dev.ID, "error", err)
		for k := range out {
			out[k].Status, out[k].Seq, out[k].Original, out[k].Reason = StatusRetry, 0, "", "transient"
		}
		return true
	}
	var rej *RejectError
	if !errors.As(err, &rej) {
		e.log.Error("offline: mutation failed", "tenant", p.TenantID, "device", dev.ID, "error", err)
	}
	if failed < 0 {
		failed = 0
	}
	e.rejectUnit(out, failed, reasonOf(err))

	// Remember the rejection so a retried id gets the same answer. This
	// runs in a fresh transaction because the unit's was rolled back.
	if rerr := e.inTx(ctx, func(tx T) error {
		for k := range unit {
			if out[k].Status != StatusRejected {
				continue
			}
			m := &unit[k]
			if err := e.store.insertLedgerIgnore(ctx, tx, &ledgerRow{TenantID: p.TenantID, MutationID: m.ID,
				DeviceID: dev.ID, UserID: p.UserID, Entity: m.Entity, EntityID: m.EntityID, Op: m.Op, HLC: m.HLC,
				Group: m.Group, Status: StatusRejected, Reason: out[k].Reason, Payload: mustJSON(m), CreatedAt: e.now()}); err != nil {
				return err
			}
		}
		return nil
	}); rerr != nil {
		e.log.Warn("offline: could not record rejection", "tenant", p.TenantID, "error", rerr)
	}
	return false
}

// rejectUnit marks every non-duplicate member rejected; members other
// than the one that failed are rejected because their group was.
func (e *Engine[T]) rejectUnit(out []Result, failed int, reason string) {
	for k := range out {
		if out[k].Status == StatusDuplicate {
			continue
		}
		out[k].Status, out[k].Seq, out[k].Reason = StatusRejected, 0, reason
		if k != failed {
			out[k].Reason = "group_rejected: " + reason
		}
	}
}

var reserved = map[string]bool{"id": true, "tenant_id": true, "deleted_at": true}

func (e *Engine[T]) validate(m *Mutation) error {
	if !ValidMutationID(m.ID) {
		return Rejectf("invalid", "mutation id must be a ULID or UUID")
	}
	res, ok := e.resources[m.Entity]
	if !ok {
		return Rejectf("unknown_entity", "%q is not synced", m.Entity)
	}
	if !validKey(m.EntityID) {
		return Rejectf("invalid", "bad entity_id")
	}
	if !m.Op.valid() {
		return Rejectf("invalid", "op must be create, update or delete")
	}
	if m.Group != "" && !validKey(m.Group) {
		return Rejectf("invalid", "bad group")
	}
	if h, err := ParseHLC(m.HLC); err != nil || h.IsZero() {
		return Rejectf("invalid", "bad hlc")
	}
	for f := range m.Set {
		if reserved[f] || strings.HasPrefix(f, "sync_") || (res.fields != nil && !res.fields[f]) {
			return Rejectf("forbidden_field", "field %q may not be set", f)
		}
	}
	for f, v := range m.Inc {
		if !res.counters[f] {
			return Rejectf("forbidden_field", "field %q is not a counter", f)
		}
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return Rejectf("invalid", "bad delta for %q", f)
		}
	}
	return nil
}

// applyOne resolves and writes one mutation inside the unit's transaction.
func (e *Engine[T]) applyOne(ctx context.Context, tx T, p Principal, dev *Device, m *Mutation) (*Result, error) {
	res := e.resources[m.Entity]
	cur, err := res.Load(ctx, tx, m.EntityID)
	if err != nil {
		return nil, err
	}
	if m.Op == OpUpdate && cur == nil {
		return nil, Rejectf("not_found", "%s %s does not exist", m.Entity, m.EntityID)
	}
	if res.Authorize != nil {
		if err := res.Authorize(ctx, p, m, cur); err != nil {
			var rej *RejectError
			if !errors.As(err, &rej) {
				err = Reject("forbidden", err)
			}
			return nil, err
		}
	}

	hlc, _ := ParseHLC(m.HLC)
	var notes []string
	if hlc.Wall > e.opts.Now().Add(e.opts.MaxDrift).UnixMilli() {
		// The device clock is too far ahead; use the server's time.
		srv := e.clock.Now()
		hlc = HLC{Wall: srv.Wall, Count: srv.Count, Node: dev.ID}
		notes = append(notes, "clock_skew")
	} else {
		e.clock.Observe(hlc)
	}

	d, err := res.Resolver.Resolve(ctx, &Conflict{Mutation: m, HLC: hlc, Current: cur})
	if err != nil {
		return nil, err
	}
	if d == nil {
		d = &Decision{Skip: true, Note: "no_change"}
	}
	if d.Note != "" {
		notes = append(notes, d.Note)
	}
	row := &ledgerRow{TenantID: p.TenantID, MutationID: m.ID, DeviceID: dev.ID, UserID: p.UserID, Entity: m.Entity,
		EntityID: m.EntityID, Op: m.Op, HLC: hlc.String(), Group: m.Group, Status: StatusApplied,
		Payload: mustJSON(m), CreatedAt: e.now()}
	if len(notes) > 0 {
		row.Status, row.Reason = StatusResolved, strings.Join(notes, ",")
	}

	if !d.Skip {
		op := d.Op
		if op == "" {
			op = m.Op
		}
		if op != OpDelete {
			if cur == nil || cur.Deleted {
				op = OpCreate
			} else {
				op = OpUpdate
			}
		}
		w := &Write{Op: op, EntityID: m.EntityID, Set: d.Set, Inc: d.Inc, Version: 1, HLC: hlc.String(),
			FieldClocks: map[string]string{}, Current: cur, Mutation: m, Principal: p}
		if cur != nil {
			w.Version = cur.Version + 1
			w.HLC = MaxHLC(cur.HLC, hlc.String())
			if !cur.Deleted {
				for f, c := range cur.FieldClocks {
					w.FieldClocks[f] = c
				}
			}
		}
		for f := range d.Set {
			w.FieldClocks[f] = hlc.String()
		}
		if err := res.Apply(ctx, tx, w); err != nil {
			return nil, err
		}
		if row.Seq, err = e.store.nextSeq(ctx, tx, p.TenantID); err != nil {
			return nil, err
		}
	}
	// A skipped mutation keeps seq 0: it changed nothing, so pulls ignore it.
	if err := e.store.insertLedger(ctx, tx, row); err != nil {
		return nil, err
	}
	return &Result{Status: row.Status, Seq: row.Seq, Reason: row.Reason}, nil
}

// attachRecords adds the server's current state to results the client
// cannot settle on its own: resolved and rejected mutations (and replays
// of them), so it can replace its optimistic local copy.
func (e *Engine[T]) attachRecords(ctx context.Context, muts []Mutation, results []Result) {
	want := map[string][]int{}
	for i := range results {
		r := &results[i]
		st := r.Status
		if st == StatusDuplicate {
			st = r.Original
		}
		if st != StatusResolved && st != StatusRejected {
			continue
		}
		if _, ok := e.resources[muts[i].Entity]; !ok || !validKey(muts[i].EntityID) {
			continue
		}
		want[muts[i].Entity] = append(want[muts[i].Entity], i)
	}
	if len(want) == 0 {
		return
	}
	err := e.inTx(ctx, func(tx T) error {
		for entity, idx := range want {
			ids := make([]string, 0, len(idx))
			for _, i := range idx {
				ids = append(ids, muts[i].EntityID)
			}
			recs, err := e.loadMany(ctx, tx, e.resources[entity], ids)
			if err != nil {
				return err
			}
			for _, i := range idx {
				rec := recs[muts[i].EntityID]
				if rec == nil {
					rec = &Record{ID: muts[i].EntityID, Deleted: true}
				}
				results[i].Record = rec
			}
		}
		return nil
	})
	if err != nil {
		e.log.Warn("offline: could not load records for push results", "error", err)
	}
}

func (e *Engine[T]) loadMany(ctx context.Context, tx T, res *resource[T], ids []string) (map[string]*Record, error) {
	if res.LoadMany != nil {
		return res.LoadMany(ctx, tx, ids)
	}
	out := make(map[string]*Record, len(ids))
	for _, id := range ids {
		if _, seen := out[id]; seen {
			continue
		}
		r, err := res.Load(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		if r != nil {
			out[id] = r
		}
	}
	return out, nil
}
