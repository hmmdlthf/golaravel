package integration

import (
	"context"
	"testing"

	"github.com/golaravel/golaravel/pkg/cache"
	"github.com/golaravel/golaravel/pkg/events"
	"github.com/golaravel/golaravel/pkg/features"
	"github.com/golaravel/golaravel/pkg/privacy"
)

type featureDefs struct {
	taxMode  *features.Feature[string]
	branches *features.Feature[int]
	theme    *features.Feature[map[string]string]
}

func newFeatureDefs() featureDefs {
	return featureDefs{
		taxMode:  features.Enum("tax.mode", "inclusive", "inclusive", "exclusive", "none"),
		branches: features.Int("branches.max", 1).Rules(features.Range(1, 50)),
		theme:    features.JSON("ui.theme", map[string]string{"color": "blue"}),
	}
}

func newFeatureManager(t *testing.T, b sqlBackend, table string, store cache.Store) featureDefs {
	t.Helper()
	m, err := features.New(features.Options{DB: b.db, Dialect: b.dialect, Table: table, Cache: store,
		Events: events.NewDispatcher(), Actor: func(context.Context) string { return "it-user" }})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for range 2 { // Migrate must be repeatable.
		if err := m.Migrate(ctx); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}
	d := newFeatureDefs()
	if err := m.Register(d.taxMode, d.branches, d.theme); err != nil {
		t.Fatal(err)
	}
	return d
}

func exerciseFeatures(t *testing.T, d featureDefs, other featureDefs) {
	t.Helper()
	acme := privacy.WithTenantID(context.Background(), "acme")
	globex := privacy.WithTenantID(context.Background(), "globex")

	if v := d.taxMode.Value(acme); v != "inclusive" {
		t.Fatalf("default = %q", v)
	}
	if err := d.taxMode.Set(acme, "exclusive"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := d.taxMode.Set(acme, "none"); err != nil { // upsert over an existing row
		t.Fatalf("overwrite: %v", err)
	}
	if err := d.branches.Set(acme, 12); err != nil {
		t.Fatal(err)
	}
	if err := d.theme.Set(acme, map[string]string{"color": "red"}); err != nil {
		t.Fatal(err)
	}
	if v := other.taxMode.Value(acme); v != "none" {
		t.Fatalf("other instance taxMode = %q", v)
	}
	if other.branches.Value(acme) != 12 || other.theme.Value(acme)["color"] != "red" {
		t.Fatal("other instance misses int/json values")
	}
	if v := other.taxMode.Value(globex); v != "inclusive" {
		t.Fatalf("globex = %q", v)
	}
	if err := d.taxMode.Reset(acme); err != nil {
		t.Fatal(err)
	}
	if v := other.taxMode.Value(acme); v != "inclusive" {
		t.Fatalf("after reset = %q", v)
	}
}

func TestFeaturesSQL(t *testing.T) {
	for _, b := range sqlBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			table := "it_tenant_settings_" + suffix()
			dropTables(t, b.db, table)
			a := newFeatureManager(t, b, table, nil)
			c := newFeatureManager(t, b, table, nil)
			exerciseFeatures(t, a, c)
		})
	}
}

func TestFeaturesRedisCache(t *testing.T) {
	c := redisClient(t)
	b := sqlBackends(t)[0]
	table := "it_tenant_settings_" + suffix()
	dropTables(t, b.db, table)
	prefix := "it:" + suffix() + ":features:"
	// Two instances sharing a database and a Redis cache.
	s1 := cache.NewRedisStore(c, cache.RedisOptions{Prefix: prefix})
	s2 := cache.NewRedisStore(c, cache.RedisOptions{Prefix: prefix})
	t.Cleanup(func() { _ = s1.Flush() })
	exerciseFeatures(t, newFeatureManager(t, b, table, s1), newFeatureManager(t, b, table, s2))
}
