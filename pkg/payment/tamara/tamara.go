// Package tamara is the Tamara (Saudi Arabia, UAE, Kuwait, Bahrain, Oman)
// buy-now-pay-later driver: checkout sessions, JWT-signed notifications,
// order authorisation, capture and refunds.
package tamara

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golaravel/golaravel/pkg/payment"
)

// Driver returns the Tamara driver.
func Driver() *payment.Driver {
	return &payment.Driver{
		Name:        "tamara",
		Label:       "Tamara",
		Description: "Split in 3 or 4, or pay later. Buy now, pay later across the GCC.",
		Website:     "https://tamara.co",
		Countries:   []string{"SA", "AE", "KW", "BH", "OM"},
		Currencies:  []string{"SAR", "AED", "KWD", "BHD", "OMR"},
		Features: payment.Features{Refunds: true, PartialRefunds: true, Capture: true, Cancel: true,
			StatusLookup: true, Installments: true},
		Fields: []payment.Field{
			{Name: "api_token", Label: "API token", Type: payment.FieldSecret, Required: true,
				Help: "Partners Portal → Settings → API."},
			{Name: "notification_token", Label: "Notification token", Type: payment.FieldSecret, Required: true,
				Help: "Verifies the tamaraToken JWT on notifications. Register this site's webhook URL in the Partners Portal for order events."},
			{Name: "payment_type", Label: "Payment type", Type: payment.FieldSelect, Default: "PAY_BY_INSTALMENTS",
				Options: map[string]string{"PAY_BY_INSTALMENTS": "Pay in instalments", "PAY_NOW": "Pay now"}},
			{Name: "instalments", Label: "Instalments", Type: payment.FieldText, Default: "",
				Help: "Leave blank to let the customer choose (single checkout)."},
			{Name: "country_code", Label: "Default country", Type: payment.FieldSelect, Default: "SA",
				Options: map[string]string{"SA": "Saudi Arabia", "AE": "United Arab Emirates", "KW": "Kuwait", "BH": "Bahrain", "OM": "Oman"}},
			{Name: "api_base", Label: "API base URL", Type: payment.FieldText, Help: "Leave blank. For tests and proxies."},
		},
		New: func(cfg payment.Config) (payment.Gateway, error) {
			base := "https://api.tamara.co"
			if cfg.Test() {
				base = "https://api-sandbox.tamara.co"
			}
			return &Gateway{cfg: cfg, api: strings.TrimRight(payment.Pick(cfg.Get("api_base"), base), "/")}, nil
		},
	}
}

// Gateway is a configured Tamara merchant.
type Gateway struct {
	cfg payment.Config
	api string
}

func (g *Gateway) call(ctx context.Context, method, path string, body, out any) error {
	return payment.Call{Gateway: "tamara", Client: g.cfg.Client(), Method: method, URL: g.api + path,
		Header: payment.Bearer(g.cfg.Get("api_token")), JSON: body}.Do(ctx, out)
}

func money(amount int64, cur string) map[string]any {
	return map[string]any{"amount": json.Number(payment.Decimal(amount, cur)), "currency": cur}
}

// Checkout creates a checkout session.
func (g *Gateway) Checkout(ctx context.Context, req *payment.GatewayCheckout) (*payment.Session, error) {
	cur := req.Currency
	first, last := payment.FirstLast(req.Customer, "Customer")
	addr := req.Customer.Address
	if addr == nil {
		addr = &payment.Address{}
	}
	country := strings.ToUpper(payment.Pick(addr.Country, g.cfg.Get("country_code"), "SA"))
	items := []map[string]any{}
	for _, it := range req.Items {
		items = append(items, map[string]any{
			"reference_id": payment.Pick(it.SKU, it.Name), "type": payment.Pick(it.Category, "Physical"),
			"name": it.Name, "sku": payment.Pick(it.SKU, it.Name), "quantity": it.Quantity,
			"unit_price": money(it.UnitAmount, cur), "total_amount": money(it.UnitAmount*int64(it.Quantity), cur),
			"image_url": it.ImageURL,
		})
	}
	if len(items) == 0 {
		name := payment.Pick(req.Description, "Order "+req.Reference)
		items = append(items, map[string]any{"reference_id": req.Reference, "type": "Digital", "name": name,
			"sku": req.Reference, "quantity": 1, "total_amount": money(req.Amount, cur)})
	}
	shipping := map[string]any{"first_name": first, "last_name": last, "line1": payment.Pick(addr.Line1, "-"),
		"line2": addr.Line2, "city": payment.Pick(addr.City, "-"), "region": addr.Region,
		"postal_code": addr.PostalCode, "country_code": country, "phone_number": req.Customer.Phone}
	locale := "en_US"
	if strings.HasPrefix(req.Locale, "ar") {
		locale = "ar_SA"
	}
	body := map[string]any{
		"order_reference_id": req.PaymentID,
		"order_number":       req.Reference,
		"total_amount":       money(req.Amount, cur),
		"description":        truncate(payment.Pick(req.Description, "Order "+req.Reference), 256),
		"country_code":       country,
		"payment_type":       payment.Pick(g.cfg.Get("payment_type"), "PAY_BY_INSTALMENTS"),
		"locale":             locale,
		"items":              items,
		"consumer": map[string]any{"first_name": first, "last_name": last,
			"phone_number": req.Customer.Phone, "email": req.Customer.Email},
		"shipping_address": shipping,
		"billing_address":  shipping,
		"tax_amount":       money(req.TaxAmount, cur),
		"shipping_amount":  money(req.ShippingAmount, cur),
		"discount":         map[string]any{"name": "Discount", "amount": money(req.DiscountAmount, cur)},
		"merchant_url": map[string]string{"success": req.ReturnURL, "failure": req.FailureReturnURL,
			"cancel": req.CancelReturnURL, "notification": req.NotifyURL},
		"platform":        "GoLaravel",
		"is_mobile":       false,
		"risk_assessment": map[string]any{},
	}
	if n := g.cfg.Get("instalments"); n != "" {
		body["instalments"] = json.Number(n)
	}
	var out struct {
		OrderID     string `json:"order_id"`
		CheckoutID  string `json:"checkout_id"`
		CheckoutURL string `json:"checkout_url"`
		Status      string `json:"status"`
	}
	if err := g.call(ctx, http.MethodPost, "/checkout", body, &out); err != nil {
		return nil, err
	}
	return &payment.Session{GatewayRef: out.OrderID, Data: map[string]string{"checkout_id": out.CheckoutID},
		Action: payment.Action{Type: payment.ActionRedirect, URL: out.CheckoutURL}}, nil
}

func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// fromOrderStatus maps a Tamara order status.
func fromOrderStatus(st string) (payment.Status, bool) {
	switch strings.ToLower(st) {
	case "authorised", "authorized":
		return payment.StatusAuthorized, true
	case "fully_captured":
		return payment.StatusPaid, true
	case "partially_captured":
		return payment.StatusPaid, true
	case "fully_refunded":
		return payment.StatusRefunded, true
	case "partially_refunded":
		return payment.StatusPartiallyRefunded, true
	case "canceled", "cancelled":
		return payment.StatusCanceled, true
	case "declined":
		return payment.StatusFailed, true
	case "expired":
		return payment.StatusExpired, true
	}
	return "", false // new, approved, updated: nothing to record yet
}

// authorise turns an approved order into an authorised one. Tamara needs
// this call after the approved notification, or the order expires.
func (g *Gateway) authorise(ctx context.Context, orderID string) (*payment.Update, error) {
	var out struct {
		OrderID      string `json:"order_id"`
		Status       string `json:"status"`
		AutoCaptured bool   `json:"auto_captured"`
		CaptureID    string `json:"capture_id"`
	}
	if err := g.call(ctx, http.MethodPost, "/orders/"+url.PathEscape(orderID)+"/authorise", map[string]any{}, &out); err != nil {
		return nil, err
	}
	u := &payment.Update{GatewayRef: orderID, Status: payment.StatusAuthorized, Event: "authorise"}
	if out.AutoCaptured || strings.EqualFold(out.Status, "fully_captured") {
		u.Status = payment.StatusPaid
	}
	if out.CaptureID != "" {
		u.Data = map[string]string{"capture_id": out.CaptureID}
	}
	return u, nil
}

type order struct {
	OrderID          string `json:"order_id"`
	OrderReferenceID string `json:"order_reference_id"`
	Status           string `json:"status"`
	RefundedAmount   struct {
		Amount   json.Number `json:"amount"`
		Currency string      `json:"currency"`
	} `json:"refunded_amount"`
}

func (g *Gateway) order(ctx context.Context, id string) (*order, error) {
	var o order
	if err := g.call(ctx, http.MethodGet, "/orders/"+url.PathEscape(id), nil, &o); err != nil {
		return nil, err
	}
	return &o, nil
}

func (g *Gateway) fromOrder(ctx context.Context, o *order) (*payment.Update, error) {
	if strings.EqualFold(o.Status, "approved") {
		u, err := g.authorise(ctx, o.OrderID)
		if err != nil {
			return nil, err
		}
		u.PaymentID = o.OrderReferenceID
		return u, nil
	}
	st, ok := fromOrderStatus(o.Status)
	u := &payment.Update{PaymentID: o.OrderReferenceID, GatewayRef: o.OrderID, Status: st, Ignore: !ok,
		Event: "status " + o.Status}
	if o.RefundedAmount.Amount != "" {
		if n, err := payment.ParseDecimal(o.RefundedAmount.Amount.String(), o.RefundedAmount.Currency); err == nil {
			u.Refunded = n
		}
	}
	return u, nil
}

// ParseWebhook verifies the tamaraToken JWT, then reads the order back
// from the API (and authorises it when it was just approved).
func (g *Gateway) ParseWebhook(ctx context.Context, r *http.Request, body []byte) (*payment.Update, error) {
	tok := r.URL.Query().Get("tamaraToken")
	if tok == "" {
		tok = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	if err := VerifyToken(tok, g.cfg.Get("notification_token"), time.Now()); err != nil {
		return nil, err
	}
	var ev struct {
		OrderID          string `json:"order_id"`
		OrderReferenceID string `json:"order_reference_id"`
		EventType        string `json:"event_type"`
	}
	if err := json.Unmarshal(body, &ev); err != nil || ev.OrderID == "" {
		return nil, payment.ErrInvalidSignature
	}
	o, err := g.order(ctx, ev.OrderID)
	if err != nil {
		return nil, err
	}
	u, err := g.fromOrder(ctx, o)
	if err != nil {
		return nil, err
	}
	u.Event = ev.EventType
	return u, nil
}

// FetchStatus reads the order (authorising it if it is approved).
func (g *Gateway) FetchStatus(ctx context.Context, p *payment.Payment) (*payment.Update, error) {
	if p.GatewayRef == "" {
		return &payment.Update{Ignore: true}, nil
	}
	o, err := g.order(ctx, p.GatewayRef)
	if err != nil {
		return nil, err
	}
	return g.fromOrder(ctx, o)
}

// Capture captures an authorised order.
func (g *Gateway) Capture(ctx context.Context, p *payment.Payment, amount int64) (*payment.Update, error) {
	var out struct {
		CaptureID string `json:"capture_id"`
		Status    string `json:"status"`
	}
	err := g.call(ctx, http.MethodPost, "/payments/capture", map[string]any{
		"order_id":     p.GatewayRef,
		"total_amount": money(amount, p.Currency),
		"shipping_info": map[string]any{"shipped_at": time.Now().UTC().Format(time.RFC3339),
			"shipping_company": payment.Pick(p.Metadata["shipping_company"], "N/A")},
	}, &out)
	if err != nil {
		return nil, err
	}
	return &payment.Update{Status: payment.StatusPaid, Event: "capture " + out.Status,
		Data: map[string]string{"capture_id": out.CaptureID}}, nil
}

// Cancel cancels an authorised order before capture.
func (g *Gateway) Cancel(ctx context.Context, p *payment.Payment) (*payment.Update, error) {
	err := g.call(ctx, http.MethodPost, "/orders/"+url.PathEscape(p.GatewayRef)+"/cancel",
		map[string]any{"total_amount": money(p.Amount, p.Currency)}, nil)
	if err != nil {
		return nil, err
	}
	return &payment.Update{Status: payment.StatusCanceled, Event: "cancel"}, nil
}

// Refund refunds a captured order.
func (g *Gateway) Refund(ctx context.Context, p *payment.Payment, amount int64, reason string) (*payment.RefundResult, error) {
	var out struct {
		RefundID       string `json:"refund_id"`
		RefundedAmount struct {
			Amount   json.Number `json:"amount"`
			Currency string      `json:"currency"`
		} `json:"refunded_amount"`
	}
	err := g.call(ctx, http.MethodPost, "/payments/simplified-refund/"+url.PathEscape(p.GatewayRef), map[string]any{
		"total_amount": money(amount, p.Currency), "comment": payment.Pick(reason, "Refund"),
		"merchant_refund_id": payment.NewID("rf"),
	}, &out)
	if err != nil {
		return nil, err
	}
	res := &payment.RefundResult{RefundRef: out.RefundID}
	if out.RefundedAmount.Amount != "" {
		if n, err := payment.ParseDecimal(out.RefundedAmount.Amount.String(), p.Currency); err == nil && n > p.Refunded {
			res.Refunded = n
		}
	}
	return res, nil
}

// VerifyToken checks an HS256 JWT signed with the notification token.
func VerifyToken(token, secret string, now time.Time) error {
	parts := strings.Split(token, ".")
	if secret == "" || len(parts) != 3 {
		return payment.ErrInvalidSignature
	}
	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return payment.ErrInvalidSignature
	}
	var h struct {
		Alg string `json:"alg"`
	}
	if json.Unmarshal(hb, &h) != nil || h.Alg != "HS256" {
		return payment.ErrInvalidSignature
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(parts[0] + "." + parts[1]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(sig, m.Sum(nil)) {
		return payment.ErrInvalidSignature
	}
	pb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return payment.ErrInvalidSignature
	}
	var claims struct {
		Exp json.Number `json:"exp"`
	}
	_ = json.Unmarshal(pb, &claims)
	if exp, err := claims.Exp.Int64(); err == nil && exp > 0 && now.Unix() > exp+60 {
		return payment.ErrInvalidSignature
	}
	return nil
}

// SignToken makes an HS256 JWT, for tests.
func SignToken(secret string, claims map[string]any) string {
	h := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	c, _ := json.Marshal(claims)
	p := base64.RawURLEncoding.EncodeToString(c)
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(h + "." + p))
	return h + "." + p + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
