package offline_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golaravel/golaravel/pkg/database"
	"github.com/golaravel/golaravel/pkg/offline"
	"github.com/golaravel/golaravel/pkg/privacy"
)

func TestHLC(t *testing.T) {
	a := offline.HLC{Wall: 1700000000000, Count: 3, Node: "a"}
	s := a.String()
	if s != "001700000000000:00003:a" {
		t.Fatalf("encoded %q", s)
	}
	b, err := offline.ParseHLC(s)
	if err != nil || b != a {
		t.Fatalf("round trip: %v %v", b, err)
	}
	later := []offline.HLC{{Wall: 1700000000000, Count: 3, Node: "b"}, {Wall: 1700000000000, Count: 4, Node: "a"}, {Wall: 1700000000001, Node: "a"}}
	prev := a
	for _, h := range later {
		if !h.After(prev) || !(h.String() > prev.String()) {
			t.Fatalf("%v should sort after %v, as values and as text", h, prev)
		}
		prev = h
	}
	for _, bad := range []string{"x", "1:2:3", "001700000000000:0000a:n", "001700000000000:00001:"} {
		if _, err := offline.ParseHLC(bad); err == nil {
			t.Errorf("ParseHLC(%q) should fail", bad)
		}
	}

	now := time.UnixMilli(1000)
	c := offline.NewClock("srv", func() time.Time { return now })
	h1, h2 := c.Now(), c.Now()
	if !h2.After(h1) || h1.Wall != 1000 {
		t.Fatalf("clock not monotonic: %v %v", h1, h2)
	}
	c.Observe(offline.HLC{Wall: 5000, Count: 7, Node: "dev"})
	if h3 := c.Now(); h3.Wall != 5000 || h3.Count != 8 {
		t.Fatalf("observe not merged: %v", h3)
	}
}

func TestIDs(t *testing.T) {
	seen := map[string]bool{}
	prev := ""
	for i := 0; i < 2000; i++ {
		id := offline.NewID()
		if !offline.ValidMutationID(id) || seen[id] || id <= prev {
			t.Fatalf("bad id %q after %q", id, prev)
		}
		seen[id], prev = true, id
	}
	if !offline.ValidMutationID("01890a5d-ac96-774b-bcce-b302099a8057") {
		t.Fatal("uuidv7 rejected")
	}
	for _, bad := range []string{"", "nope", "81890A5DAC96774BBCCEB30209", "01890a5d-ac96-774b-bcce-b302099a805Z"} {
		if offline.ValidMutationID(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestPushIsIdempotent(t *testing.T) {
	e := newEnv(t)
	p, d := e.device("acme")
	c := newClock(d.ID, e.now)
	m := create(c, "p1", set("name", "Tea", "price", 250, "stock", 10))

	rs := e.push(p, d, m)
	wantStatus(t, rs, offline.StatusApplied)
	if rs[0].Seq == 0 {
		t.Fatal("applied mutation has no seq")
	}
	// The connection dropped before the client saw the ack: it retries.
	again := e.push(p, d, m, inc(c, "p1", -1))
	wantStatus(t, again, offline.StatusDuplicate, offline.StatusApplied)
	if again[0].Original != offline.StatusApplied || again[0].Seq != rs[0].Seq {
		t.Fatalf("duplicate should echo the original: %+v", again[0])
	}
	if got := e.product("acme", "p1").Stock; got != 9 {
		t.Fatalf("stock %v, want 9 (create applied once, then -1)", got)
	}
	if n := e.ledgerCount("acme"); n != 2 {
		t.Fatalf("ledger has %d rows, want 2", n)
	}
}

func TestFieldLevelLastWriteWins(t *testing.T) {
	e := newEnv(t)
	p, d1 := e.device("acme")
	_, d2 := e.device("acme")
	c1, c2 := newClock(d1.ID, e.now), newClock(d2.ID, e.now)
	wantStatus(t, e.push(p, d1, create(c1, "p1", set("name", "Tea", "price", 250))), offline.StatusApplied)

	// Both tills edit offline: till 2 renames, till 1 reprices a little later.
	c2.wall = c1.wall
	rename := update(c2, "p1", set("name", "Green tea"))
	c1.wall = c2.wall + 10
	reprice := update(c1, "p1", set("price", 300))
	wantStatus(t, e.push(p, d1, reprice), offline.StatusApplied)
	wantStatus(t, e.push(p, d2, rename), offline.StatusApplied)
	got := e.product("acme", "p1")
	if got.Name != "Green tea" || got.Price != 300 {
		t.Fatalf("different fields should both survive: %+v", got)
	}

	// An edit made before the latest price change loses that field only.
	c2.wall = c1.wall - 5
	stale := update(c2, "p1", set("price", 100, "name", "Tea (old)"))
	c2.wall = c1.wall + 50
	rs := e.push(p, d2, stale)
	wantStatus(t, rs, offline.StatusResolved)
	if !strings.Contains(rs[0].Reason, "stale_fields") || rs[0].Record == nil {
		t.Fatalf("resolved result should explain and carry the record: %+v", rs[0])
	}
	got = e.product("acme", "p1")
	if got.Price != 300 || got.Name != "Tea (old)" {
		t.Fatalf("price should keep the newer value, name take the newer edit: %+v", got)
	}
	if data := rs[0].Record.Data.(map[string]any); data["price"] != int64(300) {
		t.Fatalf("record should show server state: %+v", data)
	}
}

func TestCountersAddUp(t *testing.T) {
	e := newEnv(t)
	p, d1 := e.device("acme")
	_, d2 := e.device("acme")
	c1, c2 := newClock(d1.ID, e.now), newClock(d2.ID, e.now)
	wantStatus(t, e.push(p, d1, create(c1, "p1", set("name", "Tea", "stock", 10))), offline.StatusApplied)

	// Two tills sell offline; the older sale arrives last and still counts.
	saleOld := inc(c2, "p1", -3)
	c1.wall = c2.wall + 100
	wantStatus(t, e.push(p, d1, inc(c1, "p1", -2)), offline.StatusApplied)
	wantStatus(t, e.push(p, d2, saleOld), offline.StatusApplied)
	if got := e.product("acme", "p1").Stock; got != 5 {
		t.Fatalf("stock %v, want 5", got)
	}

	bad := update(c1, "p1", nil)
	bad.Inc = map[string]float64{"price": 1}
	rs := e.push(p, d1, bad)
	wantStatus(t, rs, offline.StatusRejected)
	if !strings.HasPrefix(rs[0].Reason, "forbidden_field") {
		t.Fatalf("reason %q", rs[0].Reason)
	}
}

func TestGroupIsAtomic(t *testing.T) {
	e := newEnv(t)
	p, d := e.device("acme")
	c := newClock(d.ID, e.now)
	wantStatus(t, e.push(p, d, create(c, "p1", set("name", "Tea", "stock", 10))), offline.StatusApplied)

	a := create(c, "p2", set("name", "Cake"))
	b := update(c, "p1", set("price", -5)) // Apply rejects this
	a.Group, b.Group = "sale-1", "sale-1"
	after := inc(c, "p1", -1)
	rs := e.push(p, d, a, b, after)
	wantStatus(t, rs, offline.StatusRejected, offline.StatusRejected, offline.StatusApplied)
	if !strings.HasPrefix(rs[0].Reason, "group_rejected") || !strings.Contains(rs[1].Reason, "price must not be negative") {
		t.Fatalf("reasons: %q / %q", rs[0].Reason, rs[1].Reason)
	}
	if rs[0].Record == nil || !rs[0].Record.Deleted {
		t.Fatalf("rejected create should come back as a tombstone so the client drops it: %+v", rs[0].Record)
	}
	if _, err := e.client.Product.Get(privacy.WithTenantID(context.Background(), "acme"), "p2"); err == nil {
		t.Fatal("p2 was created although its group was rejected")
	}
	// The rejection is remembered: a retry gets the same answer.
	again := e.push(p, d, a, b)
	wantStatus(t, again, offline.StatusDuplicate, offline.StatusDuplicate)
	if again[1].Original != offline.StatusRejected {
		t.Fatalf("original %s", again[1].Original)
	}
}

func TestTransientFailureStopsBatch(t *testing.T) {
	e := newEnv(t)
	p, d := e.device("acme")
	c := newClock(d.ID, e.now)
	m1 := create(c, "p1", set("name", "Tea", "stock", 1))
	m2 := inc(c, "p1", 5)
	m3 := inc(c, "p1", 10)
	wantStatus(t, e.push(p, d, m1), offline.StatusApplied)

	e.failNext(1, errTransient)
	rs := e.push(p, d, m1, m2, m3)
	wantStatus(t, rs, offline.StatusDuplicate, offline.StatusRetry, offline.StatusRetry)
	if rs[2].Reason != "blocked" {
		t.Fatalf("later mutations should be held back in order: %q", rs[2].Reason)
	}
	if got := e.product("acme", "p1").Stock; got != 1 {
		t.Fatalf("nothing after the failure may apply: stock %v", got)
	}
	wantStatus(t, e.push(p, d, m2, m3), offline.StatusApplied, offline.StatusApplied)
	if got := e.product("acme", "p1").Stock; got != 16 {
		t.Fatalf("stock %v, want 16", got)
	}

	// An unexpected error is permanent and does not leak to the client.
	e.failNext(1, errors.New("UNIQUE constraint failed: secret_table.col"))
	rs = e.push(p, d, inc(c, "p1", 1))
	wantStatus(t, rs, offline.StatusRejected)
	if rs[0].Reason != "apply_failed" {
		t.Fatalf("internal error leaked: %q", rs[0].Reason)
	}
}

func TestValidation(t *testing.T) {
	e := newEnv(t)
	p, d := e.device("acme")
	c := newClock(d.ID, e.now)
	cases := map[string]offline.Mutation{
		"invalid":         {ID: "nope", Entity: "product", EntityID: "x", Op: offline.OpCreate, HLC: c.next()},
		"unknown_entity":  {ID: offline.NewID(), Entity: "order", EntityID: "x", Op: offline.OpCreate, HLC: c.next()},
		"forbidden_field": {ID: offline.NewID(), Entity: "product", EntityID: "x", Op: offline.OpCreate, HLC: c.next(), Set: set("tenant_id", "evil")},
		"not_found":       update(c, "ghost", set("name", "x")),
	}
	bad := create(c, "x", nil)
	bad.HLC = "yesterday"
	cases["invalid "] = bad
	for want, m := range cases {
		rs := e.push(p, d, m)
		if rs[0].Status != offline.StatusRejected || !strings.HasPrefix(rs[0].Reason, strings.TrimSpace(want)) {
			t.Errorf("%s: got %s %q", want, rs[0].Status, rs[0].Reason)
		}
	}
	many := make([]offline.Mutation, 501)
	if _, err := e.eng.Push(context.Background(), p, &offline.PushRequest{DeviceID: d.ID, Mutations: many}); !errors.Is(err, offline.ErrBatchTooLarge) {
		t.Fatalf("got %v", err)
	}
}

func TestDeletes(t *testing.T) {
	e := newEnv(t)
	p, d := e.device("acme")
	c := newClock(d.ID, e.now)
	wantStatus(t, e.push(p, d, create(c, "p1", set("name", "Tea"))), offline.StatusApplied)
	wantStatus(t, e.push(p, d, del(c, "p1")), offline.StatusApplied)
	if e.product("acme", "p1").DeletedAt == nil {
		t.Fatal("delete should leave a tombstone")
	}
	rs := e.push(p, d, update(c, "p1", set("name", "Chai")), del(c, "p1"))
	wantStatus(t, rs, offline.StatusResolved, offline.StatusResolved)
	if rs[0].Reason != "deleted" || rs[1].Reason != "already_deleted" {
		t.Fatalf("reasons %q %q", rs[0].Reason, rs[1].Reason)
	}
	wantStatus(t, e.push(p, d, create(c, "p1", set("name", "Chai"))), offline.StatusApplied)
	if got := e.product("acme", "p1"); got.DeletedAt != nil || got.Name != "Chai" {
		t.Fatalf("newer create should restore: %+v", got)
	}

	// A delete made before a later edit loses.
	old := del(c, "p1")
	wantStatus(t, e.push(p, d, update(c, "p1", set("name", "Masala chai"))), offline.StatusApplied)
	rs = e.push(p, d, old)
	if rs[0].Status != offline.StatusResolved || rs[0].Reason != "superseded" {
		t.Fatalf("got %+v", rs[0])
	}
}

func TestClockSkewIsRestamped(t *testing.T) {
	e := newEnv(t)
	p, d := e.device("acme")
	c := newClock(d.ID, e.now)
	wantStatus(t, e.push(p, d, create(c, "p1", set("name", "Tea"))), offline.StatusApplied)

	future := newClock("broken-till", e.now.Add(48*time.Hour))
	rs := e.push(p, d, update(future, "p1", set("name", "From the future")))
	if rs[0].Status != offline.StatusResolved || rs[0].Reason != "clock_skew" {
		t.Fatalf("got %+v", rs[0])
	}
	// A normal edit a minute later still wins; without re-stamping the
	// broken till would have won every edit for two days.
	e.now = e.now.Add(time.Minute)
	c.wall = e.now.UnixMilli()
	wantStatus(t, e.push(p, d, update(c, "p1", set("name", "Tea"))), offline.StatusApplied)
	if got := e.product("acme", "p1").Name; got != "Tea" {
		t.Fatalf("name %q", got)
	}
}

func TestPull(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p, d1 := e.device("acme")
	_, d2 := e.device("acme")
	c := newClock(d1.ID, e.now)
	e.push(p, d1, create(c, "p1", set("name", "Tea")), create(c, "p2", set("name", "Cake")),
		update(c, "p1", set("price", 5)), create(c, "p3", set("name", "Bun")), del(c, "p2"))

	// Each entity once, at its latest change, oldest first.
	page, err := e.eng.Pull(ctx, p, d2.ID, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !page.HasMore || len(page.Changes) != 2 || page.Changes[0].EntityID != "p1" || page.Changes[1].EntityID != "p3" {
		t.Fatalf("first page: %+v", page)
	}
	if page.Changes[0].Data.(map[string]any)["price"] != int64(5) || page.Changes[0].Version != 2 {
		t.Fatalf("p1 should arrive in its current state: %+v", page.Changes[0])
	}
	rest, err := e.eng.Pull(ctx, p, d2.ID, page.Cursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if rest.HasMore || len(rest.Changes) != 1 || rest.Changes[0].EntityID != "p2" || !rest.Changes[0].Deleted {
		t.Fatalf("second page should be the p2 tombstone: %+v", rest)
	}
	empty, _ := e.eng.Pull(ctx, p, d2.ID, rest.Cursor, 2)
	if len(empty.Changes) != 0 || empty.Cursor != rest.Cursor {
		t.Fatalf("nothing new: %+v", empty)
	}

	// Edits made outside sync (admin panel) are pulled too, in or out of
	// an Ent transaction.
	actx := privacy.WithTenantID(ctx, "acme")
	if err := e.client.Product.UpdateOneID("p3").SetName("Bun (admin)").Exec(actx); err != nil {
		t.Fatal(err)
	}
	tx, _ := e.client.Tx(actx)
	if err := tx.Product.Create().SetID("p9").SetName("Admin item").Exec(actx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	tx, _ = e.client.Tx(actx)
	_ = tx.Product.Create().SetID("p10").SetName("Rolled back").Exec(actx)
	_ = tx.Rollback()

	admin, err := e.eng.Pull(ctx, p, d2.ID, rest.Cursor, 10)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, ch := range admin.Changes {
		got = append(got, ch.EntityID)
	}
	if strings.Join(got, ",") != "p3,p9" {
		t.Fatalf("admin changes: %v", got)
	}
	// The engine's own writes were not recorded twice by ChangeHook.
	if n := e.ledgerCount("acme"); n != 7 {
		t.Fatalf("ledger rows %d, want 5 pushed + 2 admin", n)
	}

	dev, _ := e.eng.Device(ctx, "acme", d2.ID)
	if dev.Cursor != rest.Cursor || dev.LastPullAt == 0 {
		t.Fatalf("device should remember the acknowledged cursor: %+v", dev)
	}
}

func TestTenantIsolation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	pa, da := e.device("acme")
	pb, db := e.device("globex")
	ca, cb := newClock(da.ID, e.now), newClock(db.ID, e.now)

	shared := create(ca, "p1", set("name", "Acme tea"))
	wantStatus(t, e.push(pa, da, shared), offline.StatusApplied)

	// The same mutation id in another tenant is a different mutation.
	// (Entity ids are global keys; clients generate ULIDs for them.)
	other := shared
	other.EntityID = "g1"
	other.Set = set("name", "Globex tea")
	other.HLC = cb.next()
	wantStatus(t, e.push(pb, db, other), offline.StatusApplied)
	if e.product("acme", "p1").Name != "Acme tea" || e.product("globex", "g1").Name != "Globex tea" {
		t.Fatal("tenants overwrote each other")
	}

	if _, err := e.eng.Push(ctx, pb, &offline.PushRequest{DeviceID: da.ID}); !errors.Is(err, offline.ErrUnknownDevice) {
		t.Fatalf("another tenant's device must not work: %v", err)
	}
	if _, err := e.eng.Pull(ctx, pb, da.ID, 0, 0); !errors.Is(err, offline.ErrUnknownDevice) {
		t.Fatalf("got %v", err)
	}
	// A tenant cannot touch, or learn about, another tenant's entity.
	rs := e.push(pb, db, update(cb, "p1", set("name", "hijack")))
	if rs[0].Status != offline.StatusRejected || !strings.HasPrefix(rs[0].Reason, "not_found") {
		t.Fatalf("cross-tenant update: %+v", rs[0])
	}
	res, _ := e.eng.Pull(ctx, pb, db.ID, 0, 0)
	if len(res.Changes) != 1 || res.Changes[0].Data.(map[string]any)["name"] != "Globex tea" {
		t.Fatalf("globex pull: %+v", res.Changes)
	}

	if err := e.eng.RevokeDevice(ctx, "acme", da.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.eng.Pull(ctx, pa, da.ID, 0, 0); !errors.Is(err, offline.ErrDeviceRevoked) {
		t.Fatalf("revoked device pulled: %v", err)
	}
}

func TestSnapshotAndResync(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p, d := e.device("acme")
	c := newClock(d.ID, e.now)
	for i := 0; i < 5; i++ {
		e.push(p, d, create(c, fmt.Sprintf("p%d", i), set("name", "x")))
	}
	e.push(p, d, del(c, "p2"))

	var ids []string
	var cursor int64
	after := ""
	for {
		page, err := e.eng.Snapshot(ctx, p, d.ID, "product", after, 2)
		if err != nil {
			t.Fatal(err)
		}
		if after == "" {
			cursor = page.Cursor
		}
		for _, r := range page.Records {
			ids = append(ids, r.ID)
		}
		if !page.HasMore {
			break
		}
		after = page.Next
	}
	if strings.Join(ids, ",") != "p0,p1,p3,p4" || cursor != 6 {
		t.Fatalf("snapshot %v cursor %d", ids, cursor)
	}
	if _, err := e.eng.Snapshot(ctx, p, d.ID, "order", "", 0); !errors.Is(err, offline.ErrUnknownEntity) {
		t.Fatalf("got %v", err)
	}

	// After compaction, a device behind the horizon must resync.
	if err := e.client.SyncTenant.UpdateOneID("acme").SetHorizon(4).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.eng.Pull(ctx, p, d.ID, 3, 0); !errors.Is(err, offline.ErrResyncRequired) {
		t.Fatalf("got %v", err)
	}
	if _, err := e.eng.Pull(ctx, p, d.ID, 4, 0); err != nil {
		t.Fatal(err)
	}
}

// Concurrent pushes from many devices while another device pulls: the
// puller must see every entity, which fails if sequence numbers could
// commit out of order.
func TestConcurrentPushesPullWithoutGaps(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p, reader := e.device("acme")
	const writers, each = 4, 15
	var wg sync.WaitGroup
	done := make(chan struct{})
	for w := 0; w < writers; w++ {
		_, d := e.device("acme")
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			c := newClock(d.ID, e.now)
			for i := 0; i < each; i++ {
				resp, err := e.eng.Push(ctx, p, &offline.PushRequest{DeviceID: d.ID,
					Mutations: []offline.Mutation{create(c, fmt.Sprintf("w%d-%02d", w, i), set("name", "x"))}})
				if err != nil || resp.Results[0].Status != offline.StatusApplied {
					t.Errorf("push: %v %+v", err, resp)
					return
				}
			}
		}(w)
	}
	go func() { wg.Wait(); close(done) }()

	seen := map[string]bool{}
	var cursor int64
	pull := func() {
		res, err := e.eng.Pull(ctx, p, reader.ID, cursor, 7)
		if err != nil {
			t.Fatal(err)
		}
		for _, ch := range res.Changes {
			seen[ch.EntityID] = true
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
	for i := 0; i < 10; i++ {
		pull()
	}
	if len(seen) != writers*each {
		var missing []string
		for w := 0; w < writers; w++ {
			for i := 0; i < each; i++ {
				if id := fmt.Sprintf("w%d-%02d", w, i); !seen[id] {
					missing = append(missing, id)
				}
			}
		}
		sort.Strings(missing)
		t.Fatalf("puller missed %v", missing)
	}
}

func TestMigrateWithPlainSQL(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:"+filepath.Join(t.TempDir(), "plain.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	eng, err := offline.New(offline.Options[offline.SQLTx]{Begin: offline.BeginSQL(db), Dialect: database.SQLite, Prefix: "pos_"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := eng.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := eng.RegisterDevice(ctx, offline.Principal{TenantID: "t"}, offline.DeviceInfo{}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM pos_sync_devices").Scan(&n); err != nil || n != 1 {
		t.Fatalf("prefixed table: %d %v", n, err)
	}
	if _, err := offline.New(offline.Options[offline.SQLTx]{Begin: offline.BeginSQL(db), Prefix: "bad;"}); err == nil {
		t.Fatal("invalid prefix accepted")
	}
}

// An edit made in the admin panel carries the server's clock, so an older
// offline edit of the same field does not overwrite it, while an offline
// edit of another field still applies.
func TestAdminEditsBeatOlderOfflineEdits(t *testing.T) {
	e := newEnv(t)
	p, d := e.device("acme")
	c := newClock(d.ID, e.now)
	wantStatus(t, e.push(p, d, create(c, "p1", set("name", "Tea", "price", 100))), offline.StatusApplied)
	offlinePrice := update(c, "p1", set("price", 120))
	offlineName := update(c, "p1", set("name", "Black tea"))

	e.now = e.now.Add(time.Second) // the admin edits after the till went offline
	actx := privacy.WithTenantID(context.Background(), "acme")
	if err := e.client.Product.UpdateOneID("p1").SetPrice(150).Exec(actx); err != nil {
		t.Fatal(err)
	}
	got := e.product("acme", "p1")
	if got.SyncVersion != 2 || got.SyncClocks["price"] == "" || got.SyncClocks["name"] == got.SyncClocks["price"] {
		t.Fatalf("admin edit should bump the version and the price clock only: %+v", got)
	}
	rs := e.push(p, d, offlinePrice, offlineName)
	wantStatus(t, rs, offline.StatusResolved, offline.StatusApplied)
	got = e.product("acme", "p1")
	if got.Price != 150 || got.Name != "Black tea" {
		t.Fatalf("price should stay the admin's, name take the offline edit: %+v", got)
	}

	// Bulk admin updates fall back to the row clock for every field.
	older := update(c, "p1", set("price", 1))
	e.now = e.now.Add(time.Second)
	if _, err := e.client.Product.Update().SetName("Bulk").Save(actx); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, e.push(p, d, older), offline.StatusResolved)
	if got := e.product("acme", "p1"); got.Price != 150 || got.Name != "Bulk" {
		t.Fatalf("after bulk update: %+v", got)
	}
}
