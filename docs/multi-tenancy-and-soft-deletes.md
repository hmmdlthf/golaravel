# Multi-Tenancy Privacy Scopes & Soft Deletes 🛡️ (PRD §2.3)

GoLaravel includes native, zero-reflection **Ent ORM Privacy Policies**, **Global Query Scopes**, **Automatic Soft Deletes**, and **Multi-Tenant Isolation Scopes** that enforce privacy and security directly at the query and mutation engine level.

---

## 1. Soft Deletes (`pkg/privacy`)

Soft deleting prevents accidental permanent data loss by populating a `deleted_at` timestamp rather than physically removing the database row.

### Adding Soft Deletes to an Ent Schema:
Include `privacy.SoftDeleteMixin{}` in your schema:

```go
package schema

import (
	"entgo.io/ent"
	"github.com/golaravel/golaravel/pkg/privacy"
)

type Invoice struct {
	ent.Schema
}

func (Invoice) Mixin() []ent.Mixin {
	return []ent.Mixin{
		privacy.SoftDeleteMixin{},
	}
}
```

### Registering Soft Delete Scopes & Hooks on Ent Client:
```go
import "github.com/golaravel/golaravel/pkg/privacy"

// Apply global query filter to exclude soft-deleted records:
client.Invoice.Intercept(privacy.SoftDeleteScope[*InvoiceQuery](
	func(q *InvoiceQuery) { q.Where(invoice.DeletedAtIsNil()) },
	func(q *InvoiceQuery) { q.Where(invoice.DeletedAtNotNil()) },
))

// Intercept DeleteOne / Delete to set deleted_at = time.Now():
client.Invoice.Use(privacy.SoftDeleteMutationHook("deleted_at"))
```

### Querying Soft Deleted Records:
By default, all queries automatically exclude records where `deleted_at IS NOT NULL`.

```go
// 1. Default query (only active, non-deleted records):
activeInvoices, err := client.Invoice.Query().All(ctx)

// 2. Query INCLUDING soft-deleted records ($model->withTrashed()):
ctxWithTrashed := privacy.WithTrashed(ctx)
allInvoices, err := client.Invoice.Query().All(ctxWithTrashed)

// 3. Query ONLY soft-deleted records ($model->onlyTrashed()):
ctxOnlyTrashed := privacy.OnlyTrashed(ctx)
trashedInvoices, err := client.Invoice.Query().All(ctxOnlyTrashed)

// 4. Permanently hard-delete a record from SQL ($model->forceDelete()):
ctxForceDelete := privacy.WithForceDelete(ctx)
err = client.Invoice.DeleteOne(inv).Exec(ctxForceDelete)

// 5. Restore a soft-deleted record:
err = client.Invoice.UpdateOne(inv).ClearDeletedAt().Exec(ctx)
```

---

## 2. Multi-Tenancy Privacy Scopes (`pkg/privacy`)

Multi-tenancy isolation guarantees that queries and mutations are strictly scoped to the active tenant, preventing cross-tenant data leakage.

### Adding Multi-Tenancy to an Ent Schema:
Include `privacy.TenantMixin{}` in your schema:

```go
func (Invoice) Mixin() []ent.Mixin {
	return []ent.Mixin{
		privacy.TenantMixin{},
	}
}
```

### Registering Tenant Scopes & Mutation Hooks:
```go
// Enforce tenant isolation on all queries:
client.Invoice.Intercept(privacy.TenantScope[*InvoiceQuery](func(q *InvoiceQuery, tenantID string) {
	q.Where(invoice.TenantEQ(tenantID))
}))

// Or, without writing a predicate per entity, the generic SQL interceptor
// (filters on the "tenant_id" column by default; pass another column name):
client.Invoice.Intercept(privacy.TenantSQLInterceptor())

// Auto-assign tenant_id on Create & prevent cross-tenant Update/Delete:
client.Invoice.Use(privacy.TenantMutationHook("tenant_id"))
```

### Automatic Tenant Protection Guarantees:
- **On Query**: If a query is executed without an active tenant in the context, it aborts with `ErrTenantMissing` instead of exposing global data.
- **Fails closed**: If `TenantSQLInterceptor` is attached to a query type it cannot filter (one without Ent's generated `Where(...predicate.X)` method), the query aborts with `ErrTenantScopeUnsupported` rather than running unfiltered. `TenantScope` is checked at compile time, so prefer it where the extra line per entity is acceptable.
- **On Create**: The `tenant_id` field is automatically injected into the created entity from the context. If a mismatched tenant ID is provided, it rejects the mutation with `ErrTenantMismatch`.
- **On Update/Delete**: Automatically appends `WHERE tenant_id = ?` at the storage level. A mutation the hook cannot scope or stamp with the tenant is refused with `ErrTenantScopeUnsupported`.

---

## 3. HTTP Multi-Tenancy Middleware

GoLaravel provides a flexible, modular middleware that resolves the active tenant ID from headers, subdomains, route parameters, or JWT claims:

```go
import "github.com/golaravel/golaravel/pkg/privacy"

// Multi-tenant middleware with priority fallback:
tenantMw := privacy.TenantMiddleware(
	privacy.HeaderResolver("X-Tenant-ID"),         // 1. Check Header
	privacy.SubdomainResolver("saas.myapp.com"),   // 2. Check Subdomain (e.g. acme.saas.myapp.com)
	privacy.ParamResolver("tenant"),               // 3. Check Route Parameter (/tenants/:tenant/...)
	privacy.JWTClaimResolver("tenant_id"),         // 4. Check JWT Bearer Claims
)

// Mount onto route group:
api := app.Group("/api", tenantMw)
api.GET("/invoices", InvoiceController.List)
```

### Built-in Resolvers:
| Resolver | Example Source | Output Tenant ID |
|---|---|---|
| `HeaderResolver("X-Tenant-ID")` | `X-Tenant-ID: acme_corp` | `acme_corp` |
| `SubdomainResolver("saas.com")` | Host: `starlight.saas.com:8080` | `starlight` |
| `ParamResolver("tenant")` | URL: `/tenants/:tenant/dashboard` | `param value` |
| `JWTClaimResolver("tenant_id")` | JWT Claims: `{"tenant_id": "cust_42"}` | `cust_42` |
| `QueryResolver("tenant")` | URL Query: `?tenant=acme` | `acme` |

### Administrative Bypass:
For global background jobs, analytics, or super-administrators, bypass tenant isolation using:

```go
superAdminCtx := privacy.WithBypassTenancy(ctx)
allGlobalInvoices, err := client.Invoice.Query().All(superAdminCtx)
```

---

## 4. Custom Global Scopes & Artisan Generator

You can generate custom global scopes (e.g., active status, regional constraints) using Artisan:

```bash
artisan make:scope ActiveScope
```

This creates `app/scopes/active_scope.go`:
```go
package scopes

import (
	"context"
)

type ActiveScope[Q any] struct{}

func (s *ActiveScope[Q]) Apply(ctx context.Context, query Q) error {
	// Add custom constraints to query
	return nil
}
```

Attach to your Ent client with:
```go
client.Invoice.Intercept(privacy.QueryScope[*InvoiceQuery](&scopes.ActiveScope[*InvoiceQuery]{}))
```
