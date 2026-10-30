package main

import (
	"context"
	"log"
	"os"

	_ "github.com/mattn/go-sqlite3"

	"github.com/golaravel/golaravel/example/app"
	"github.com/golaravel/golaravel/example/database/seeders"
	"github.com/golaravel/golaravel/pkg/database"
)

func main() {
	ctx := context.Background()

	dsn := "file:ent?mode=memory&cache=shared&_fk=1"
	if custom := os.Getenv("DATABASE_URL"); custom != "" {
		dsn = custom
	} else if custom := os.Getenv("DB_DSN"); custom != "" {
		dsn = custom
	}

	client, err := app.Open("sqlite3", dsn)
	if err != nil {
		log.Fatalf("failed opening database connection: %v", err)
	}
	defer client.Close()

	// Ensure schema exists
	if err := client.Schema.Create(ctx); err != nil {
		log.Fatalf("failed syncing schema: %v", err)
	}

	runner := database.NewRunner(client)
	if err := runner.Call(ctx, seeders.NewDatabaseSeeder()); err != nil {
		log.Fatalf("seeding failed: %v", err)
	}
}
