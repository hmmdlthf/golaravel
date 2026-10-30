package seeders

import (
	"context"
	"log/slog"

	"github.com/golaravel/golaravel/pkg/database"
)

// DatabaseSeeder orchestrates the entire database seeding workflow.
type DatabaseSeeder struct{}

// NewDatabaseSeeder creates a root DatabaseSeeder instance.
func NewDatabaseSeeder() *DatabaseSeeder {
	return &DatabaseSeeder{}
}

// Seed executes all registered project seeders in logical sequence.
func (s *DatabaseSeeder) Seed(ctx context.Context, client any) error {
	slog.Info("Starting DatabaseSeeder pipeline...")

	runner := database.NewRunner(client)
	return runner.Call(ctx,
		NewUserSeeder(),
		NewInvoiceSeeder(),
	)
}
