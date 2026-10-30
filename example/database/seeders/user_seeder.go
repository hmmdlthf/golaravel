package seeders

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/golaravel/golaravel/example/app"
	"github.com/golaravel/golaravel/pkg/hash"
)

// UserSeeder seeds initial administrative and team member accounts.
type UserSeeder struct{}

// NewUserSeeder creates a new UserSeeder instance.
func NewUserSeeder() *UserSeeder {
	return &UserSeeder{}
}

// Seed populates the users table with demo accounts.
func (s *UserSeeder) Seed(ctx context.Context, client any) error {
	c, ok := client.(*app.Client)
	if !ok {
		return fmt.Errorf("user seeder expected *app.Client, got %T", client)
	}

	slog.Info("Seeding initial users...")

	hashedPassword, err := hash.Make("password123")
	if err != nil {
		return fmt.Errorf("failed hashing password: %w", err)
	}

	// 1. Admin User
	_, err = c.User.Create().
		SetName("Ahmad (Admin)").
		SetEmail("admin@golaravel.dev").
		SetPassword(hashedPassword).
		SetRole("admin").
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed seeding admin user: %w", err)
	}

	// 2. Member User
	_, err = c.User.Create().
		SetName("Sarah Connor").
		SetEmail("sarah@golaravel.dev").
		SetPassword(hashedPassword).
		SetRole("member").
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed seeding member user: %w", err)
	}

	return nil
}
