package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/golaravel/golaravel/example/app"
	"github.com/golaravel/golaravel/example/app/handlers"
	"github.com/golaravel/golaravel/pkg/auth"
	"github.com/golaravel/golaravel/pkg/container"
	"github.com/golaravel/golaravel/pkg/flutter"
	"github.com/golaravel/golaravel/pkg/golaravel"
	"github.com/golaravel/golaravel/pkg/hash"
	"github.com/golaravel/golaravel/pkg/middleware"
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

	container.Instance(engine.Container, client)
	container.Instance(engine.Container, jwt)
	handlers.RegisterInvoiceController(engine.Container, jwt)
	ctrl := container.MustResolve[*handlers.InvoiceController](engine.Container)

	engine.GET("/invoice/:token", ctrl.PublicShow)
	api := engine.Group("/api")
	api.POST("/auth/login", ctrl.ApiLogin)
	api.GET("/invoices", ctrl.ApiList)
	api.POST("/invoices", ctrl.ApiStore)
	api.PATCH("/invoices/:id/pay", ctrl.ApiPay)

	flutter.ServeWeb(engine.Router, flutter.Options{
		MountPath:   "/app",
		DistDir:     "client/build/web",
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
}
