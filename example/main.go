package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"log/slog"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/golaravel/golaravel/example/app"
	"github.com/golaravel/golaravel/example/app/handlers"
	"github.com/golaravel/golaravel/pkg/artisan"
	"github.com/golaravel/golaravel/pkg/auth"
	"github.com/golaravel/golaravel/pkg/container"
	gcontext "github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/flutter"
	"github.com/golaravel/golaravel/pkg/golaravel"
	"github.com/golaravel/golaravel/pkg/hash"
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

	// 2. Seed Admin User
	hashedPassword, _ := hash.Make("password123")
	admin, err := client.User.Create().
		SetName("Ahmad (Admin)").
		SetEmail("admin@golaravel.dev").
		SetPassword(hashedPassword).
		SetRole("admin").
		Save(ctx)
	if err != nil {
		log.Fatalf("failed seeding admin user: %v", err)
	}

	// 3. Seed Sample Invoices with LineItems
	token1 := randomToken()
	inv1, _ := client.Invoice.Create().
		SetOwner(admin).
		SetInvoiceNumber("INV-2026-001").
		SetCustomerName("Acme Global Logistics").
		SetCustomerEmail("billing@acme.com").
		SetStatus("sent").
		SetAmount(3850.00).
		SetDueDate(time.Now().Add(10 * 24 * time.Hour)).
		SetToken(token1).
		Save(ctx)

	_, _ = client.LineItem.Create().
		SetInvoice(inv1).
		SetDescription("Dedicated GoLaravel Enterprise Cluster (128 Cores)").
		SetQuantity(1).
		SetUnitPrice(3000.00).
		SetTotal(3000.00).
		Save(ctx)

	_, _ = client.LineItem.Create().
		SetInvoice(inv1).
		SetDescription("Premium 24/7 SLA & Direct Engineering Support").
		SetQuantity(1).
		SetUnitPrice(850.00).
		SetTotal(850.00).
		Save(ctx)

	token2 := randomToken()
	inv2, _ := client.Invoice.Create().
		SetOwner(admin).
		SetInvoiceNumber("INV-2026-002").
		SetCustomerName("Starlight AI Labs").
		SetCustomerEmail("accounts@starlight.ai").
		SetStatus("paid").
		SetAmount(1200.00).
		SetDueDate(time.Now().Add(-2 * 24 * time.Hour)).
		SetToken(token2).
		Save(ctx)

	_, _ = client.LineItem.Create().
		SetInvoice(inv2).
		SetDescription("Flutter Multiplatform Integration Consulting").
		SetQuantity(8).
		SetUnitPrice(150.00).
		SetTotal(1200.00).
		Save(ctx)

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

	slog.Info("GoLaravel SaaS Showcase Server running",
		"url", "http://localhost"+port,
		"sample_invoice_url", "http://localhost"+port+"/invoice/"+token1,
	)

	if err := appEngine.Run(port); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func randomToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
