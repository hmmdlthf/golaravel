package database

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"time"
)

// Seeder defines the contract for populating database records.
type Seeder interface {
	Seed(ctx context.Context, client any) error
}

// SeederFunc allows using ordinary functions as Seeders.
type SeederFunc func(ctx context.Context, client any) error

// Seed calls f(ctx, client).
func (f SeederFunc) Seed(ctx context.Context, client any) error {
	return f(ctx, client)
}

// SeederResult stores the execution result of a single seeder.
type SeederResult struct {
	Name     string
	Duration time.Duration
	Error    error
}

// Runner manages the registration and execution of database seeders.
type Runner struct {
	mu      sync.RWMutex
	client  any
	seeders map[string]Seeder
	order   []string
	silent  bool
}

// NewRunner creates a new database seeder runner.
func NewRunner(client any) *Runner {
	return &Runner{
		client:  client,
		seeders: make(map[string]Seeder),
		order:   make([]string, 0),
	}
}

// SetClient sets the underlying database or ORM client (e.g. Ent client).
func (r *Runner) SetClient(client any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.client = client
}

// SetSilent controls whether progress is logged to standard output.
func (r *Runner) SetSilent(silent bool) {
	r.silent = silent
}

// Register registers a named seeder.
func (r *Runner) Register(name string, seeder Seeder) *Runner {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.seeders[name]; !exists {
		r.order = append(r.order, name)
	}
	r.seeders[name] = seeder
	return r
}

// RegisterFunc registers a seeder by function.
func (r *Runner) RegisterFunc(name string, fn SeederFunc) *Runner {
	return r.Register(name, fn)
}

// Call executes the provided seeders in sequential order.
// This allows a DatabaseSeeder to invoke other specific seeders.
func (r *Runner) Call(ctx context.Context, seeders ...Seeder) error {
	for _, seeder := range seeders {
		name := getSeederName(seeder)
		if !r.silent {
			fmt.Printf("  \033[33mSeeding:\033[0m %s...\n", name)
		}

		start := time.Now()
		if err := seeder.Seed(ctx, r.client); err != nil {
			if !r.silent {
				fmt.Printf("  \033[31mFailed:\033[0m  %s (%v)\n", name, err)
			}
			return fmt.Errorf("seeder [%s] failed: %w", name, err)
		}

		duration := time.Since(start)
		if !r.silent {
			fmt.Printf("  \033[32mSeeded:\033[0m  %s (%v)\n", name, duration.Round(time.Microsecond))
		}
	}
	return nil
}

// Run executes registered seeders. If names are specified, only those seeders run;
// otherwise, all registered seeders run in their registration order.
func (r *Runner) Run(ctx context.Context, names ...string) error {
	r.mu.RLock()
	var targets []Seeder
	if len(names) > 0 {
		for _, name := range names {
			s, exists := r.seeders[name]
			if !exists {
				r.mu.RUnlock()
				return fmt.Errorf("seeder [%s] not found", name)
			}
			targets = append(targets, s)
		}
	} else {
		for _, name := range r.order {
			targets = append(targets, r.seeders[name])
		}
	}
	r.mu.RUnlock()

	return r.Call(ctx, targets...)
}

// Names returns a copy of registered seeder names.
func (r *Runner) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	res := make([]string, len(r.order))
	copy(res, r.order)
	return res
}

func getSeederName(s Seeder) string {
	t := reflect.TypeOf(s)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	name := t.Name()
	if name == "" {
		return "AnonymousSeeder"
	}
	return name
}

// Global default seeder runner
var defaultRunner = NewRunner(nil)

// RegisterSeeder registers a seeder into the global runner.
func RegisterSeeder(name string, seeder Seeder) {
	defaultRunner.Register(name, seeder)
}

// RegisterSeederFunc registers a function seeder into the global runner.
func RegisterSeederFunc(name string, fn SeederFunc) {
	defaultRunner.RegisterFunc(name, fn)
}

// Seed runs the global seeders with the provided client.
func Seed(ctx context.Context, client any, names ...string) error {
	defaultRunner.SetClient(client)
	return defaultRunner.Run(ctx, names...)
}

// Call executes the provided seeders using the global runner.
func Call(ctx context.Context, client any, seeders ...Seeder) error {
	defaultRunner.SetClient(client)
	return defaultRunner.Call(ctx, seeders...)
}
