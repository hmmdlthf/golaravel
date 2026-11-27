package features

import (
	"context"
	"fmt"
	"maps"
)

type fakeCtxKey struct{}

// Fake returns ctx with feature values forced for tests:
//
//	ctx = features.Fake(ctx, map[string]any{"invoices.recurring": true, "tax.mode": "none"})
//
// Faked keys resolve to the given values in every read through ctx (Get,
// Enabled, All, middleware). A faked key needs no tenant; other keys still
// do, and All and Public need one. Values are validated like Set; an
// invalid or unknown one makes reads fail. Fake calls nest: inner values
// win.
func Fake(ctx context.Context, values map[string]any) context.Context {
	merged := maps.Clone(fakesFrom(ctx))
	if merged == nil {
		merged = map[string]any{}
	}
	maps.Copy(merged, values)
	return context.WithValue(ctx, fakeCtxKey{}, merged)
}

func fakesFrom(ctx context.Context) map[string]any {
	f, _ := ctx.Value(fakeCtxKey{}).(map[string]any)
	return f
}

// fake returns the faked raw value for key, if any.
func (m *Manager) fake(ctx context.Context, d Definition) (string, bool, error) {
	v, ok := fakesFrom(ctx)[d.Meta().Key]
	if !ok {
		return "", false, nil
	}
	raw, err := d.encode(v)
	if err != nil {
		return "", false, fmt.Errorf("features.Fake: %w", err)
	}
	return string(raw), true, nil
}

// withFakes overlays faked values on a tenant's loaded values. Unknown
// faked keys are an error, so a typo in a test does not pass silently.
func (m *Manager) withFakes(ctx context.Context, vals map[string]string) (map[string]string, error) {
	fakes := fakesFrom(ctx)
	if len(fakes) == 0 {
		return vals, nil
	}
	out := make(map[string]string, len(vals)+len(fakes))
	maps.Copy(out, vals)
	for key := range fakes {
		d, err := m.definition(key)
		if err != nil {
			return nil, fmt.Errorf("features.Fake: %w", err)
		}
		raw, _, err := m.fake(ctx, d)
		if err != nil {
			return nil, err
		}
		out[key] = raw
	}
	return out, nil
}
