package offline

import (
	"context"
	"encoding/json"
)

// Conflict is what a resolver decides on: an incoming mutation and the
// entity's current state.
type Conflict struct {
	Mutation *Mutation
	// HLC is the mutation's effective timestamp (re-stamped by the server
	// when the device clock was too far ahead).
	HLC     HLC
	Current *Record // nil when the entity does not exist
}

// Decision says what to write. A Decision with Skip set changes nothing.
type Decision struct {
	Op  Op
	Set map[string]json.RawMessage
	Inc map[string]float64
	// Skip means nothing is written; the mutation is still acknowledged.
	Skip bool
	// Note explains a change to what the client sent; a non-empty Note
	// makes the result "resolved" instead of "applied".
	Note string
}

// Resolver settles an incoming mutation against the current state. Return
// Reject(...) to refuse the mutation outright.
type Resolver interface {
	Resolve(ctx context.Context, c *Conflict) (*Decision, error)
}

// ResolverFunc adapts a function to Resolver.
type ResolverFunc func(ctx context.Context, c *Conflict) (*Decision, error)

func (f ResolverFunc) Resolve(ctx context.Context, c *Conflict) (*Decision, error) { return f(ctx, c) }

// Built-in strategies. All of them add up Inc deltas, which commute, and
// treat deletes the same way:
//
//   - update of a deleted entity: skipped (deletes win over edits)
//   - create of a deleted entity: restores it if newer than the delete
//   - delete of a missing/deleted entity: skipped
//   - delete of an entity edited later than the delete: skipped
var (
	// FieldLWW keeps, per field, the value with the latest timestamp, so
	// two devices editing different fields both keep their changes.
	FieldLWW Resolver = ResolverFunc(fieldLWW)
	// LWW keeps whole-row last-write-wins: an older mutation's fields are
	// all dropped.
	LWW Resolver = ResolverFunc(rowLWW)
	// ServerWins drops the client's fields when the entity changed after
	// the version the client based its edit on.
	ServerWins Resolver = ResolverFunc(serverWins)
	// ClientWins applies whatever arrives last at the server.
	ClientWins Resolver = ResolverFunc(clientWins)
)

func fieldLWW(_ context.Context, c *Conflict) (*Decision, error) {
	if d, done := common(c); done {
		return d, nil
	}
	cur := c.Current
	d := &Decision{Op: c.Mutation.Op, Set: map[string]json.RawMessage{}, Inc: c.Mutation.Inc}
	if cur == nil || cur.Deleted {
		d.Set = c.Mutation.Set
		return d, nil
	}
	rowClock, _ := ParseHLC(cur.HLC)
	var dropped []string
	for f, v := range c.Mutation.Set {
		clock := rowClock
		if s, ok := cur.FieldClocks[f]; ok {
			clock, _ = ParseHLC(s)
		}
		if c.HLC.After(clock) {
			d.Set[f] = v
		} else {
			dropped = append(dropped, f)
		}
	}
	return finish(d, dropped), nil
}

func rowLWW(_ context.Context, c *Conflict) (*Decision, error) {
	if d, done := common(c); done {
		return d, nil
	}
	d := &Decision{Op: c.Mutation.Op, Set: c.Mutation.Set, Inc: c.Mutation.Inc}
	if c.Current != nil && !c.Current.Deleted {
		clock, _ := ParseHLC(c.Current.HLC)
		if !c.HLC.After(clock) {
			return finish(&Decision{Op: c.Mutation.Op, Inc: c.Mutation.Inc}, keys(c.Mutation.Set)), nil
		}
	}
	return d, nil
}

func serverWins(_ context.Context, c *Conflict) (*Decision, error) {
	if d, done := common(c); done {
		return d, nil
	}
	if c.Current != nil && !c.Current.Deleted && c.Current.Version > c.Mutation.BaseVersion {
		return finish(&Decision{Op: c.Mutation.Op, Inc: c.Mutation.Inc}, keys(c.Mutation.Set)), nil
	}
	return &Decision{Op: c.Mutation.Op, Set: c.Mutation.Set, Inc: c.Mutation.Inc}, nil
}

func clientWins(_ context.Context, c *Conflict) (*Decision, error) {
	if d, done := common(c); done {
		return d, nil
	}
	return &Decision{Op: c.Mutation.Op, Set: c.Mutation.Set, Inc: c.Mutation.Inc}, nil
}

// common handles the delete and tombstone cases every strategy shares.
func common(c *Conflict) (*Decision, bool) {
	m, cur := c.Mutation, c.Current
	switch m.Op {
	case OpDelete:
		if cur == nil || cur.Deleted {
			return &Decision{Skip: true, Note: "already_deleted"}, true
		}
		clock, _ := ParseHLC(cur.HLC)
		if !c.HLC.After(clock) {
			return &Decision{Skip: true, Note: "superseded"}, true
		}
		return &Decision{Op: OpDelete}, true
	case OpUpdate:
		if cur != nil && cur.Deleted {
			return &Decision{Skip: true, Note: "deleted"}, true
		}
	case OpCreate:
		if cur != nil && cur.Deleted {
			clock, _ := ParseHLC(cur.HLC)
			if !c.HLC.After(clock) {
				return &Decision{Skip: true, Note: "deleted"}, true
			}
		}
	}
	return nil, false
}

func finish(d *Decision, dropped []string) *Decision {
	if len(dropped) > 0 {
		d.Note = "stale_fields"
	}
	if len(d.Set) == 0 && len(d.Inc) == 0 && d.Op != OpDelete {
		d.Skip = true
		if d.Note == "" {
			d.Note = "no_change"
		}
	}
	return d
}

func keys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
