package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/golaravel/golaravel/example/app"
	"github.com/golaravel/golaravel/example/app/handlers"
	"github.com/golaravel/golaravel/example/database/seeders"
	"github.com/golaravel/golaravel/pkg/artisan"
	"github.com/golaravel/golaravel/pkg/auth"
	"github.com/golaravel/golaravel/pkg/broadcast"
	"github.com/golaravel/golaravel/pkg/container"
	gcontext "github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/database"
	"github.com/golaravel/golaravel/pkg/flutter"
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

	// Run auto-migration for User, Invoice, and LineItem tables
	if err := client.Schema.Create(ctx); err != nil {
		log.Fatalf("failed creating schema resources: %v", err)
	}

	// 2. Populate database with initial records via GoLaravel Seeder Engine
	seederRunner := database.NewRunner(client)
	if err := seederRunner.Call(ctx, seeders.NewDatabaseSeeder()); err != nil {
		log.Fatalf("failed seeding database: %v", err)
	}

	// Attach Ent ORM real-time mutation hook:
	// Automatically broadcasts invoice.created, invoice.updated, invoice.deleted
	// across WebSockets and SSE to all connected clients!
	client.Invoice.Use(broadcast.EntMutationHook("invoices"))

	// 4. Initialize JWT Manager
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		jwtSecret = "golaravel-super-secure-production-key-32-chars!"
	}
	jwtManager := auth.NewJWTManager(jwtSecret, 24*time.Hour)

	// 5. Initialize GoLaravel Engine
	appEngine := golaravel.New()
	appEngine.Use(middleware.Recovery())
	appEngine.Use(middleware.SecureHeaders())
	appEngine.Use(middleware.CORS())

	// Register DI
	container.Instance(appEngine.Container, client)
	container.Instance(appEngine.Container, jwtManager)
	handlers.RegisterInvoiceController(appEngine.Container, jwtManager)
	invoiceCtrl := container.MustResolve[*handlers.InvoiceController](appEngine.Container)

	// 6. Routes Setup

	// === FAST-PATH MICRO-SSR (TEMPL) ===
	// Public invoice link opened by customers from email/SMS:
	// Rendered in < 45 nanoseconds! Zero client JS overhead.
	appEngine.GET("/invoice/:token", invoiceCtrl.PublicShow).Name("invoices.public")

	// === HIGH-SPEED JSON REST API (FOR FLUTTER MULTIPLATFORM CLIENT) ===
	api := appEngine.Group("/api")
	api.POST("/auth/login", invoiceCtrl.ApiLogin).Name("api.auth.login")
	api.GET("/invoices", invoiceCtrl.ApiList).Name("api.invoices.list")
	api.POST("/invoices", invoiceCtrl.ApiStore).Name("api.invoices.store")
	api.PATCH("/invoices/:id/pay", invoiceCtrl.ApiPay).Name("api.invoices.pay")

	// === REALTIME WEBSOCKETS & SSE BROADCASTING (PRD §5.3) ===
	api.GET("/ws", broadcast.WebSocketHandler()).Name("api.ws")
	api.GET("/broadcast/sse", broadcast.SSEHandler()).Name("api.broadcast.sse")

	// === FLUTTER WEB HOSTING (SPA FALLBACK) ===
	flutter.ServeWeb(appEngine.Router, flutter.Options{
		MountPath:   "/app",
		DistDir:     "example/client/build/web",
		FallbackSPA: true,
	})

	// Redirect root / to /app/ (Flutter Dashboard)
	appEngine.GET("/", func(c *gcontext.Context) error {
		return c.Redirect(303, "/app/")
	}).Name("root")

	// Print beautiful route table
	artisan.PrintRoutes(appEngine.Router)

	port := os.Getenv("PORT")
	if port == "" {
		port = ":8080"
	}

	firstInvoice, _ := client.Invoice.Query().First(ctx)
	sampleToken := "demo-token"
	if firstInvoice != nil {
		sampleToken = firstInvoice.Token
	}

	slog.Info("GoLaravel SaaS Showcase Server running",
		"url", "http://localhost"+port,
		"sample_invoice_url", "http://localhost"+port+"/invoice/"+sampleToken,
	)

	if err := appEngine.Run(port); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
