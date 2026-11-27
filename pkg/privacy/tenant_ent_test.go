package privacy_test

import (
	"context"
	"errors"
	"testing"

	"entgo.io/ent"
	_ "github.com/mattn/go-sqlite3"

	"github.com/golaravel/golaravel/pkg/privacy"
	"github.com/golaravel/golaravel/pkg/privacy/internal/testent"
	"github.com/golaravel/golaravel/pkg/privacy/internal/testent/enttest"
	"github.com/golaravel/golaravel/pkg/privacy/internal/testent/note"
)

// Regression: TenantSQLInterceptor used to assert the query to an interface
// with WhereP, which generated *XQuery types don't have, so it silently
// skipped filtering and leaked other tenants' rows.
func TestTenantSQLInterceptor_GeneratedEnt(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:tenant_sql?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	bg := context.Background()
	a := client.Note.Create().SetTenantID("tenant-a").SetTitle("a1").SaveX(bg)
	client.Note.Create().SetTenantID("tenant-a").SetTitle("a2").SaveX(bg)
	b := client.Note.Create().SetTenantID("tenant-b").SetTitle("b1").SaveX(bg)

	client.Note.Intercept(privacy.TenantSQLInterceptor())

	ctxA := privacy.WithTenantID(bg, "tenant-a")
	ctxB := privacy.WithTenantID(bg, "tenant-b")

	if _, err := client.Note.Get(ctxB, a.ID); !testent.IsNotFound(err) {
		t.Fatalf("tenant-b fetched tenant-a's note: err=%v", err)
	}
	if got, err := client.Note.Get(ctxB, b.ID); err != nil || got.ID != b.ID {
		t.Fatalf("tenant-b own note: got=%v err=%v", got, err)
	}

	if n := client.Note.Query().CountX(ctxA); n != 2 {
		t.Fatalf("tenant-a count = %d, want 2", n)
	}
	for _, n := range client.Note.Query().AllX(ctxB) {
		if n.TenantID != "tenant-b" {
			t.Fatalf("tenant-b listed %s's note %d", n.TenantID, n.ID)
		}
	}
	if titles := client.Note.Query().Select(note.FieldTitle).StringsX(ctxB); len(titles) != 1 || titles[0] != "b1" {
		t.Fatalf("tenant-b select = %v", titles)
	}
	if ok := client.Note.Query().Where(note.ID(a.ID)).ExistX(ctxB); ok {
		t.Fatal("tenant-b sees tenant-a's note via Exist")
	}

	if _, err := client.Note.Query().All(bg); !errors.Is(err, privacy.ErrTenantMissing) {
		t.Fatalf("no tenant: err=%v, want ErrTenantMissing", err)
	}
	if n := client.Note.Query().CountX(privacy.WithBypassTenancy(bg)); n != 3 {
		t.Fatalf("bypass count = %d, want 3", n)
	}
}

type unsupportedQuery struct{}

// A query the interceptor cannot filter must fail, never run unfiltered.
func TestTenantSQLInterceptor_UnsupportedQueryFailsClosed(t *testing.T) {
	ctx := privacy.WithTenantID(context.Background(), "tenant-a")
	tr, ok := privacy.TenantSQLInterceptor().(ent.Traverser)
	if !ok {
		t.Fatal("interceptor is not a Traverser")
	}
	if err := tr.Traverse(ctx, &unsupportedQuery{}); !errors.Is(err, privacy.ErrTenantScopeUnsupported) {
		t.Fatalf("err=%v, want ErrTenantScopeUnsupported", err)
	}
}
