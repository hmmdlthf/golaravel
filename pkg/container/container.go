package container

import (
	"fmt"
	"reflect"
	"sync"
)

// BindingType defines whether a dependency is a singleton, transient factory, or instance.
type BindingType int

const (
	BindingSingleton BindingType = iota
	BindingTransient
	BindingInstance
)

// binding represents a registered dependency inside the container.
type binding struct {
	kind     BindingType
	factory  any
	instance any
	lock     sync.Mutex
}

// Container defines the type-safe dependency injection container.
type Container struct {
	mu       sync.RWMutex
	bindings map[reflect.Type]*binding
	parent   *Container
}

// New creates a new isolated Container instance.
func New() *Container {
	return &Container{
		bindings: make(map[reflect.Type]*binding, 32),
	}
}

// Scoped creates a child container for request-scoped dependencies.
// Lookups cascade up to the parent container if not found in the child.
func (c *Container) Scoped() *Container {
	return &Container{
		bindings: make(map[reflect.Type]*binding, 8),
		parent:   c,
	}
}

// keyOf returns the reflect.Type of T without allocations.
func keyOf[T any]() reflect.Type {
	return reflect.TypeOf((*T)(nil)).Elem()
}

// Singleton registers a factory function as a thread-safe singleton.
// The factory will only be called once upon first resolution.
func Singleton[T any](c *Container, factory func(c *Container) (T, error)) {
	t := keyOf[T]()
	c.mu.Lock()
	defer c.mu.Unlock()

	c.bindings[t] = &binding{
		kind:    BindingSingleton,
		factory: factory,
	}
}

// Bind registers a transient factory. A new instance of T is produced on every Resolve.
func Bind[T any](c *Container, factory func(c *Container) (T, error)) {
	t := keyOf[T]()
	c.mu.Lock()
	defer c.mu.Unlock()

	c.bindings[t] = &binding{
		kind:    BindingTransient,
		factory: factory,
	}
}

// Instance registers an existing instance as a singleton.
func Instance[T any](c *Container, instance T) {
	t := keyOf[T]()
	c.mu.Lock()
	defer c.mu.Unlock()

	c.bindings[t] = &binding{
		kind:     BindingInstance,
		instance: instance,
	}
}

// Resolve retrieves the registered dependency of type T from the container.
// If not found in the current container and a parent exists, it searches the parent.
func Resolve[T any](c *Container) (T, error) {
	t := keyOf[T]()

	b, found := c.findBinding(t)
	if !found {
		var zero T
		return zero, fmt.Errorf("container: dependency not registered for type %s", t)
	}

	switch b.kind {
	case BindingInstance:
		return b.instance.(T), nil

	case BindingTransient:
		fn := b.factory.(func(c *Container) (T, error))
		return fn(c)

	case BindingSingleton:
		// Double-checked locking for thread-safe singleton initialization
		if b.instance != nil {
			return b.instance.(T), nil
		}
		b.lock.Lock()
		defer b.lock.Unlock()

		if b.instance != nil {
			return b.instance.(T), nil
		}

		fn := b.factory.(func(c *Container) (T, error))
		inst, err := fn(c)
		if err != nil {
			var zero T
			return zero, fmt.Errorf("container: failed to initialize singleton %s: %w", t, err)
		}
		b.instance = inst
		return inst, nil

	default:
		var zero T
		return zero, fmt.Errorf("container: unknown binding kind for type %s", t)
	}
}

// MustResolve retrieves the dependency of type T, panicking if an error occurs.
func MustResolve[T any](c *Container) T {
	val, err := Resolve[T](c)
	if err != nil {
		panic(err)
	}
	return val
}

// Has checks if a type T is registered in the container or its parent.
func Has[T any](c *Container) bool {
	t := keyOf[T]()
	_, found := c.findBinding(t)
	return found
}

func (c *Container) findBinding(t reflect.Type) (*binding, bool) {
	c.mu.RLock()
	b, ok := c.bindings[t]
	c.mu.RUnlock()

	if ok {
		return b, true
	}
	if c.parent != nil {
		return c.parent.findBinding(t)
	}
	return nil, false
}
