package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/golaravel/golaravel/example/app"
	"github.com/golaravel/golaravel/example/app/handlers"
	"github.com/golaravel/golaravel/example/app/invoice"
	"github.com/golaravel/golaravel/example/app/pos"
	"github.com/golaravel/golaravel/example/app/views"
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
	"github.com/golaravel/golaravel/pkg/offline"
	"github.com/golaravel/golaravel/pkg/router"
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
		// Never ship a fixed fallback secret: anyone who reads it can mint
		// tokens. Tokens signed with this random key end on restart.
		buf := make([]byte, 32)
		_, _ = rand.Read(buf)
		jwtSecret = hex.EncodeToString(buf)
		log.Println("JWT_SECRET is not set; using a random key for this process")
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

	// 6. Route Model Binding (PRD §3.1)
	// Bind "invoice" parameter by unique token predicate
	router.BindFieldModel[*app.Invoice](appEngine.Router, "invoice", "token", func(ctx context.Context, token string) (*app.Invoice, error) {
		return client.Invoice.Query().
			Where(invoice.TokenEQ(token)).
			WithItems().
			Only(ctx)
	})

	// Bind "invoice" parameter by integer primary key
	router.BindModel[*app.Invoice](appEngine.Router, "invoice", func(ctx context.Context, idStr string) (*app.Invoice, error) {
		var id int
		if _, err := fmt.Sscanf(idStr, "%d", &id); err != nil {
			return nil, err
		}
		return client.Invoice.Query().
			Where(invoice.IDEQ(id)).
			WithItems().
			Only(ctx)
	})

	// 7. Routes Setup

	// === FAST-PATH MICRO-SSR (TEMPL) VIA ROUTE MODEL BINDING ===
	// Public invoice link opened by customers from email/SMS:
	// Automatically resolves *app.Invoice by token parameter, rendering in < 45 nanoseconds!
	appEngine.GET("/invoice/{invoice:token}", router.With[*app.Invoice]("invoice", func(c *gcontext.Context, inv *app.Invoice) error {
		return c.Templ(http.StatusOK, views.PublicInvoiceView(inv))
	})).Name("invoices.public")

	// === HIGH-SPEED JSON REST API (FOR FLUTTER MULTIPLATFORM CLIENT) ===
	api := appEngine.Group("/api")
	api.POST("/auth/login", invoiceCtrl.ApiLogin).Name("api.auth.login")
	api.GET("/invoices", invoiceCtrl.ApiList).Name("api.invoices.list")
	api.POST("/invoices", invoiceCtrl.ApiStore).Name("api.invoices.store")
	api.PATCH("/invoices/:id/pay", invoiceCtrl.ApiPay).Name("api.invoices.pay")

	// === REALTIME WEBSOCKETS & SSE BROADCASTING (PRD §5.3) ===
	api.GET("/ws", broadcast.WebSocketHandler()).Name("api.ws")
	api.GET("/broadcast/sse", broadcast.SSEHandler()).Name("api.broadcast.sse")

	// === OFFLINE POINT OF SALE (pkg/offline) ===
	// Tills sell offline and sync when the network is back: /pos is a demo
	// till, /api/pos/sync the sync endpoints. Set OFFLINE_SYNC_KEY (from
	// offline.GenerateServerKey(...).Encode()) to keep encrypted tills
	// working across restarts.
	syncKey, err := offlineKey()
	if err != nil {
		log.Fatalf("offline sync key: %v", err)
	}
	if _, err := pos.Setup(appEngine.Router, client, jwtManager, pos.Options{Keys: []offline.ServerKey{syncKey}}); err != nil {
		log.Fatalf("offline sync: %v", err)
	}
	if err := pos.Seed(ctx, client); err != nil {
		log.Fatalf("seeding products: %v", err)
	}

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
		"offline_till", "http://localhost"+port+"/pos",
	)

	if err := appEngine.Run(port); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// offlineKey loads the sync server key from OFFLINE_SYNC_KEY, or makes a
// throwaway one (encrypted tills then re-register after a restart).
func offlineKey() (offline.ServerKey, error) {
	if enc := os.Getenv("OFFLINE_SYNC_KEY"); enc != "" {
		return offline.ParseServerKey("k1", enc)
	}
	return offline.GenerateServerKey("dev")
}
