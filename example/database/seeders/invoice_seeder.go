package seeders

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"github.com/golaravel/golaravel/example/app"
	"github.com/golaravel/golaravel/example/app/user"
)

// InvoiceSeeder seeds sample enterprise invoices with multi-line items.
type InvoiceSeeder struct{}

// NewInvoiceSeeder creates a new InvoiceSeeder instance.
func NewInvoiceSeeder() *InvoiceSeeder {
	return &InvoiceSeeder{}
}

// Seed populates the invoices and line_items tables.
func (s *InvoiceSeeder) Seed(ctx context.Context, client any) error {
	c, ok := client.(*app.Client)
	if !ok {
		return fmt.Errorf("invoice seeder expected *app.Client, got %T", client)
	}

	slog.Info("Seeding sample invoices & line items...")

	admin, err := c.User.Query().Where(user.EmailEQ("admin@golaravel.dev")).Only(ctx)
	if err != nil {
		return fmt.Errorf("admin user required for invoice seeding: %w", err)
	}

	// 1. Invoice 1: Acme Logistics
	token1 := generateRandomToken()
	inv1, err := c.Invoice.Create().
		SetOwner(admin).
		SetInvoiceNumber("INV-2026-001").
		SetCustomerName("Acme Global Logistics").
		SetCustomerEmail("billing@acme.com").
		SetStatus("sent").
		SetAmount(3850.00).
		SetDueDate(time.Now().Add(10 * 24 * time.Hour)).
		SetToken(token1).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed creating invoice 1: %w", err)
	}

	_, _ = c.LineItem.Create().
		SetInvoice(inv1).
		SetDescription("Dedicated GoLaravel Enterprise Cluster (128 Cores)").
		SetQuantity(1).
		SetUnitPrice(3000.00).
		SetTotal(3000.00).
		Save(ctx)

	_, _ = c.LineItem.Create().
		SetInvoice(inv1).
		SetDescription("Premium 24/7 SLA & Direct Engineering Support").
		SetQuantity(1).
		SetUnitPrice(850.00).
		SetTotal(850.00).
		Save(ctx)

	// 2. Invoice 2: Starlight AI
	token2 := generateRandomToken()
	inv2, err := c.Invoice.Create().
		SetOwner(admin).
		SetInvoiceNumber("INV-2026-002").
		SetCustomerName("Starlight AI Labs").
		SetCustomerEmail("accounts@starlight.ai").
		SetStatus("paid").
		SetAmount(1200.00).
		SetDueDate(time.Now().Add(-2 * 24 * time.Hour)).
		SetToken(token2).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed creating invoice 2: %w", err)
	}

	_, _ = c.LineItem.Create().
		SetInvoice(inv2).
		SetDescription("Flutter Multiplatform Integration Consulting").
		SetQuantity(8).
		SetUnitPrice(150.00).
		SetTotal(1200.00).
		Save(ctx)

	return nil
}

func generateRandomToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
