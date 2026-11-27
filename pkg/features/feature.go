// Package features gives each tenant its own values for optional features
// and settings, with a default defined in code.
//
// A feature is declared once, in code, with a type and a default:
//
//	var (
//		RecurringInvoices = features.Bool("invoices.recurring", false).
//			Label("Recurring invoices").Group("Billing").ManagedBy(features.SuperAdmin)
//		TaxMode = features.Enum("tax.mode", "inclusive", "inclusive", "exclusive", "none").
//			Label("Tax mode").Group("Billing")
//		MaxBranches = features.Int("branches.max", 1).Rules(features.Range(1, 50))
//	)
//
// A tenant gets a row in the tenant_settings table only when its value
// differs from the default. Reading resolves
//
//	tenant row exists ? tenant row : code default
//
// so adding a feature needs no migration or seeding, and changing a default
// reaches every tenant that has not overridden it.
//
// The tenant comes from the context (privacy.WithTenantID), exactly as for
// tenant-scoped queries. Without one, reads and writes fail with
// privacy.ErrTenantMissing; with tenancy bypassed, reads return the default.
package features

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"sync/atomic"
)

var (
	// ErrInvalidValue is returned when a value is not allowed by the
	// feature's type, options or rules.
	ErrInvalidValue = errors.New("features: invalid value")
	// ErrUnknownFeature is returned for a key no feature is registered under.
	ErrUnknownFeature = errors.New("features: unknown feature")
	// ErrNotRegistered is returned when a feature is used before it is
	// registered with a Manager.
	ErrNotRegistered = errors.New("features: feature is not registered with a manager")
)

// Type is the kind of value a feature holds. User interfaces use it to
// pick a control (toggle, select, number input, ...).
type Type string

const (
	TypeBool   Type = "bool"
	TypeEnum   Type = "enum"
	TypeInt    Type = "int"
	TypeString Type = "string"
	TypeJSON   Type = "json"
)

// Role says who may change a feature for a tenant. The package records it;
// enforcing it is up to the caller (an admin panel, an HTTP handler).
type Role string

const (
	// TenantAdmin features are preferences a tenant may change itself.
	TenantAdmin Role = "tenant_admin"
	// SuperAdmin features are changed only by the central operator, for
	// example features sold per plan.
	SuperAdmin Role = "super_admin"
)

// Meta describes a feature for listings and settings pages.
type Meta struct {
	Key         string   `json:"key"`
	Type        Type     `json:"type"`
	Label       string   `json:"label,omitempty"`
	Description string   `json:"description,omitempty"`
	Group       string   `json:"group,omitempty"`
	ManagedBy   Role     `json:"managed_by"`
	Public      bool     `json:"public"`
	Options     []string `json:"options,omitempty"`
	Min         *int     `json:"min,omitempty"`
	Max         *int     `json:"max,omitempty"`
	Default     any      `json:"default"`
}

// Definition is a registered feature of any value type. Feature[T]
// implements it; it cannot be implemented outside this package.
type Definition interface {
	Meta() Meta
	bind(m *Manager) error
	checkDefault() error
	// decode parses and validates a stored value.
	decode(raw []byte) (any, error)
	// encode converts an untyped value (as from a form or CLI), validates
	// it and returns its stored JSON.
	encode(v any) ([]byte, error)
	defaultValue() any
}

// Rule is an extra check on a feature's value. Rules may also describe
// themselves in Meta (Range sets Min and Max).
type Rule[T any] struct {
	check func(T) error
	meta  func(*Meta)
}

// NewRule returns a Rule from a check function.
func NewRule[T any](check func(T) error) Rule[T] { return Rule[T]{check: check} }

// Range allows integers from min to max, inclusive.
func Range(min, max int) Rule[int] {
	return Rule[int]{
		check: func(v int) error {
			if v < min || v > max {
				return fmt.Errorf("must be between %d and %d", min, max)
			}
			return nil
		},
		meta: func(m *Meta) { m.Min, m.Max = &min, &max },
	}
}

// MaxLen limits a string to n bytes.
func MaxLen(n int) Rule[string] {
	return NewRule(func(v string) error {
		if len(v) > n {
			return fmt.Errorf("must be at most %d characters", n)
		}
		return nil
	})
}

// Feature is a feature or setting whose value has type T.
type Feature[T any] struct {
	meta  Meta
	def   T
	rules []Rule[T]
	mgr   atomic.Pointer[Manager]
}

func newFeature[T any](key string, typ Type, def T) *Feature[T] {
	return &Feature[T]{meta: Meta{Key: key, Type: typ, ManagedBy: TenantAdmin}, def: def}
}

// Bool declares an on/off feature.
func Bool(key string, def bool) *Feature[bool] { return newFeature(key, TypeBool, def) }

// Enum declares a setting that is one of a fixed list of options.
func Enum(key, def string, options ...string) *Feature[string] {
	f := newFeature(key, TypeEnum, def)
	f.meta.Options = slices.Clone(options)
	f.rules = append(f.rules, NewRule(func(v string) error {
		if !slices.Contains(options, v) {
			return fmt.Errorf("must be one of %v", options)
		}
		return nil
	}))
	return f
}

// Int declares an integer setting.
func Int(key string, def int) *Feature[int] { return newFeature(key, TypeInt, def) }

// String declares a free-text setting.
func String(key, def string) *Feature[string] { return newFeature(key, TypeString, def) }

// JSON declares a setting holding any JSON-encodable value.
func JSON[T any](key string, def T) *Feature[T] { return newFeature(key, TypeJSON, def) }

// Label sets the human-readable name.
func (f *Feature[T]) Label(s string) *Feature[T] { f.meta.Label = s; return f }

// Description sets a longer explanation for settings pages.
func (f *Feature[T]) Description(s string) *Feature[T] { f.meta.Description = s; return f }

// Group sets the section a settings page lists the feature under.
func (f *Feature[T]) Group(s string) *Feature[T] { f.meta.Group = s; return f }

// ManagedBy sets who may change the feature (default TenantAdmin).
func (f *Feature[T]) ManagedBy(r Role) *Feature[T] { f.meta.ManagedBy = r; return f }

// Public marks the feature as safe to send to frontends and clients.
func (f *Feature[T]) Public() *Feature[T] { f.meta.Public = true; return f }

// Rules adds checks every value must pass, the default included.
func (f *Feature[T]) Rules(rules ...Rule[T]) *Feature[T] {
	f.rules = append(f.rules, rules...)
	return f
}

// Key returns the feature's key.
func (f *Feature[T]) Key() string { return f.meta.Key }

// Default returns the code default.
func (f *Feature[T]) Default() T { return f.def }

// Meta describes the feature.
func (f *Feature[T]) Meta() Meta {
	m := f.meta
	m.Options = slices.Clone(m.Options)
	m.Default = f.def
	for _, r := range f.rules {
		if r.meta != nil {
			r.meta(&m)
		}
	}
	return m
}

// Get returns the active tenant's value, or the default when it has none.
func (f *Feature[T]) Get(ctx context.Context) (T, error) {
	m, err := f.manager()
	if err != nil {
		return f.def, err
	}
	v, _, err := m.resolve(ctx, f)
	if err != nil {
		return f.def, err
	}
	return v.(T), nil
}

// Value is Get that returns the default on any error (and logs it).
func (f *Feature[T]) Value(ctx context.Context) T {
	v, err := f.Get(ctx)
	if err != nil {
		if m := f.mgr.Load(); m != nil {
			m.log.WarnContext(ctx, "features: using default", "key", f.meta.Key, "error", err)
		}
	}
	return v
}

// Overridden reports whether the active tenant has its own value.
func (f *Feature[T]) Overridden(ctx context.Context) (bool, error) {
	m, err := f.manager()
	if err != nil {
		return false, err
	}
	_, ok, err := m.resolve(ctx, f)
	return ok, err
}

// Set stores v as the active tenant's value.
func (f *Feature[T]) Set(ctx context.Context, v T) error {
	m, err := f.manager()
	if err != nil {
		return err
	}
	if err := f.validate(v); err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrInvalidValue, f.meta.Key, err)
	}
	return m.write(ctx, f, raw)
}

// Reset removes the active tenant's value, so it gets the default again.
func (f *Feature[T]) Reset(ctx context.Context) error {
	m, err := f.manager()
	if err != nil {
		return err
	}
	return m.write(ctx, f, nil)
}

// Enabled reports whether a bool feature is on for the active tenant. It
// returns false on any error, so a broken lookup never turns a feature on.
func Enabled(ctx context.Context, f *Feature[bool]) bool {
	v, err := f.Get(ctx)
	return err == nil && v
}

// Is reports whether the active tenant's value equals want. It returns
// false on any error.
func Is[T comparable](ctx context.Context, f *Feature[T], want T) bool {
	v, err := f.Get(ctx)
	return err == nil && v == want
}

func (f *Feature[T]) manager() (*Manager, error) {
	m := f.mgr.Load()
	if m == nil {
		return nil, fmt.Errorf("%w: %s", ErrNotRegistered, f.meta.Key)
	}
	return m, nil
}

func (f *Feature[T]) validate(v T) error {
	for _, r := range f.rules {
		if err := r.check(v); err != nil {
			return fmt.Errorf("%w: %s: %v", ErrInvalidValue, f.meta.Key, err)
		}
	}
	return nil
}

var keyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)

func (f *Feature[T]) bind(m *Manager) error {
	if !f.mgr.CompareAndSwap(nil, m) && f.mgr.Load() != m {
		return fmt.Errorf("features: %s is already registered with another manager", f.meta.Key)
	}
	return nil
}

func (f *Feature[T]) checkDefault() error {
	if len(f.meta.Key) > 191 || !keyPattern.MatchString(f.meta.Key) {
		return fmt.Errorf("features: invalid key %q (use lowercase letters, digits, '.', '_', '-')", f.meta.Key)
	}
	if err := f.validate(f.def); err != nil {
		return fmt.Errorf("features: default of %s: %w", f.meta.Key, err)
	}
	return nil
}

func (f *Feature[T]) decode(raw []byte) (any, error) {
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrInvalidValue, f.meta.Key, err)
	}
	if err := f.validate(v); err != nil {
		return nil, err
	}
	return v, nil
}

func (f *Feature[T]) encode(in any) ([]byte, error) {
	if v, ok := in.(T); ok {
		if err := f.validate(v); err != nil {
			return nil, err
		}
		return json.Marshal(v)
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrInvalidValue, f.meta.Key, err)
	}
	v, err := f.decode(raw)
	if err != nil {
		// Form and CLI input arrives as text: "true", "5", `{"a":1}`.
		s, isString := in.(string)
		if !isString {
			return nil, err
		}
		if _, wantString := any(f.def).(string); wantString {
			return nil, err
		}
		if f.meta.Type == TypeBool {
			if b, perr := strconv.ParseBool(s); perr == nil {
				s = strconv.FormatBool(b)
			}
		}
		if v, err = f.decode([]byte(s)); err != nil {
			return nil, err
		}
	}
	return json.Marshal(v)
}

func (f *Feature[T]) defaultValue() any { return f.def }
