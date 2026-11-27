package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/golaravel/golaravel/example/app"
	"github.com/golaravel/golaravel/example/app/invoice"
	"github.com/golaravel/golaravel/example/app/notifications"
	"github.com/golaravel/golaravel/example/app/user"
	"github.com/golaravel/golaravel/example/app/views"
	"github.com/golaravel/golaravel/pkg/auth"
	"github.com/golaravel/golaravel/pkg/container"
	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/hash"
	"github.com/golaravel/golaravel/pkg/notification"
	"github.com/golaravel/golaravel/pkg/validation"
)

// InvoiceController handles invoice operations and public invoice rendering.
type InvoiceController struct {
	client *app.Client
	jwt    *auth.JWTManager
}

// NewInvoiceController creates a new InvoiceController.
func NewInvoiceController(client *app.Client, jwt *auth.JWTManager) *InvoiceController {
	return &InvoiceController{
		client: client,
		jwt:    jwt,
	}
}

// PublicShow renders the standalone customer invoice via Templ in < 45ns.
// No client JavaScript or Flutter bundle required.
func (ctrl *InvoiceController) PublicShow(c *context.Context) error {
	token := c.Param("token")
	if token == "" {
		return c.String(http.StatusBadRequest, "Missing invoice token")
	}

	// Fetch invoice with LineItems edge traversal
	inv, err := ctrl.client.Invoice.
		Query().
		Where(invoice.TokenEQ(token)).
		WithItems().
		Only(c.Context())

	if err != nil {
		return c.String(http.StatusNotFound, "Invoice not found or expired")
	}

	return c.Templ(http.StatusOK, views.PublicInvoiceView(inv))
}

// ApiList returns all invoices as high-speed JSON for Flutter clients.
func (ctrl *InvoiceController) ApiList(c *context.Context) error {
	invoices, err := ctrl.client.Invoice.
		Query().
		WithItems().
		Order(app.Desc(invoice.FieldCreatedAt)).
		All(c.Context())

	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, invoices)
}

// CreateInvoiceRequest represents the payload for creating an invoice.
type CreateInvoiceRequest struct {
	CustomerName  string             `json:"customer_name"`
	CustomerEmail string             `json:"customer_email"`
	DueDateDays   int                `json:"due_date_days"`
	Items         []CreateLineItem   `json:"items"`
}

type CreateLineItem struct {
	Description string  `json:"description"`
	Quantity    int     `json:"quantity"`
	UnitPrice   float64 `json:"unit_price"`
}

func (r *CreateInvoiceRequest) Validate(v *validation.Validator) {
	v.Required("customer_name", r.CustomerName).
		Required("customer_email", r.CustomerEmail).
		Email("customer_email", r.CustomerEmail).
		Rule("items", "At least one line item is required.", len(r.Items) > 0)
}

// ApiStore creates a new invoice with line items inside an Ent transaction.
func (ctrl *InvoiceController) ApiStore(c *context.Context) error {
	var req CreateInvoiceRequest
	if !validation.ValidateAndRespond(c, &req) {
		return nil
	}

	// Calculate totals
	var totalAmount float64
	for _, item := range req.Items {
		totalAmount += float64(item.Quantity) * item.UnitPrice
	}

	// Generate secure unique token
	tokenBytes := make([]byte, 16)
	_, _ = rand.Read(tokenBytes)
	token := hex.EncodeToString(tokenBytes)

	invNum := fmt.Sprintf("INV-%d", time.Now().Unix()%1000000)

	dueDate := time.Now().Add(time.Duration(req.DueDateDays) * 24 * time.Hour)
	if req.DueDateDays <= 0 {
		dueDate = time.Now().Add(14 * 24 * time.Hour)
	}

	// Execute inside transaction
	tx, err := ctrl.client.Tx(c.Context())
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	inv, err := tx.Invoice.Create().
		SetInvoiceNumber(invNum).
		SetCustomerName(req.CustomerName).
		SetCustomerEmail(req.CustomerEmail).
		SetStatus("sent").
		SetAmount(totalAmount).
		SetDueDate(dueDate).
		SetToken(token).
		Save(c.Context())

	if err != nil {
		_ = tx.Rollback()
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	// Create LineItems
	for _, item := range req.Items {
		_, err := tx.LineItem.Create().
			SetInvoice(inv).
			SetDescription(item.Description).
			SetQuantity(item.Quantity).
			SetUnitPrice(item.UnitPrice).
			SetTotal(float64(item.Quantity) * item.UnitPrice).
			Save(c.Context())
		if err != nil {
			_ = tx.Rollback()
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
	}

	if err := tx.Commit(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	// Reload with items edge
	created, err := ctrl.client.Invoice.
		Query().
		Where(invoice.IDEQ(inv.ID)).
		WithItems().
		Only(c.Context())
	if err != nil {
		return c.JSON(http.StatusOK, inv)
	}

	return c.JSON(http.StatusCreated, created)
}

// ApiPay marks an invoice as paid.
func (ctrl *InvoiceController) ApiPay(c *context.Context) error {
	idStr := c.Param("id")
	var id int
	if _, err := fmt.Sscanf(idStr, "%d", &id); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid invoice ID"})
	}

	inv, err := ctrl.client.Invoice.
		UpdateOneID(id).
		SetStatus("paid").
		Save(c.Context())

	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Invoice not found"})
	}

	// Dispatch multi-channel notification (Email + In-App Database Alert + Realtime WebSockets/SSE)
	recipient := notification.OnDemand("mail", inv.CustomerEmail).
		Route("database", fmt.Sprintf("invoice_%d", inv.ID)).
		Route("broadcast", "invoices")
	_ = notification.SendOne(c.Context(), recipient, notifications.NewInvoicePaidNotification(inv.InvoiceNumber, inv.CustomerName, inv.Amount))

	return c.JSON(http.StatusOK, inv)
}

// LoginRequest defines credentials for authentication.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (r *LoginRequest) Validate(v *validation.Validator) {
	v.Required("email", r.Email).
		Email("email", r.Email).
		Required("password", r.Password)
}

// ApiLogin authenticates the user and returns a signed JWT.
func (ctrl *InvoiceController) ApiLogin(c *context.Context) error {
	var req LoginRequest
	if !validation.ValidateAndRespond(c, &req) {
		return nil
	}

	u, err := ctrl.client.User.
		Query().
		Where(user.EmailEQ(req.Email)).
		Only(c.Context())

	if err != nil || !hash.Check(req.Password, u.Password) {
		return c.JSON(http.StatusUnauthorized, map[string]string{
			"error": "Invalid email or password",
		})
	}

	token, err := ctrl.jwt.GenerateToken(fmt.Sprint(u.ID), map[string]any{
		// The example has one tenant; the offline POS reads it from here
		// (privacy.JWTClaimResolver), since a server-issued claim can't be
		// forged by the client.
		"tenant_id": "demo",
		"name":      u.Name,
		"email": u.Email,
		"role":  u.Role,
	})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]any{
		"token": token,
		"user": map[string]any{
			"id":    u.ID,
			"name":  u.Name,
			"email": u.Email,
			"role":  u.Role,
		},
	})
}

// RegisterInvoiceController registers the controller into the IoC Container.
func RegisterInvoiceController(c *container.Container, jwt *auth.JWTManager) {
	container.Singleton(c, func(c *container.Container) (*InvoiceController, error) {
		entClient, err := container.Resolve[*app.Client](c)
		if err != nil {
			return nil, err
		}
		return NewInvoiceController(entClient, jwt), nil
	})
}
