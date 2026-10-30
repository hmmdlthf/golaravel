package privacy

import (
	"context"
	"errors"
)

var (
	ErrTenantMissing  = errors.New("privacy/tenant: tenant id is missing from context")
	ErrTenantMismatch = errors.New("privacy/tenant: entity does not belong to the active tenant")
)

type contextKey string

const (
	tenantKey        contextKey = "golaravel_tenant_id"
	bypassTenancyKey contextKey = "golaravel_bypass_tenancy"
	withTrashedKey    contextKey = "golaravel_with_trashed"
	onlyTrashedKey   contextKey = "golaravel_only_trashed"
	forceDeleteKey   contextKey = "golaravel_force_delete"
)

// WithTenantID returns a new context containing the given tenant ID.
func WithTenantID(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, tenantKey, tenantID)
}

// TenantFromContext extracts the tenant ID from the context if present.
func TenantFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	val := ctx.Value(tenantKey)
	if val == nil {
		return "", false
	}
	tenantID, ok := val.(string)
	return tenantID, ok && tenantID != ""
}

// MustTenantFromContext extracts the tenant ID from the context or panics.
func MustTenantFromContext(ctx context.Context) string {
	tenantID, ok := TenantFromContext(ctx)
	if !ok {
		panic(ErrTenantMissing)
	}
	return tenantID
}

// WithBypassTenancy returns a context that bypasses tenant isolation (e.g. for super-admins or global background jobs).
func WithBypassTenancy(ctx context.Context) context.Context {
	return context.WithValue(ctx, bypassTenancyKey, true)
}

// IsTenancyBypassed reports whether tenant isolation is bypassed in the current context.
func IsTenancyBypassed(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	val := ctx.Value(bypassTenancyKey)
	if val == nil {
		return false
	}
	b, ok := val.(bool)
	return ok && b
}

// WithTrashed returns a context configured to include soft-deleted records in queries.
// Equivalent to Laravel's $model->withTrashed().
func WithTrashed(ctx context.Context) context.Context {
	return context.WithValue(ctx, withTrashedKey, true)
}

// IsWithTrashed reports whether the query should include soft-deleted records.
func IsWithTrashed(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	val := ctx.Value(withTrashedKey)
	if val == nil {
		return false
	}
	b, ok := val.(bool)
	return ok && b
}

// OnlyTrashed returns a context configured to query ONLY soft-deleted records.
// Equivalent to Laravel's $model->onlyTrashed().
func OnlyTrashed(ctx context.Context) context.Context {
	return context.WithValue(ctx, onlyTrashedKey, true)
}

// IsOnlyTrashed reports whether the query should only return soft-deleted records.
func IsOnlyTrashed(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	val := ctx.Value(onlyTrashedKey)
	if val == nil {
		return false
	}
	b, ok := val.(bool)
	return ok && b
}

// WithForceDelete returns a context marking the subsequent delete operation as a hard physical delete.
// Equivalent to Laravel's $model->forceDelete().
func WithForceDelete(ctx context.Context) context.Context {
	return context.WithValue(ctx, forceDeleteKey, true)
}

// IsForceDelete reports whether the deletion should physically remove the record.
func IsForceDelete(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	val := ctx.Value(forceDeleteKey)
	if val == nil {
		return false
	}
	b, ok := val.(bool)
	return ok && b
}
