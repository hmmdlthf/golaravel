package privacy

import (
	"context"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/sql"
)

// SoftDeleteScope returns a typed global scope for any query type Q.
// When whereNil is called, it appends a `deleted_at IS NULL` predicate.
// When whereNotNil is called, it appends a `deleted_at IS NOT NULL` predicate.
func SoftDeleteScope[Q any](whereNil func(q Q), whereNotNil func(q Q)) ent.Interceptor {
	return ent.TraverseFunc(func(ctx context.Context, q ent.Query) error {
		if typed, ok := q.(Q); ok {
			if IsOnlyTrashed(ctx) {
				whereNotNil(typed)
			} else if !IsWithTrashed(ctx) {
				whereNil(typed)
			}
		}
		return nil
	})
}

// SoftDeleteSQLInterceptor returns a generic Ent interceptor that uses SQL selectors
// to filter out soft-deleted rows using the given column name (default: "deleted_at").
func SoftDeleteSQLInterceptor(columnName ...string) ent.Interceptor {
	col := "deleted_at"
	if len(columnName) > 0 && columnName[0] != "" {
		col = columnName[0]
	}

	type wherePQuery interface {
		WhereP(...func(*sql.Selector))
	}

	return ent.TraverseFunc(func(ctx context.Context, q ent.Query) error {
		wq, ok := q.(wherePQuery)
		if !ok {
			return nil
		}

		if IsOnlyTrashed(ctx) {
			wq.WhereP(func(s *sql.Selector) {
				s.Where(sql.NotNull(s.C(col)))
			})
		} else if !IsWithTrashed(ctx) {
			wq.WhereP(func(s *sql.Selector) {
				s.Where(sql.IsNull(s.C(col)))
			})
		}
		return nil
	})
}

// SoftDeleteMutationHook returns an Ent mutation hook that intercepts OpDelete and OpDeleteOne.
// Unless WithForceDelete(ctx) is active, it mutates the operation into an update setting `deleted_at = time.Now()`.
func SoftDeleteMutationHook(columnName ...string) ent.Hook {
	col := "deleted_at"
	if len(columnName) > 0 && columnName[0] != "" {
		col = columnName[0]
	}

	type settableMutation interface {
		SetOp(ent.Op)
		SetField(string, ent.Value) error
	}

	return func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			// Only intercept delete operations
			if !m.Op().Is(ent.OpDelete | ent.OpDeleteOne) {
				return next.Mutate(ctx, m)
			}

			// Perform hard delete if requested via WithForceDelete
			if IsForceDelete(ctx) {
				return next.Mutate(ctx, m)
			}

			// Mutate delete into update
			sm, ok := m.(settableMutation)
			if !ok {
				return next.Mutate(ctx, m)
			}

			if m.Op().Is(ent.OpDeleteOne) {
				sm.SetOp(ent.OpUpdateOne)
			} else {
				sm.SetOp(ent.OpUpdate)
			}

			if err := sm.SetField(col, time.Now()); err != nil {
				return nil, err
			}

			return next.Mutate(ctx, m)
		})
	}
}
