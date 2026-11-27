package privacy

import (
	"context"
	"fmt"
	"reflect"

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

// TenantSQLInterceptor returns a generic Ent interceptor that filters every query
// by the given tenant column (default: "tenant_id") through a storage-level
// predicate. It works on any Ent-generated query (`*XQuery`): it calls the
// query's `Where(ps ...predicate.X)` method with a predicate that adds
// `WHERE <col> = <tenant>` to the query's selector.
//
// It fails closed: if the query type has no such Where method, the query
// aborts with ErrTenantScopeUnsupported instead of running unfiltered.
// Without a tenant in the context (and no bypass), it returns ErrTenantMissing.
//
// The typed TenantScope is the preferred API where the generated
// `x.TenantIDEQ` predicate is available, since it is checked at compile time.
func TenantSQLInterceptor(columnName ...string) ent.Interceptor {
	col := "tenant_id"
	if len(columnName) > 0 && columnName[0] != "" {
		col = columnName[0]
	}

	return ent.TraverseFunc(func(ctx context.Context, q ent.Query) error {
		if IsTenancyBypassed(ctx) {
			return nil
		}

		tenantID, ok := TenantFromContext(ctx)
		if !ok || tenantID == "" {
			return ErrTenantMissing
		}

		return applySelectorPredicate(q, func(s *sql.Selector) {
			s.Where(sql.EQ(s.C(col), tenantID))
		})
	})
}

var selectorFuncType = reflect.TypeOf((func(*sql.Selector))(nil))

// applySelectorPredicate adds a storage-level predicate to an Ent query.
// Generated queries expose `Where(ps ...predicate.X) *XQuery`, where
// predicate.X is a named `func(*sql.Selector)`. No Go interface can match
// that across entity types, so the method is found by reflection and the
// predicate converted to the query's own predicate type.
func applySelectorPredicate(q ent.Query, p func(*sql.Selector)) error {
	if wq, ok := q.(interface{ WhereP(...func(*sql.Selector)) }); ok {
		wq.WhereP(p)
		return nil
	}

	v := reflect.ValueOf(q)
	if !v.IsValid() {
		return fmt.Errorf("%w: nil query", ErrTenantScopeUnsupported)
	}
	m := v.MethodByName("Where")
	if !m.IsValid() {
		return fmt.Errorf("%w: %T has no Where method", ErrTenantScopeUnsupported, q)
	}
	mt := m.Type()
	if !mt.IsVariadic() || mt.NumIn() != 1 {
		return fmt.Errorf("%w: %T.Where is not variadic", ErrTenantScopeUnsupported, q)
	}
	elem := mt.In(0).Elem()
	if !selectorFuncType.ConvertibleTo(elem) || elem.Kind() != reflect.Func {
		return fmt.Errorf("%w: %T.Where takes %s, not a func(*sql.Selector) predicate", ErrTenantScopeUnsupported, q, elem)
	}
	pv := reflect.ValueOf(p).Convert(elem)
	m.Call([]reflect.Value{pv})
	return nil
}

// TenantMutationHook enforces tenant isolation on create, update, and delete mutations.
// On OpCreate, it automatically populates tenant_id from the context if not set, or verifies
// that any provided tenant_id matches the active context tenant.
// On OpUpdate/OpDelete, it appends a WHERE tenant_id = ? clause to prevent cross-tenant mutations.
// It fails closed: a mutation it cannot set the tenant on (create) or scope
// (update/delete) is refused with ErrTenantScopeUnsupported instead of running.
func TenantMutationHook(columnName ...string) ent.Hook {
	col := "tenant_id"
	if len(columnName) > 0 && columnName[0] != "" {
		col = columnName[0]
	}

	type fieldGetterMutation interface {
		ent.Mutation
		Field(string) (ent.Value, bool)
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
				if err := m.SetField(col, tenantID); err != nil {
					return nil, fmt.Errorf("%w: setting %q on %T: %v", ErrTenantScopeUnsupported, col, m, err)
				}
			}

			// On Update/Delete: enforce tenant scope at storage level
			if m.Op().Is(ent.OpUpdate | ent.OpUpdateOne | ent.OpDelete | ent.OpDeleteOne) {
				wm, ok := m.(wherePMutation)
				if !ok {
					return nil, fmt.Errorf("%w: %T has no WhereP method", ErrTenantScopeUnsupported, m)
				}
				wm.WhereP(func(s *sql.Selector) {
					s.Where(sql.EQ(s.C(col), tenantID))
				})
			}

			return next.Mutate(ctx, m)
		})
	}
}
