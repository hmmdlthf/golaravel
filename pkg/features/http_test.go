package features_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golaravel/golaravel"
	gcontext "github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/events"
	"github.com/golaravel/golaravel/pkg/features"
	"github.com/golaravel/golaravel/pkg/inertia"
	"github.com/golaravel/golaravel/pkg/privacy"
)

func ok(c *gcontext.Context) error { return c.String(http.StatusOK, "ok") }

func serve(app http.Handler, path, tenantID string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if tenantID != "" {
		req.Header.Set("X-Tenant-ID", tenantID)
	}
	app.ServeHTTP(w, req)
	return w
}

func TestRequireMiddleware(t *testing.T) {
	db := &countingDB{DB: openDB(t)}
	d := newDefs()
	newManager(t, db, features.Options{}, d.recurring, d.taxMode)
	acme := tenantCtx("acme")
	if err := d.recurring.Set(acme, true); err != nil {
		t.Fatal(err)
	}
	if err := d.taxMode.Set(acme, "exclusive"); err != nil {
		t.Fatal(err)
	}

	app := golaravel.New()
	tenantMw := privacy.TenantMiddlewareWithOptions(privacy.TenantMiddlewareOptions{
		Authorize: func(*gcontext.Context, string) bool { return true },
	}, privacy.HeaderResolver())
	optional := privacy.TenantMiddlewareWithOptions(privacy.TenantMiddlewareOptions{
		Authorize: func(*gcontext.Context, string) bool { return true },
	}, privacy.HeaderResolver())
	app.GET("/recurring", tenantMw, features.Require(d.recurring), features.RequireValue(d.taxMode, []string{"exclusive"}), ok)
	app.GET("/forbidden", tenantMw, features.Require(d.recurring, features.DeniedStatus(http.StatusForbidden)), ok)
	app.GET("/upgrade", tenantMw, features.Require(d.recurring, features.OnDenied(func(c *gcontext.Context) error {
		return c.String(http.StatusPaymentRequired, "upgrade")
	})), ok)
	app.GET("/no-tenant", optional, features.Require(d.recurring), ok)

	before := db.selects.Load()
	if w := serve(app, "/recurring", "acme"); w.Code != http.StatusOK {
		t.Fatalf("acme: %d %s", w.Code, w.Body)
	}
	// Two checks in one request share one load (memo).
	if n := db.selects.Load() - before; n != 1 {
		t.Fatalf("%d selects for one request, want 1", n)
	}
	if w := serve(app, "/recurring", "globex"); w.Code != http.StatusNotFound {
		t.Fatalf("globex: %d", w.Code)
	}
	if w := serve(app, "/forbidden", "globex"); w.Code != http.StatusForbidden {
		t.Fatalf("DeniedStatus: %d", w.Code)
	}
	if w := serve(app, "/upgrade", "globex"); w.Code != http.StatusPaymentRequired || w.Body.String() != "upgrade" {
		t.Fatalf("OnDenied: %d %s", w.Code, w.Body)
	}
	if w := serve(app, "/upgrade", "acme"); w.Code != http.StatusOK {
		t.Fatalf("OnDenied allowed: %d", w.Code)
	}
}

func TestRequireWithoutTenantFailsClosed(t *testing.T) {
	d := newDefs()
	newManager(t, openDB(t), features.Options{}, d.recurring)
	app := golaravel.New()
	app.GET("/x", features.Require(d.recurring), ok)
	if w := serve(app, "/x", ""); w.Code != http.StatusForbidden {
		t.Fatalf("no tenant: %d", w.Code)
	}
}

func TestInertiaProp(t *testing.T) {
	d := newDefs()
	m := newManager(t, openDB(t), features.Options{}, d.recurring, d.taxMode, d.theme)
	if err := d.recurring.Set(tenantCtx("acme"), true); err != nil {
		t.Fatal(err)
	}
	engine := inertia.New("1")
	engine.ShareFunc("features", m.InertiaProp())

	render := func(ctx context.Context) map[string]any {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
		req.Header.Set(inertia.HeaderInertia, "true")
		c := gcontext.NewContext()
		c.Reset(w, req)
		if err := engine.Render(c, http.StatusOK, "Home", nil); err != nil {
			t.Fatal(err)
		}
		var page inertia.Page
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		return page.Props["features"].(map[string]any)
	}
	got := render(tenantCtx("acme"))
	// Only Public features: recurring and theme, not tax.mode.
	if len(got) != 2 || got["invoices.recurring"] != true {
		t.Fatalf("props = %v", got)
	}
	if got := render(context.Background()); len(got) != 0 {
		t.Fatalf("no tenant props = %v", got)
	}
}

func TestFake(t *testing.T) {
	d := newDefs()
	m := features.NewInMemory(features.Options{Events: events.NewDispatcher()})
	if err := m.Register(d.recurring, d.taxMode, d.branches); err != nil {
		t.Fatal(err)
	}
	// No tenant needed for faked keys.
	ctx := features.Fake(context.Background(), map[string]any{"invoices.recurring": true})
	if !features.Enabled(ctx, d.recurring) {
		t.Fatal("fake not applied")
	}
	if _, err := d.taxMode.Get(ctx); !errors.Is(err, privacy.ErrTenantMissing) {
		t.Fatalf("unfaked key without tenant: %v", err)
	}
	if _, err := m.All(ctx); !errors.Is(err, privacy.ErrTenantMissing) {
		t.Fatalf("All without tenant: %v", err)
	}
	// Nested fakes; inner wins, real values show through.
	acme := tenantCtx("acme")
	if err := d.branches.Set(acme, 4); err != nil {
		t.Fatal(err)
	}
	inner := features.Fake(features.Fake(acme, map[string]any{"tax.mode": "none", "invoices.recurring": true}),
		map[string]any{"tax.mode": "exclusive"})
	if d.taxMode.Value(inner) != "exclusive" || !features.Enabled(inner, d.recurring) || d.branches.Value(inner) != 4 {
		t.Fatal("nested fakes wrong")
	}
	if _, err := d.taxMode.Get(features.Fake(acme, map[string]any{"tax.mode": "bogus"})); !errors.Is(err, features.ErrInvalidValue) {
		t.Fatalf("invalid fake: %v", err)
	}
	if _, err := m.All(features.Fake(acme, map[string]any{"nope": 1})); !errors.Is(err, features.ErrUnknownFeature) {
		t.Fatalf("unknown fake: %v", err)
	}
	// The in-memory manager stores real values too.
	if err := d.taxMode.Set(acme, "none"); err != nil || d.taxMode.Value(acme) != "none" {
		t.Fatalf("in-memory set: %v", err)
	}
}

func TestCLI(t *testing.T) {
	db := openDB(t)
	d := newDefs()
	m := newManager(t, db, features.Options{}, d.recurring, d.taxMode, d.branches)
	var out bytes.Buffer
	cli := features.NewCLI(m, &out)
	run := func(name string, args ...string) error {
		for _, c := range cli.Commands() {
			if c.Name == name {
				out.Reset()
				return c.Run(args)
			}
		}
		t.Fatalf("no command %s", name)
		return nil
	}

	if err := run("features:list"); err != nil || !strings.Contains(out.String(), "inclusive, exclusive, none") {
		t.Fatalf("list: %v\n%s", err, out.String())
	}
	if err := run("features:set", "acme", "branches.max", "9"); err != nil {
		t.Fatal(err)
	}
	if err := run("features:set", "acme", "invoices.recurring", "true"); err != nil {
		t.Fatal(err)
	}
	if err := run("features:set", "acme", "tax.mode", "bogus"); !errors.Is(err, features.ErrInvalidValue) {
		t.Fatalf("invalid set: %v", err)
	}
	if err := run("features:set", "acme"); err == nil {
		t.Fatal("usage error expected")
	}
	if d.branches.Value(tenantCtx("acme")) != 9 {
		t.Fatal("CLI set not stored")
	}
	if err := run("features:list", "--tenant=acme"); err != nil || !strings.Contains(out.String(), "9 *") {
		t.Fatalf("tenant list: %v\n%s", err, out.String())
	}
	if err := run("features:reset", "acme", "branches.max"); err != nil || d.branches.Value(tenantCtx("acme")) != 1 {
		t.Fatalf("reset: %v", err)
	}

	// A feature removed from code leaves rows behind; prune deletes them.
	if _, err := db.Exec(`INSERT INTO tenant_settings (id, tenant_id, name, value, updated_at)
		VALUES ('acme|old.flag', 'acme', 'old.flag', 'true', 0), ('globex|old.flag', 'globex', 'old.flag', 'true', 0)`); err != nil {
		t.Fatal(err)
	}
	if err := run("features:prune", "--dry-run"); err != nil || !strings.Contains(out.String(), "Would delete 2") {
		t.Fatalf("dry run: %v\n%s", err, out.String())
	}
	if err := run("features:prune"); err != nil || !strings.Contains(out.String(), "Deleted 2") {
		t.Fatalf("prune: %v\n%s", err, out.String())
	}
	var n int
	_ = db.QueryRow("SELECT COUNT(*) FROM tenant_settings").Scan(&n)
	if n != 1 { // acme's invoices.recurring stays
		t.Fatalf("%d rows left", n)
	}
}
