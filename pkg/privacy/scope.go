package privacy

import (
	"context"

	"entgo.io/ent"
)

// Scope defines a reusable global query scope that modifies an Ent query before execution.
// Matches the architectural intent of Laravel's Illuminate\Database\Eloquent\Scope.
type Scope[Q any] interface {
	Apply(ctx context.Context, query Q) error
}

// ScopeFunc is an adapter to allow the use of ordinary functions as global scopes.
type ScopeFunc[Q any] func(ctx context.Context, query Q) error

// Apply calls f(ctx, query).
func (f ScopeFunc[Q]) Apply(ctx context.Context, query Q) error {
	return f(ctx, query)
}

// QueryScope creates an Ent Interceptor from a typed global scope.
// When an Ent query matching type Q is traversed, the scope is applied with zero reflection overhead.
func QueryScope[Q any](scope Scope[Q]) ent.Interceptor {
	return ent.TraverseFunc(func(ctx context.Context, q ent.Query) error {
		if typed, ok := q.(Q); ok {
			return scope.Apply(ctx, typed)
		}
		return nil
	})
}

// NewScope creates an Ent Interceptor from a ScopeFunc function.
func NewScope[Q any](fn func(ctx context.Context, query Q) error) ent.Interceptor {
	return QueryScope[Q](ScopeFunc[Q](fn))
}
