package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golaravel/golaravel/example/app"
	"github.com/golaravel/golaravel/example/app/pos"
	"github.com/golaravel/golaravel/pkg/auth"
	"github.com/golaravel/golaravel/pkg/golaravel"
	"github.com/golaravel/golaravel/pkg/offline"
	"github.com/golaravel/golaravel/pkg/privacy"
)

// The offline point of sale: a till registers, loads the products, sells
// while "offline" (a grouped push of sale, line and stock delta), and the
// shop's rules (immutable sales, no negative prices) are enforced.
func TestPOS_OfflineSync(t *testing.T) {
	ctx := context.Background()
	client, err := app.Open("sqlite3", "file:pos_test?mode=memory&cache=shared&_fk=1")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Schema.Create(ctx); err != nil {
		t.Fatal(err)
	}
	jwt := auth.NewJWTManager("test-jwt-secret-key-32-chars-long", time.Hour)
	engine := golaravel.New()
	if _, err := pos.Setup(engine.Router, client, jwt, pos.Options{}); err != nil {
		t.Fatal(err)
	}
	if err := pos.Seed(ctx, client); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(engine)
	defer srv.Close()

	token, _ := jwt.GenerateToken("1", map[string]any{"tenant_id": pos.DemoTenant})
	var device string
	call := func(method, path string, body, out any) int {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, srv.URL+"/api/pos/sync"+path, rd)
		req.Header.Set("Authorization", "Bearer "+token)
		if device != "" {
			req.Header.Set(offline.DeviceHeader, device)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if out != nil {
			_ = json.NewDecoder(resp.Body).Decode(out)
		}
		return resp.StatusCode
	}

	var dev offline.Device
	if code := call("POST", "/devices", offline.DeviceInfo{Name: "Till 1"}, &dev); code != 201 || dev.TenantID != pos.DemoTenant {
		t.Fatalf("register: %d %+v", code, dev)
	}
	device = dev.ID

	var snap offline.SnapshotResponse
	if code := call("GET", "/snapshot?entity=product", nil, &snap); code != 200 || len(snap.Records) != 3 {
		t.Fatalf("snapshot: %d %+v", code, snap)
	}

	clock := time.Now().UnixMilli()
	hlc := func() string { clock++; return offline.HLC{Wall: clock, Node: device}.String() }
	mut := func(entity, id string, op offline.Op, set map[string]any, inc map[string]float64) offline.Mutation {
		m := offline.Mutation{ID: offline.NewID(), Entity: entity, EntityID: id, Op: op, HLC: hlc(), Inc: inc, Group: ""}
		if set != nil {
			m.Set = map[string]json.RawMessage{}
			for k, v := range set {
				b, _ := json.Marshal(v)
				m.Set[k] = b
			}
		}
		return m
	}
	push := func(ms ...offline.Mutation) []offline.Result {
		t.Helper()
		var resp offline.PushResponse
		if code := call("POST", "/push", offline.PushRequest{Mutations: ms}, &resp); code != 200 {
			t.Fatalf("push: %d", code)
		}
		return resp.Results
	}

	// A sale made offline: sale, line and stock change in one group.
	sale := mut("sale", "s1", offline.OpCreate, map[string]any{"total": 900, "sold_at": time.Now().UTC()}, nil)
	line := mut("sale_line", "l1", offline.OpCreate, map[string]any{"sale_id": "s1", "product_id": "seed-tea", "qty": 2, "price": 450}, nil)
	stock := mut("product", "seed-tea", offline.OpUpdate, nil, map[string]float64{"stock": -2})
	sale.Group, line.Group, stock.Group = "sale-s1", "sale-s1", "sale-s1"
	for i, r := range push(sale, line, stock) {
		if r.Status != offline.StatusApplied {
			t.Fatalf("sale mutation %d: %s %s", i, r.Status, r.Reason)
		}
	}
	tctx := privacy.WithTenantID(ctx, pos.DemoTenant)
	if p := client.Product.GetX(tctx, "seed-tea"); p.Stock != 38 {
		t.Fatalf("stock %v, want 38", p.Stock)
	}

	// Shop rules.
	for _, c := range []struct {
		m    offline.Mutation
		want string
	}{
		{mut("sale", "s1", offline.OpUpdate, map[string]any{"total": 1}, nil), "immutable"},
		{mut("sale", "s1", offline.OpDelete, nil, nil), "immutable"},
		{mut("product", "seed-bun", offline.OpUpdate, map[string]any{"price": -5}, nil), "invalid"},
	} {
		r := push(c.m)[0]
		if r.Status != offline.StatusRejected || !strings.HasPrefix(r.Reason, c.want) {
			t.Fatalf("%s %s: %s %q", c.m.Entity, c.m.Op, r.Status, r.Reason)
		}
	}

	// An edit in the back office reaches the till.
	var pulled offline.PullResponse
	if code := call("GET", "/pull?cursor=0", nil, &pulled); code != 200 {
		t.Fatalf("pull: %d", code)
	}
	client.Product.UpdateOneID("seed-bun").SetPrice(150).ExecX(tctx)
	var next offline.PullResponse
	call("GET", "/pull?cursor="+jsonNumber(pulled.Cursor), nil, &next)
	if len(next.Changes) != 1 || next.Changes[0].EntityID != "seed-bun" {
		t.Fatalf("back-office edit not pulled: %+v", next.Changes)
	}

	// No token, or a token without a tenant, gets nothing.
	token = ""
	if code := call("GET", "/pull", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", code)
	}
	token, _ = jwt.GenerateToken("1", nil)
	if code := call("GET", "/pull", nil, nil); code != http.StatusForbidden {
		t.Fatalf("no tenant claim: %d", code)
	}

	resp, err := http.Get(srv.URL + "/pos")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(page), "Offline till") {
		t.Fatal("demo page not served")
	}
}

func jsonNumber(n int64) string { b, _ := json.Marshal(n); return string(b) }
