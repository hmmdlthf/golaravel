package database

import (
	"context"
	"errors"
	"testing"
)

type mockUserSeeder struct {
	invoked bool
}

func (m *mockUserSeeder) Seed(ctx context.Context, client any) error {
	m.invoked = true
	return nil
}

type mockPostSeeder struct {
	invoked bool
}

func (m *mockPostSeeder) Seed(ctx context.Context, client any) error {
	m.invoked = true
	return nil
}

type mockFailSeeder struct{}

func (m *mockFailSeeder) Seed(ctx context.Context, client any) error {
	return errors.New("simulated seeder error")
}

func TestSeederRunner(t *testing.T) {
	ctx := context.Background()
	runner := NewRunner("mock-client")
	runner.SetSilent(true)

	userSeeder := &mockUserSeeder{}
	postSeeder := &mockPostSeeder{}

	runner.Register("users", userSeeder)
	runner.Register("posts", postSeeder)

	if len(runner.Names()) != 2 {
		t.Fatalf("expected 2 seeders, got %d", len(runner.Names()))
	}

	// Run specific seeder
	if err := runner.Run(ctx, "users"); err != nil {
		t.Fatalf("unexpected error running users seeder: %v", err)
	}
	if !userSeeder.invoked {
		t.Errorf("expected userSeeder to be invoked")
	}
	if postSeeder.invoked {
		t.Errorf("expected postSeeder not to be invoked yet")
	}

	// Run all
	if err := runner.Run(ctx); err != nil {
		t.Fatalf("unexpected error running all seeders: %v", err)
	}
	if !postSeeder.invoked {
		t.Errorf("expected postSeeder to be invoked")
	}
}

func TestSeederRunner_Call(t *testing.T) {
	ctx := context.Background()
	runner := NewRunner(nil)
	runner.SetSilent(true)

	u := &mockUserSeeder{}
	p := &mockPostSeeder{}

	if err := runner.Call(ctx, u, p); err != nil {
		t.Fatalf("unexpected error in Call: %v", err)
	}
	if !u.invoked || !p.invoked {
		t.Errorf("expected both seeders to be invoked via Call")
	}
}

func TestSeederRunner_Fail(t *testing.T) {
	ctx := context.Background()
	runner := NewRunner(nil)
	runner.SetSilent(true)

	failSeeder := &mockFailSeeder{}
	err := runner.Call(ctx, failSeeder)
	if err == nil {
		t.Fatalf("expected error from mockFailSeeder")
	}
}

func TestSeederFunc(t *testing.T) {
	ctx := context.Background()
	runner := NewRunner("dummy-client")
	runner.SetSilent(true)

	invoked := false
	runner.RegisterFunc("quick", func(ctx context.Context, client any) error {
		invoked = true
		if client.(string) != "dummy-client" {
			t.Errorf("unexpected client: %v", client)
		}
		return nil
	})

	if err := runner.Run(ctx, "quick"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !invoked {
		t.Errorf("expected quick seeder function to be invoked")
	}
}
