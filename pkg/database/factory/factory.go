package factory

import (
	"context"
	"fmt"
	"sync"
)

// Factory represents a generic model factory capable of generating in-memory
// instances or persisting records directly into the database via Ent / ORM client.
type Factory[T any] struct {
	faker         *Faker
	count         int
	definition    func(f *Faker) T
	creator       func(ctx context.Context, client any, def T) (any, error)
	states        []func(m *T)
	sequences     []func(n int, m *T)
	afterMaking   []func(m *T)
	afterCreating []func(ctx context.Context, client any, entity any) error
}

// New creates a new generic model Factory.
func New[T any]() *Factory[T] {
	return &Factory[T]{
		faker:         defaultFaker,
		count:         1,
		states:        make([]func(m *T), 0),
		sequences:     make([]func(n int, m *T), 0),
		afterMaking:   make([]func(m *T), 0),
		afterCreating: make([]func(ctx context.Context, client any, entity any) error, 0),
	}
}

// clone creates an isolated shallow-copy of the factory with shared hooks.
func (f *Factory[T]) clone() *Factory[T] {
	return &Factory[T]{
		faker:         f.faker,
		count:         f.count,
		definition:    f.definition,
		creator:       f.creator,
		states:        append([]func(m *T){}, f.states...),
		sequences:     append([]func(n int, m *T){}, f.sequences...),
		afterMaking:   append([]func(m *T){}, f.afterMaking...),
		afterCreating: append([]func(ctx context.Context, client any, entity any) error{}, f.afterCreating...),
	}
}

// Faker assigns a custom Faker instance (e.g. for deterministic seeded generation).
func (f *Factory[T]) Faker(faker *Faker) *Factory[T] {
	cp := f.clone()
	cp.faker = faker
	return cp
}

// Definition registers the model's base attribute generator function.
func (f *Factory[T]) Definition(fn func(f *Faker) T) *Factory[T] {
	cp := f.clone()
	cp.definition = fn
	return cp
}

// Creator binds an Ent / ORM persistence callback that persists the model into the database.
func (f *Factory[T]) Creator(fn func(ctx context.Context, client any, def T) (any, error)) *Factory[T] {
	cp := f.clone()
	cp.creator = fn
	return cp
}

// Count specifies the number of models to create or make.
func (f *Factory[T]) Count(n int) *Factory[T] {
	cp := f.clone()
	if n <= 0 {
		n = 1
	}
	cp.count = n
	return cp
}

// State registers a mutation callback that alters the generated model attributes.
func (f *Factory[T]) State(fn func(m *T)) *Factory[T] {
	cp := f.clone()
	cp.states = append(cp.states, fn)
	return cp
}

// Sequence registers an indexed sequence mutation (0-indexed sequence counter).
func (f *Factory[T]) Sequence(fn func(index int, m *T)) *Factory[T] {
	cp := f.clone()
	cp.sequences = append(cp.sequences, fn)
	return cp
}

// AfterMaking registers a lifecycle hook executed immediately after each model is assembled in memory.
func (f *Factory[T]) AfterMaking(fn func(m *T)) *Factory[T] {
	cp := f.clone()
	cp.afterMaking = append(cp.afterMaking, fn)
	return cp
}

// AfterCreating registers a lifecycle hook executed immediately after each model is saved to the database.
func (f *Factory[T]) AfterCreating(fn func(ctx context.Context, client any, entity any) error) *Factory[T] {
	cp := f.clone()
	cp.afterCreating = append(cp.afterCreating, fn)
	return cp
}

// MakeOne builds a single model instance in-memory without saving it to the database.
func (f *Factory[T]) MakeOne() T {
	results := f.Make(1)
	return results[0]
}

// Make builds a slice of models in-memory according to the configured count.
func (f *Factory[T]) Make(overrideCount ...int) []T {
	if f.definition == nil {
		panic("factory: definition function not configured")
	}

	total := f.count
	if len(overrideCount) > 0 && overrideCount[0] > 0 {
		total = overrideCount[0]
	}

	results := make([]T, total)
	for i := 0; i < total; i++ {
		item := f.definition(f.faker)

		// Apply states
		for _, state := range f.states {
			state(&item)
		}

		// Apply sequences
		for _, seq := range f.sequences {
			seq(i, &item)
		}

		// Apply after making hooks
		for _, hook := range f.afterMaking {
			hook(&item)
		}

		results[i] = item
	}

	return results
}

// CreateOne persists a single model into the database via the configured Creator.
func (f *Factory[T]) CreateOne(ctx context.Context, client any) (any, error) {
	results, err := f.Create(ctx, client, 1)
	if err != nil {
		return nil, err
	}
	return results[0], nil
}

// Create persists the configured count of models into the database.
func (f *Factory[T]) Create(ctx context.Context, client any, overrideCount ...int) ([]any, error) {
	if f.creator == nil {
		return nil, fmt.Errorf("factory: creator callback not configured (cannot persist models without client creator)")
	}

	defs := f.Make(overrideCount...)
	entities := make([]any, len(defs))

	for i, def := range defs {
		entity, err := f.creator(ctx, client, def)
		if err != nil {
			return nil, fmt.Errorf("factory: failed persisting entity at index %d: %w", i, err)
		}

		// Trigger after creating hooks
		for _, hook := range f.afterCreating {
			if err := hook(ctx, client, entity); err != nil {
				return nil, fmt.Errorf("factory: after-creating hook failed at index %d: %w", i, err)
			}
		}

		entities[i] = entity
	}

	return entities, nil
}

// Factory Registry for named factories

var (
	registryMu sync.RWMutex
	factories  = make(map[string]any)
)

// Register stores a named factory in the global registry.
func Register[T any](name string, f *Factory[T]) {
	registryMu.Lock()
	defer registryMu.Unlock()
	factories[name] = f
}

// Get retrieves a named factory from the global registry.
func Get[T any](name string) (*Factory[T], error) {
	registryMu.RLock()
	defer registryMu.RUnlock()

	raw, ok := factories[name]
	if !ok {
		return nil, fmt.Errorf("factory: '%s' not registered", name)
	}

	f, ok := raw.(*Factory[T])
	if !ok {
		return nil, fmt.Errorf("factory: '%s' registered with incompatible type %T", name, raw)
	}

	return f, nil
}

// ClearRegistry clears all registered factories.
func ClearRegistry() {
	registryMu.Lock()
	defer registryMu.Unlock()
	factories = make(map[string]any)
}
