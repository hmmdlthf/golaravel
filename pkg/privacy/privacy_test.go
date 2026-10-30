package privacy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/sql"
	"github.com/golaravel/golaravel/pkg/auth"
	gcontext "github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/golaravel"
)

// Mock Ent Query for testing typed query scopes
type MockInvoiceQuery struct {
	WherePredicates []string
}

func (q *MockInvoiceQuery) WhereDeletedAtIsNil() {
	q.WherePredicates = append(q.WherePredicates, "deleted_at IS NULL")
}

func (q *MockInvoiceQuery) WhereDeletedAtNotNil() {
	q.WherePredicates = append(q.WherePredicates, "deleted_at IS NOT NULL")
}

func (q *MockInvoiceQuery) WhereTenantEQ(tenantID string) {
	q.WherePredicates = append(q.WherePredicates, "tenant_id = "+tenantID)
}

func (q *MockInvoiceQuery) WhereP(ps ...func(*sql.Selector)) {
	s := sql.Select().From(sql.Table("invoices"))
	for _, p := range ps {
		p(s)
	}
	queryStr, _ := s.Query()
	q.WherePredicates = append(q.WherePredicates, queryStr)
}

// Mock Ent Mutation for testing hooks
type MockMutation struct {
	op     ent.Op
	fields map[string]ent.Value
	wheres []func(*sql.Selector)
}

func (m *MockMutation) Op() ent.Op                      { return m.op }
func (m *MockMutation) SetOp(op ent.Op)                 { m.op = op }
func (m *MockMutation) Type() string                    { return "Mock" }
func (m *MockMutation) WhereP(ps ...func(*sql.Selector)) { m.wheres = append(m.wheres, ps...) }
func (m *MockMutation) SetField(name string, value ent.Value) error {
	if m.fields == nil {
		m.fields = make(map[string]ent.Value)
	}
	m.fields[name] = value
	return nil
}
func (m *MockMutation) Field(name string) (ent.Value, bool) {
	if m.fields == nil {
		return nil, false
	}
	v, ok := m.fields[name]
	return v, ok
}
func (m *MockMutation) Fields() []string {
	keys := make([]string, 0, len(m.fields))
	for k := range m.fields {
		keys = append(keys, k)
	}
	return keys
}
func (m *MockMutation) AddedFields() []string                      { return nil }
func (m *MockMutation) AddedField(string) (ent.Value, bool)         { return nil, false }
func (m *MockMutation) AddField(string, ent.Value) error           { return nil }
func (m *MockMutation) ClearedFields() []string                    { return nil }
func (m *MockMutation) FieldCleared(string) bool                   { return false }
func (m *MockMutation) ClearField(string) error                    { return nil }
func (m *MockMutation) ResetField(string) error                    { return nil }
func (m *MockMutation) AddedEdges() []string                       { return nil }
func (m *MockMutation) AddedIDs(string) []ent.Value                { return nil }
func (m *MockMutation) RemovedEdges() []string                     { return nil }
func (m *MockMutation) RemovedIDs(string) []ent.Value              { return nil }
func (m *MockMutation) ClearedEdges() []string                     { return nil }
func (m *MockMutation) EdgeCleared(string) bool                    { return false }
func (m *MockMutation) ClearEdge(string) error                     { return nil }
func (m *MockMutation) ResetEdge(string) error                     { return nil }
func (m *MockMutation) OldField(context.Context, string) (ent.Value, error) { return nil, nil }

func TestPrivacy_ContextHelpers(t *testing.T) {
	ctx := context.Background()

	// Tenant helpers
	if _, ok := TenantFromContext(ctx); ok {
		t.Fatal("expected no tenant in bare context")
	}

	tCtx := WithTenantID(ctx, "tenant_123")
	tenantID, ok := TenantFromContext(tCtx)
	if !ok || tenantID != "tenant_123" {
		t.Fatalf("expected tenant_123, got %s", tenantID)
	}
	if MustTenantFromContext(tCtx) != "tenant_123" {
		t.Fatalf("expected MustTenantFromContext to return tenant_123")
	}

	// Bypass tenancy
	if IsTenancyBypassed(ctx) {
		t.Fatal("expected tenancy not bypassed by default")
	}
	bCtx := WithBypassTenancy(ctx)
	if !IsTenancyBypassed(bCtx) {
		t.Fatal("expected tenancy bypassed")
	}

	// Soft delete helpers
	if IsWithTrashed(ctx) || IsOnlyTrashed(ctx) || IsForceDelete(ctx) {
		t.Fatal("expected soft delete flags false by default")
	}

	wtCtx := WithTrashed(ctx)
	if !IsWithTrashed(wtCtx) {
		t.Fatal("expected WithTrashed true")
	}

	otCtx := OnlyTrashed(ctx)
	if !IsOnlyTrashed(otCtx) {
		t.Fatal("expected OnlyTrashed true")
	}

	fdCtx := WithForceDelete(ctx)
	if !IsForceDelete(fdCtx) {
		t.Fatal("expected ForceDelete true")
	}
}

func TestPrivacy_SoftDeleteScope(t *testing.T) {
	interceptor := SoftDeleteScope[*MockInvoiceQuery](
		func(q *MockInvoiceQuery) { q.WhereDeletedAtIsNil() },
		func(q *MockInvoiceQuery) { q.WhereDeletedAtNotNil() },
	)

	traverser, ok := interceptor.(ent.Traverser)
	if !ok {
		t.Fatalf("expected interceptor to implement ent.Traverser")
	}

	// 1. Default: adds deleted_at IS NULL
	q1 := &MockInvoiceQuery{}
	_ = traverser.Traverse(context.Background(), q1)
	if len(q1.WherePredicates) != 1 || q1.WherePredicates[0] != "deleted_at IS NULL" {
		t.Fatalf("expected deleted_at IS NULL predicate, got: %v", q1.WherePredicates)
	}

	// 2. WithTrashed: does NOT add deleted_at filter
	q2 := &MockInvoiceQuery{}
	_ = traverser.Traverse(WithTrashed(context.Background()), q2)
	if len(q2.WherePredicates) != 0 {
		t.Fatalf("expected no predicate for WithTrashed, got: %v", q2.WherePredicates)
	}

	// 3. OnlyTrashed: adds deleted_at IS NOT NULL
	q3 := &MockInvoiceQuery{}
	_ = traverser.Traverse(OnlyTrashed(context.Background()), q3)
	if len(q3.WherePredicates) != 1 || q3.WherePredicates[0] != "deleted_at IS NOT NULL" {
		t.Fatalf("expected deleted_at IS NOT NULL predicate, got: %v", q3.WherePredicates)
	}
}

func TestPrivacy_SoftDeleteMutationHook(t *testing.T) {
	hook := SoftDeleteMutationHook("deleted_at")

	// 1. Soft delete on OpDeleteOne converts to OpUpdateOne
	m1 := &MockMutation{op: ent.OpDeleteOne}
	mutator1 := hook(ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
		return "mutated", nil
	}))

	_, err := mutator1.Mutate(context.Background(), m1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !m1.op.Is(ent.OpUpdateOne) {
		t.Fatalf("expected OpUpdateOne, got %v", m1.op)
	}
	if _, ok := m1.fields["deleted_at"].(time.Time); !ok {
		t.Fatalf("expected deleted_at timestamp in fields, got %v", m1.fields)
	}

	// 2. Hard delete on OpDeleteOne with WithForceDelete stays OpDeleteOne
	m2 := &MockMutation{op: ent.OpDeleteOne}
	mutator2 := hook(ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
		return "force_deleted", nil
	}))

	_, err = mutator2.Mutate(WithForceDelete(context.Background()), m2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !m2.op.Is(ent.OpDeleteOne) {
		t.Fatalf("expected OpDeleteOne preserved with force delete, got %v", m2.op)
	}
}

func TestPrivacy_TenantScope(t *testing.T) {
	interceptor := TenantScope[*MockInvoiceQuery](func(q *MockInvoiceQuery, tenantID string) {
		q.WhereTenantEQ(tenantID)
	})

	traverser, ok := interceptor.(ent.Traverser)
	if !ok {
		t.Fatal("expected Traverser")
	}

	// 1. Missing tenant returns ErrTenantMissing
	q1 := &MockInvoiceQuery{}
	err := traverser.Traverse(context.Background(), q1)
	if err != ErrTenantMissing {
		t.Fatalf("expected ErrTenantMissing, got %v", err)
	}

	// 2. Valid tenant applies tenant predicate
	q2 := &MockInvoiceQuery{}
	err = traverser.Traverse(WithTenantID(context.Background(), "acme_corp"), q2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(q2.WherePredicates) != 1 || q2.WherePredicates[0] != "tenant_id = acme_corp" {
		t.Fatalf("expected tenant_id = acme_corp predicate, got: %v", q2.WherePredicates)
	}

	// 3. Bypass tenancy skips filter and allows execution
	q3 := &MockInvoiceQuery{}
	err = traverser.Traverse(WithBypassTenancy(context.Background()), q3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(q3.WherePredicates) != 0 {
		t.Fatalf("expected no predicates when bypassed")
	}
}

func TestPrivacy_TenantMutationHook(t *testing.T) {
	hook := TenantMutationHook("tenant_id")

	// 1. Missing tenant aborts
	m1 := &MockMutation{op: ent.OpCreate}
	mutator1 := hook(ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
		return nil, nil
	}))
	_, err := mutator1.Mutate(context.Background(), m1)
	if err != ErrTenantMissing {
		t.Fatalf("expected ErrTenantMissing, got %v", err)
	}

	// 2. Auto-sets tenant_id on Create
	m2 := &MockMutation{op: ent.OpCreate}
	_, err = mutator1.Mutate(WithTenantID(context.Background(), "tenant_alpha"), m2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m2.fields["tenant_id"] != "tenant_alpha" {
		t.Fatalf("expected tenant_alpha in fields, got %v", m2.fields["tenant_id"])
	}

	// 3. Tenant mismatch returns ErrTenantMismatch
	m3 := &MockMutation{
		op: ent.OpCreate,
		fields: map[string]ent.Value{
			"tenant_id": "tenant_hacker",
		},
	}
	_, err = mutator1.Mutate(WithTenantID(context.Background(), "tenant_legit"), m3)
	if err != ErrTenantMismatch {
		t.Fatalf("expected ErrTenantMismatch, got %v", err)
	}

	// 4. On OpUpdate, appends WHERE tenant_id = ?
	m4 := &MockMutation{op: ent.OpUpdate}
	_, err = mutator1.Mutate(WithTenantID(context.Background(), "tenant_beta"), m4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m4.wheres) != 1 {
		t.Fatalf("expected 1 WhereP predicate appended for tenant isolation")
	}
}

func TestPrivacy_TenantMiddleware(t *testing.T) {
	jwt := auth.NewJWTManager("test-secret-32-chars-long-here!!", 1*time.Hour)
	jwtToken, _ := jwt.GenerateToken("user_1", map[string]any{
		"tenant_id": "jwt_tenant_99",
	})

	app := golaravel.New()

	// Register multi-tenant route group
	mw := TenantMiddleware(
		HeaderResolver("X-Tenant-ID"),
		SubdomainResolver("saas.com"),
		ParamResolver("tenant"),
		QueryResolver("tenant"),
		JWTClaimResolver("tenant_id"),
	)

	// Auth guard for JWT testing
	authMw := auth.BearerGuard(jwt)

	app.GET("/api/data", mw, func(c *gcontext.Context) error {
		tenantID, ok := TenantFromContext(c.Context())
		if !ok {
			return c.String(http.StatusInternalServerError, "Missing tenant in ctx")
		}
		return c.JSON(http.StatusOK, map[string]string{"tenant": tenantID})
	})

	app.GET("/tenants/:tenant/dashboard", mw, func(c *gcontext.Context) error {
		tenantID := MustTenantFromContext(c.Context())
		return c.JSON(http.StatusOK, map[string]string{"tenant": tenantID})
	})

	app.GET("/api/jwt-tenant", authMw, mw, func(c *gcontext.Context) error {
		tenantID := MustTenantFromContext(c.Context())
		return c.JSON(http.StatusOK, map[string]string{"tenant": tenantID})
	})

	// 1. Resolve via Header
	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/api/data", nil)
	req1.Header.Set("X-Tenant-ID", "header_tenant_1")
	app.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w1.Code, w1.Body.String())
	}

	// 2. Resolve via Subdomain
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/api/data", nil)
	req2.Host = "starlight.saas.com:8080"
	app.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 for subdomain, got %d: %s", w2.Code, w2.Body.String())
	}

	// 3. Resolve via Route Param
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("GET", "/tenants/param_corp/dashboard", nil)
	app.ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Fatalf("expected 200 for param, got %d: %s", w3.Code, w3.Body.String())
	}

	// 4. Resolve via JWT claim
	w4 := httptest.NewRecorder()
	req4 := httptest.NewRequest("GET", "/api/jwt-tenant", nil)
	req4.Header.Set("Authorization", "Bearer "+jwtToken)
	app.ServeHTTP(w4, req4)
	if w4.Code != http.StatusOK {
		t.Fatalf("expected 200 for jwt claim tenant, got %d: %s", w4.Code, w4.Body.String())
	}

	// 5. Aborts with 403 Forbidden when missing
	w5 := httptest.NewRecorder()
	req5 := httptest.NewRequest("GET", "/api/data", nil)
	app.ServeHTTP(w5, req5)
	if w5.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden when tenant missing, got %d", w5.Code)
	}
}
