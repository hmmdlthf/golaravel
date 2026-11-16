package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/golaravel/golaravel/example/app"
	"github.com/golaravel/golaravel/example/app/handlers"
	"github.com/golaravel/golaravel/example/app/invoice"
	"github.com/golaravel/golaravel/example/app/views"
	"github.com/golaravel/golaravel/pkg/auth"
	"github.com/golaravel/golaravel/pkg/broadcast"
	"github.com/golaravel/golaravel/pkg/container"
	gcontext "github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/flutter"
	"github.com/golaravel/golaravel/pkg/golaravel"
	"github.com/golaravel/golaravel/pkg/hash"
	"github.com/golaravel/golaravel/pkg/middleware"
	"github.com/golaravel/golaravel/pkg/notification"
	"github.com/golaravel/golaravel/pkg/router"
)

func setupTestApp(t *testing.T) (*golaravel.Engine, *app.Client, string) {
	ctx := context.Background()
	client, err := app.Open("sqlite3", "file:test_ent?mode=memory&cache=shared&_fk=1")
	if err != nil {
		t.Fatalf("failed opening connection: %v", err)
	}

	if err := client.Schema.Create(ctx); err != nil {
		t.Fatalf("failed creating schema: %v", err)
	}

	hashedPassword, _ := hash.Make("password123")
	user, err := client.User.Create().
		SetName("Admin User").
		SetEmail("admin@example.com").
		SetPassword(hashedPassword).
		SetRole("admin").
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}

	sampleToken := "test-invoice-token-1234"
	inv, err := client.Invoice.Create().
		SetOwner(user).
		SetInvoiceNumber("INV-TEST-001").
		SetCustomerName("Test Customer Corp").
		SetCustomerEmail("test@customer.com").
		SetStatus("sent").
		SetAmount(999.00).
		SetDueDate(time.Now().Add(7 * 24 * time.Hour)).
		SetToken(sampleToken).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}

	_, _ = client.LineItem.Create().
		SetInvoice(inv).
		SetDescription("Cloud Compute").
		SetQuantity(1).
		SetUnitPrice(999.00).
		SetTotal(999.00).
		Save(ctx)

	jwt := auth.NewJWTManager("test-jwt-secret-key-32-chars-long", 24*time.Hour)
	engine := golaravel.New()
	engine.Use(middleware.Recovery())

	// Attach real-time Ent mutation hook for live updates
	client.Invoice.Use(broadcast.EntMutationHook("invoices"))

	container.Instance(engine.Container, client)
	container.Instance(engine.Container, jwt)
	handlers.RegisterInvoiceController(engine.Container, jwt)
	ctrl := container.MustResolve[*handlers.InvoiceController](engine.Container)

	// Register Route Model Binder
	router.BindFieldModel[*app.Invoice](engine.Router, "invoice", "token", func(ctx context.Context, token string) (*app.Invoice, error) {
		return client.Invoice.Query().
			Where(invoice.TokenEQ(token)).
			WithItems().
			Only(ctx)
	})

	engine.GET("/invoice/{invoice:token}", router.With[*app.Invoice]("invoice", func(c *gcontext.Context, inv *app.Invoice) error {
		return c.Templ(http.StatusOK, views.PublicInvoiceView(inv))
	}))
	api := engine.Group("/api")
	api.POST("/auth/login", ctrl.ApiLogin)
	api.GET("/invoices", ctrl.ApiList)
	api.POST("/invoices", ctrl.ApiStore)
	api.PATCH("/invoices/:id/pay", ctrl.ApiPay)
	api.GET("/ws", broadcast.WebSocketHandler())
	api.GET("/broadcast/sse", broadcast.SSEHandler())

	flutter.ServeWeb(engine.Router, flutter.Options{
		MountPath:   "/app",
		DistDir:     testFlutterDist(t),
		FallbackSPA: true,
	})

	return engine, client, sampleToken
}

func TestShowcase_E2E(t *testing.T) {
	engine, client, sampleToken := setupTestApp(t)
	defer client.Close()

	// 1. Test Public Templ SSR Invoice route
	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/invoice/"+sampleToken, nil)
	engine.ServeHTTP(w1, req1)

	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200 for public invoice, got %d", w1.Code)
	}
	body1 := w1.Body.String()
	if !strings.Contains(body1, "Test Customer Corp") || !strings.Contains(body1, "INV-TEST-001") {
		t.Fatalf("expected invoice content in HTML body: %s", body1)
	}

	// 1b. Test Route Model Binding 404 on invalid token
	w1b := httptest.NewRecorder()
	req1b := httptest.NewRequest("GET", "/invoice/non-existent-token", nil)
	engine.ServeHTTP(w1b, req1b)
	if w1b.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for invalid token route model binding, got %d", w1b.Code)
	}

	// 2. Test Login API
	loginPayload := []byte(`{"email":"admin@example.com","password":"password123"}`)
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(loginPayload))
	req2.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 for login, got %d: %s", w2.Code, w2.Body.String())
	}
	var loginResp map[string]any
	_ = json.Unmarshal(w2.Body.Bytes(), &loginResp)
	token, ok := loginResp["token"].(string)
	if !ok || token == "" {
		t.Fatalf("expected JWT token in response: %v", loginResp)
	}

	// 3. Test Invoices List API
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("GET", "/api/invoices", nil)
	engine.ServeHTTP(w3, req3)

	if w3.Code != http.StatusOK {
		t.Fatalf("expected 200 for invoices list, got %d", w3.Code)
	}
	var invoices []map[string]any
	_ = json.Unmarshal(w3.Body.Bytes(), &invoices)
	if len(invoices) != 1 {
		t.Fatalf("expected 1 invoice, got %d", len(invoices))
	}

	// 4. Test Create Invoice API with validation & transaction
	// Subscribe to invoices channel to verify Ent model mutation hook dispatches realtime broadcast
	msgChan, unsub := broadcast.Subscribe("invoices")
	defer unsub()

	createPayload := []byte(`{
		"customer_name": "New Corp",
		"customer_email": "billing@newcorp.com",
		"due_date_days": 14,
		"items": [
			{"description": "Consulting Service", "quantity": 5, "unit_price": 200.00}
		]
	}`)
	w4 := httptest.NewRecorder()
	req4 := httptest.NewRequest("POST", "/api/invoices", bytes.NewReader(createPayload))
	req4.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w4, req4)

	if w4.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", w4.Code, w4.Body.String())
	}

	// Verify realtime event was broadcasted by Ent hook
	select {
	case msg := <-msgChan:
		if msg.Event != "invoice.created" {
			t.Fatalf("expected invoice.created event from Ent hook, got: %s", msg.Event)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected realtime Ent hook broadcast message on channel 'invoices'")
	}

	// 5. Test Flutter Web hosting
	w5 := httptest.NewRecorder()
	req5 := httptest.NewRequest("GET", "/app/", nil)
	engine.ServeHTTP(w5, req5)

	if w5.Code != http.StatusOK {
		t.Fatalf("expected 200 for Flutter web, got %d", w5.Code)
	}
	if !strings.Contains(w5.Body.String(), "flutter") {
		t.Fatalf("expected flutter bootstrap in web index: %s", w5.Body.String())
	}

	// 6. Test Pay Invoice API triggers multi-channel notification
	w6 := httptest.NewRecorder()
	req6 := httptest.NewRequest("PATCH", "/api/invoices/1/pay", nil)
	engine.ServeHTTP(w6, req6)
	if w6.Code != http.StatusOK {
		t.Fatalf("expected 200 for pay invoice, got %d: %s", w6.Code, w6.Body.String())
	}

	unreadNotifs, err := notification.GetDatabaseStore().GetUnread(context.Background(), "invoice_1")
	if err != nil || len(unreadNotifs) == 0 {
		t.Fatalf("expected in-app database notification for invoice_1, got %d (err: %v)", len(unreadNotifs), err)
	}
	if unreadNotifs[0].Type != "InvoicePaid" {
		t.Errorf("expected notification type InvoicePaid, got %s", unreadNotifs[0].Type)
	}
}

func TestE2E_WithGoLaravelTestingHarness(t *testing.T) {
	engine, _, sampleToken := setupTestApp(t)

	// 1. Fluent public Templ SSR view test
	engine.Test(t).
		Get("/invoice/" + sampleToken).
		AssertOK().
		AssertContentType("text/html").
		AssertSee("INV-TEST-001").
		AssertSee("Test Customer Corp").
		AssertDontSee("Internal Server Error")

	// 2. Fluent Route Model Binding 404
	engine.Test(t).
		Get("/invoice/non-existent-token").
		AssertNotFound()

	// 3. Fluent JSON authentication
	res := engine.Test(t).
		PostJSON("/api/auth/login", map[string]string{
			"email":    "admin@example.com",
			"password": "password123",
		}).
		AssertOK().
		AssertHeader("Content-Type", "application/json; charset=utf-8").
		AssertJsonPath("token", func(v any) bool { return v != "" })

	var loginData struct {
		Token string `json:"token"`
	}
	_ = res.DecodeJson(&loginData)

	// 4. Invoices list with Bearer token
	engine.Test(t).
		WithToken(loginData.Token).
		Get("/api/invoices").
		AssertOK().
		AssertJsonCount(1).
		AssertJsonPath("0.customer_name", "Test Customer Corp")

	// 5. Create invoice with validation error on empty payload
	engine.Test(t).
		WithToken(loginData.Token).
		PostJSON("/api/invoices", map[string]any{}).
		AssertUnprocessable().
		AssertJsonValidationErrors("customer_name", "customer_email")
}

// testFlutterDist returns a minimal Flutter web build in a temp dir, so the
// test does not depend on a local `flutter build web` (client/build is
// git-ignored and absent in CI).
func testFlutterDist(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	index := `<!DOCTYPE html><html><head><base href="/"><meta charset="UTF-8"></head>` +
		`<body><script src="flutter_bootstrap.js" async></script></body></html>`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flutter_bootstrap.js"), []byte("// flutter bootstrap stub\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
