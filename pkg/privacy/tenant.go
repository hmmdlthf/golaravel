package privacy

import (
	"context"

	"entgo.io/ent"
	"entgo.io/ent/dialect/sql"
)

// TenantScope returns a typed global scope that filters any query type Q by tenant_id.
// If no tenant is present in the context and tenancy is not bypassed, it returns ErrTenantMissing.
func TenantScope[Q any](whereTenantEQ func(q Q, tenantID string)) ent.Interceptor {
	return ent.TraverseFunc(func(ctx context.Context, q ent.Query) error {
		if IsTenancyBypassed(ctx) {
			return nil
		}

		tenantID, ok := TenantFromContext(ctx)
		if !ok || tenantID == "" {
			return ErrTenantMissing
		}

		if typed, ok := q.(Q); ok {
			whereTenantEQ(typed, tenantID)
		}
		return nil
	})
}

// TenantSQLInterceptor returns a generic Ent interceptor that uses SQL selectors
// to enforce tenant isolation using the given column name (default: "tenant_id").
func TenantSQLInterceptor(columnName ...string) ent.Interceptor {
	col := "tenant_id"
	if len(columnName) > 0 && columnName[0] != "" {
		col = columnName[0]
	}

	type wherePQuery interface {
		WhereP(...func(*sql.Selector))
	}

	return ent.TraverseFunc(func(ctx context.Context, q ent.Query) error {
		if IsTenancyBypassed(ctx) {
			return nil
		}

		tenantID, ok := TenantFromContext(ctx)
		if !ok || tenantID == "" {
			return ErrTenantMissing
		}

		wq, ok := q.(wherePQuery)
		if !ok {
			return nil
		}

		wq.WhereP(func(s *sql.Selector) {
			s.Where(sql.EQ(s.C(col), tenantID))
		})
		return nil
	})
}

// TenantMutationHook enforces tenant isolation on create, update, and delete mutations.
// On OpCreate, it automatically populates tenant_id from the context if not set, or verifies
// that any provided tenant_id matches the active context tenant.
// On OpUpdate/OpDelete, it appends a WHERE tenant_id = ? clause to prevent cross-tenant mutations.
func TenantMutationHook(columnName ...string) ent.Hook {
	col := "tenant_id"
	if len(columnName) > 0 && columnName[0] != "" {
		col = columnName[0]
	}

	type fieldGetterMutation interface {
		ent.Mutation
		Field(string) (ent.Value, bool)
	}

	type settableMutation interface {
		SetField(string, ent.Value) error
	}

	type wherePMutation interface {
		WhereP(...func(*sql.Selector))
	}

	return func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if IsTenancyBypassed(ctx) {
				return next.Mutate(ctx, m)
			}

			tenantID, ok := TenantFromContext(ctx)
			if !ok || tenantID == "" {
				return nil, ErrTenantMissing
			}

			// On Create: auto-set or verify tenant ID
			if m.Op().Is(ent.OpCreate) {
				if fg, ok := m.(fieldGetterMutation); ok {
					if existingVal, exists := fg.Field(col); exists {
						if existingStr, ok := existingVal.(string); ok && existingStr != "" && existingStr != tenantID {
							return nil, ErrTenantMismatch
						}
					}
				}
				if sm, ok := m.(settableMutation); ok {
					_ = sm.SetField(col, tenantID)
				}
			}

			// On Update/Delete: enforce tenant scope at storage level
			if m.Op().Is(ent.OpUpdate | ent.OpUpdateOne | ent.OpDelete | ent.OpDeleteOne) {
				if wm, ok := m.(wherePMutation); ok {
					wm.WhereP(func(s *sql.Selector) {
						s.Where(sql.EQ(s.C(col), tenantID))
					})
				}
			}

			return next.Mutate(ctx, m)
		})
	}
}
