package offline_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golaravel/golaravel/pkg/offline"
)

func TestCompaction(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	start := e.now
	e.now = start.Add(-40 * 24 * time.Hour)

	p, a := e.device("acme")
	_, b := e.device("acme")
	_, idle := e.device("acme")
	c := newClock(a.ID, e.now)
	for _, id := range []string{"p1", "p2", "p3", "p4"} {
		wantStatus(t, e.push(p, a, create(c, id, set("name", id))), offline.StatusApplied) // seq 1..4
	}
	wantStatus(t, e.push(p, a, update(c, "p1", set("price", -1))), offline.StatusRejected) // seq 0

	pull := func(d *offline.Device, cursor int64) (*offline.PullResponse, error) {
		return e.eng.Pull(ctx, p, d.ID, cursor, 0)
	}
	// Forty days later, a has everything and b only up to seq 2; the idle
	// device has not been seen since it registered.
	e.now = start
	c.wall = e.now.UnixMilli()
	if _, err := pull(a, 4); err != nil {
		t.Fatal(err)
	}
	if _, err := pull(b, 2); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, e.push(p, a, create(c, "p5", set("name", "new"))), offline.StatusApplied) // seq 5, recent

	r, err := e.eng.Compact(ctx, offline.CompactOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// b holds history back at 2; the idle device does not.
	if r.Tenants != 1 || r.Deleted != 3 || r.Horizon["acme"] != 2 {
		t.Fatalf("first compaction: %+v", r)
	}
	if n := e.ledgerCount("acme"); n != 3 {
		t.Fatalf("ledger rows left: %d", n)
	}
	if res, err := pull(b, 2); err != nil || len(res.Changes) != 3 {
		t.Fatalf("b continues from its cursor: %+v %v", res, err)
	}
	if _, err := pull(idle, 0); !errors.Is(err, offline.ErrResyncRequired) {
		t.Fatalf("idle device must resync: %v", err)
	}

	// Everyone catches up; rows younger than Retain stay.
	if _, err := pull(a, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := pull(b, 5); err != nil {
		t.Fatal(err)
	}
	r, err = e.eng.Compact(ctx, offline.CompactOptions{})
	if err != nil || r.Deleted != 2 || r.Horizon["acme"] != 4 {
		t.Fatalf("second compaction: %+v %v", r, err)
	}
	if res, err := pull(a, 4); err != nil || len(res.Changes) != 1 || res.Changes[0].EntityID != "p5" {
		t.Fatalf("p5 must survive: %+v %v", res, err)
	}

	// Nothing more to do; the horizon never goes back.
	r, err = e.eng.Compact(ctx, offline.CompactOptions{Tenant: "acme"})
	if err != nil || r.Deleted != 0 {
		t.Fatalf("third compaction: %+v %v", r, err)
	}
	if _, err := pull(a, 3); !errors.Is(err, offline.ErrResyncRequired) {
		t.Fatalf("horizon moved back: %v", err)
	}
}

func TestCompactCommand(t *testing.T) {
	e := newEnv(t)
	p, d := e.device("acme")
	c := newClock(d.ID, e.now)
	e.push(p, d, create(c, "p1", set("name", "x")))
	if err := e.eng.CompactCLI([]string{"--retain=1h", "--tenant=acme"}); err != nil {
		t.Fatal(err)
	}
	if err := e.eng.CompactCLI([]string{"--retain=soon"}); err == nil {
		t.Fatal("bad flag accepted")
	}
	if err := e.eng.CompactTask(offline.CompactOptions{})(context.Background()); err != nil {
		t.Fatal(err)
	}
}
