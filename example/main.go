package main

import (
	"context"
	"log"
	"log/slog"
	"os"

	_ "github.com/mattn/go-sqlite3"

	"github.com/golaravel/golaravel/example/app"
	"github.com/golaravel/golaravel/example/app/handlers"
	"github.com/golaravel/golaravel/pkg/artisan"
	"github.com/golaravel/golaravel/pkg/container"
	"github.com/golaravel/golaravel/pkg/golaravel"
	"github.com/golaravel/golaravel/pkg/middleware"
)

func main() {
	ctx := context.Background()

	// 1. Initialize Ent ORM client with tuned SQLite in-memory database
	client, err := app.Open("sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	if err != nil {
		log.Fatalf("failed opening connection to sqlite: %v", err)
	}
	defer client.Close()

	// Run the auto-migration tool to create schema tables
	if err := client.Schema.Create(ctx); err != nil {
		log.Fatalf("failed creating schema resources: %v", err)
	}

	// Seed initial users into Ent database
	_, _ = client.User.Create().SetName("Taylor Otwell").SetEmail("taylor@laravel.com").Save(ctx)
	_, _ = client.User.Create().SetName("Rob Pike").SetEmail("rob@golang.org").Save(ctx)
	_, _ = client.User.Create().SetName("Linus Torvalds").SetEmail("torvalds@kernel.org").Save(ctx)

	// 2. Initialize GoLaravel Engine
	appEngine := golaravel.New()
	appEngine.Use(middleware.Recovery())
	appEngine.Use(middleware.SecureHeaders())
	appEngine.Use(middleware.CSRF())

	// 3. Register Ent client into the type-safe IoC Container
	container.Instance(appEngine.Container, client)

	// 4. Register and resolve controllers
	handlers.RegisterUserController(appEngine.Container)
	userCtrl := container.MustResolve[*handlers.UserController](appEngine.Container)

	// 5. Register Routes with names and groups
	appEngine.GET("/", userCtrl.HomeAction).Name("home")
	appEngine.GET("/users", userCtrl.Index).Name("users.index")
	appEngine.POST("/users", userCtrl.Store).Name("users.store")

	api := appEngine.Group("/api")
	api.GET("/users", userCtrl.ApiIndex).Name("api.users.index")

	// Print beautiful artisan route table
	artisan.PrintRoutes(appEngine.Router)

	// Read port from PORT env or default to :8080
	port := os.Getenv("PORT")
	if port == "" {
		port = ":8080"
	}

	slog.Info("Starting GoLaravel HTTP server", "url", "http://localhost"+port)
	if err := appEngine.Run(port); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
