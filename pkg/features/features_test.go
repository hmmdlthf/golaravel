package features_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/golaravel/golaravel/pkg/database"
	"github.com/golaravel/golaravel/pkg/features"
	"github.com/golaravel/golaravel/pkg/privacy"
)

type defs struct {
	recurring *features.Feature[bool]
	taxMode   *features.Feature[string]
	branches  *features.Feature[int]
	footer    *features.Feature[string]
	theme     *features.Feature[map[string]string]
}

func newDefs() defs {
	return defs{
		recurring: features.Bool("invoices.recurring", false).Label("Recurring invoices").
			Group("Billing").ManagedBy(features.SuperAdmin).Public(),
		taxMode:  features.Enum("tax.mode", "inclusive", "inclusive", "exclusive", "none").Group("Billing"),
		branches: features.Int("branches.max", 1).Rules(features.Range(1, 50)),
		footer:   features.String("invoice.footer", "").Rules(features.MaxLen(10)),
		theme:    features.JSON("ui.theme", map[string]string{"color": "blue"}).Public(),
	}
}

func setup(t *testing.T) (*features.Manager, defs, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+filepath.Join(t.TempDir(), "f.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	m, err := features.New(features.Options{DB: db, Dialect: database.SQLite,
		Actor: func(context.Context) string { return "user-1" }})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := m.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	d := newDefs()
	if err := m.Register(d.recurring, d.taxMode, d.branches, d.footer, d.theme); err != nil {
		t.Fatal(err)
	}
	return m, d, db
}

func tenantCtx(id string) context.Context { return privacy.WithTenantID(context.Background(), id) }

func TestDefaultsWhenNoRow(t *testing.T) {
	_, d, _ := setup(t)
	ctx := tenantCtx("acme")
	if v, err := d.taxMode.Get(ctx); err != nil || v != "inclusive" {
		t.Fatalf("taxMode = %q, %v", v, err)
	}
	if features.Enabled(ctx, d.recurring) {
		t.Fatal("recurring should default off")
	}
	if ov, err := d.taxMode.Overridden(ctx); err != nil || ov {
		t.Fatalf("Overridden = %v, %v", ov, err)
	}
}

func TestOverrideIsPerTenant(t *testing.T) {
	_, d, db := setup(t)
	acme, globex := tenantCtx("acme"), tenantCtx("globex")

	if err := d.taxMode.Set(acme, "exclusive"); err != nil {
		t.Fatal(err)
	}
	if err := d.recurring.Set(acme, true); err != nil {
		t.Fatal(err)
	}
	if !features.Is(acme, d.taxMode, "exclusive") || !features.Enabled(acme, d.recurring) {
		t.Fatal("acme override not applied")
	}
	if v := d.taxMode.Value(globex); v != "inclusive" {
		t.Fatalf("globex taxMode = %q, want default", v)
	}

	// Overwrite, then reset back to the default.
	if err := d.taxMode.Set(acme, "none"); err != nil {
		t.Fatal(err)
	}
	if v := d.taxMode.Value(acme); v != "none" {
		t.Fatalf("after overwrite = %q", v)
	}
	var by string
	if err := db.QueryRow("SELECT updated_by FROM tenant_settings WHERE id = 'acme|tax.mode'").Scan(&by); err != nil || by != "user-1" {
		t.Fatalf("updated_by = %q, %v", by, err)
	}
	if err := d.taxMode.Reset(acme); err != nil {
		t.Fatal(err)
	}
	if v := d.taxMode.Value(acme); v != "inclusive" {
		t.Fatalf("after reset = %q", v)
	}
}

func TestValidation(t *testing.T) {
	m, d, _ := setup(t)
	ctx := tenantCtx("acme")
	cases := []error{
		d.taxMode.Set(ctx, "sometimes"),
		d.branches.Set(ctx, 0),
		d.branches.Set(ctx, 51),
		d.footer.Set(ctx, "far too long text"),
		m.Set(ctx, "branches.max", "abc"),
		m.Set(ctx, "tax.mode", 5),
	}
	for i, err := range cases {
		if !errors.Is(err, features.ErrInvalidValue) {
			t.Errorf("case %d: err = %v, want ErrInvalidValue", i, err)
		}
	}
	if err := m.Set(ctx, "nope", 1); !errors.Is(err, features.ErrUnknownFeature) {
		t.Errorf("unknown key: %v", err)
	}
}

func TestUntypedSetParsesStrings(t *testing.T) {
	m, d, _ := setup(t)
	ctx := tenantCtx("acme")
	steps := []struct {
		key string
		in  any
	}{
		{"invoices.recurring", "true"},
		{"branches.max", "7"},
		{"tax.mode", "none"},
		{"ui.theme", `{"color":"red"}`},
	}
	for _, s := range steps {
		if err := m.Set(ctx, s.key, s.in); err != nil {
			t.Fatalf("Set(%s, %v): %v", s.key, s.in, err)
		}
	}
	if err := m.Set(ctx, "branches.max", 9.0); err != nil { // JSON numbers decode as float64
		t.Fatal(err)
	}
	if !features.Enabled(ctx, d.recurring) || d.branches.Value(ctx) != 9 ||
		d.taxMode.Value(ctx) != "none" || d.theme.Value(ctx)["color"] != "red" {
		t.Fatal("untyped Set values not read back")
	}
	if v, err := m.Get(ctx, "branches.max"); err != nil || v != 9 {
		t.Fatalf("Get = %v, %v", v, err)
	}
}

func TestInvalidStoredValueFallsBackToDefault(t *testing.T) {
	m, d, db := setup(t)
	ctx := tenantCtx("acme")
	// An option removed from the enum after a tenant chose it.
	if _, err := db.Exec(`INSERT INTO tenant_settings (id, tenant_id, name, value, updated_at)
		VALUES ('acme|tax.mode', 'acme', 'tax.mode', '"legacy"', 0)`); err != nil {
		t.Fatal(err)
	}
	if v, err := d.taxMode.Get(ctx); err != nil || v != "inclusive" {
		t.Fatalf("Get = %q, %v", v, err)
	}
	all, err := m.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range all {
		if r.Key == "tax.mode" && (r.Overridden || r.Value != "inclusive") {
			t.Fatalf("All tax.mode = %+v", r)
		}
	}
}

func TestTenancyFailsClosed(t *testing.T) {
	m, d, _ := setup(t)
	bg := context.Background()
	if _, err := d.taxMode.Get(bg); !errors.Is(err, privacy.ErrTenantMissing) {
		t.Fatalf("Get without tenant: %v", err)
	}
	if err := d.taxMode.Set(bg, "none"); !errors.Is(err, privacy.ErrTenantMissing) {
		t.Fatalf("Set without tenant: %v", err)
	}
	if _, err := m.All(bg); !errors.Is(err, privacy.ErrTenantMissing) {
		t.Fatalf("All without tenant: %v", err)
	}
	if features.Enabled(bg, d.recurring) {
		t.Fatal("Enabled must be false on error")
	}

	bypass := privacy.WithBypassTenancy(bg)
	if v, err := d.taxMode.Get(bypass); err != nil || v != "inclusive" {
		t.Fatalf("bypassed Get = %q, %v", v, err)
	}
	if err := d.taxMode.Set(bypass, "none"); !errors.Is(err, privacy.ErrTenantMissing) {
		t.Fatalf("bypassed Set: %v", err)
	}
	// A super admin picks the tenant explicitly.
	if err := d.taxMode.Set(features.ForTenant(bypass, "acme"), "none"); err != nil {
		t.Fatal(err)
	}
	if v := d.taxMode.Value(tenantCtx("acme")); v != "none" {
		t.Fatalf("ForTenant write not visible: %q", v)
	}
}

func TestAllAndPublic(t *testing.T) {
	m, d, _ := setup(t)
	ctx := tenantCtx("acme")
	if err := d.recurring.Set(ctx, true); err != nil {
		t.Fatal(err)
	}
	all, err := m.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 5 {
		t.Fatalf("All returned %d", len(all))
	}
	// Sorted by group then key: "" group first, then Billing.
	if all[0].Group != "" || all[len(all)-1].Group != "Billing" {
		t.Fatalf("order: %v", all)
	}
	for _, r := range all {
		if r.Key == "branches.max" && (*r.Min != 1 || *r.Max != 50) {
			t.Fatalf("range meta = %v..%v", *r.Min, *r.Max)
		}
		if r.Key == "invoices.recurring" && (!r.Overridden || r.Value != true || r.ManagedBy != features.SuperAdmin) {
			t.Fatalf("recurring = %+v", r)
		}
	}
	pub, err := m.Public(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pub) != 2 || pub["invoices.recurring"] != true {
		t.Fatalf("Public = %v", pub)
	}
}

func TestRegisterChecks(t *testing.T) {
	m, d, _ := setup(t)
	if err := m.Register(d.taxMode); err != nil {
		t.Fatalf("re-registering the same feature: %v", err)
	}
	if err := m.Register(features.Bool("tax.mode", true)); err == nil {
		t.Fatal("duplicate key accepted")
	}
	if err := m.Register(features.Enum("bad.default", "x", "a", "b")); !errors.Is(err, features.ErrInvalidValue) {
		t.Fatalf("bad default: %v", err)
	}
	if err := m.Register(features.Bool("Bad Key", true)); err == nil {
		t.Fatal("invalid key accepted")
	}

	other, _ := features.New(features.Options{DB: &sql.DB{}})
	if err := other.Register(d.taxMode); err == nil {
		t.Fatal("feature bound to two managers")
	}
	if _, err := features.Bool("loose", true).Get(tenantCtx("acme")); !errors.Is(err, features.ErrNotRegistered) {
		t.Fatalf("unregistered Get: %v", err)
	}
}
