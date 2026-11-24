// Package tabby is the Tabby (UAE, Saudi Arabia) buy-now-pay-later driver:
// hosted checkout sessions, header-authenticated webhooks confirmed against
// the Payments API, capture and refunds.
package tabby

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golaravel/golaravel/pkg/payment"
)

// Driver returns the Tabby driver.
func Driver() *payment.Driver {
	return &payment.Driver{
		Name:        "tabby",
		Label:       "Tabby",
		Description: "Split it in 4, interest-free. Buy now, pay later in the UAE, Saudi Arabia and Kuwait.",
		Website:     "https://tabby.ai",
		Countries:   []string{"AE", "SA", "KW"},
		Currencies:  []string{"AED", "SAR", "KWD"},
		Features: payment.Features{Refunds: true, PartialRefunds: true, Capture: true, Cancel: true,
			StatusLookup: true, Installments: true},
		Fields: []payment.Field{
			{Name: "secret_key", Label: "Secret key", Type: payment.FieldSecret, Required: true, Placeholder: "sk_…"},
			{Name: "merchant_code", Label: "Merchant code", Type: payment.FieldText, Required: true,
				Help: "Given by Tabby per country/store, for example \"ae\" or your store code."},
			{Name: "region", Label: "Region", Type: payment.FieldSelect, Default: "ae",
				Options: map[string]string{"ae": "UAE / Kuwait (api.tabby.ai)", "sa": "Saudi Arabia (api.tabby.sa)"}},
			{Name: "webhook_header", Label: "Webhook header name", Type: payment.FieldText, Default: "X-Webhook-Secret",
				Help: "Register this site's webhook URL with Tabby with this header and the value below; webhooks without it are refused."},
			{Name: "webhook_secret", Label: "Webhook header value", Type: payment.FieldSecret, Required: true},
			{Name: "api_base", Label: "API base URL", Type: payment.FieldText, Help: "Leave blank. For tests and proxies."},
		},
		New: func(cfg payment.Config) (payment.Gateway, error) {
			base := "https://api.tabby.ai"
			if cfg.Get("region") == "sa" {
				base = "https://api.tabby.sa"
			}
			return &Gateway{cfg: cfg, api: strings.TrimRight(payment.Pick(cfg.Get("api_base"), base), "/")}, nil
		},
	}
}

// Gateway is a configured Tabby merchant.
type Gateway struct {
	cfg payment.Config
	api string
}

func (g *Gateway) call(ctx context.Context, method, path string, body, out any) error {
	return payment.Call{Gateway: "tabby", Client: g.cfg.Client(), Method: method, URL: g.api + path,
		Header: payment.Bearer(g.cfg.Get("secret_key")), JSON: body}.Do(ctx, out)
}

type tabbyPayment struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
	IsTest   bool   `json:"is_test"`
	Order    struct {
		ReferenceID string `json:"reference_id"`
	} `json:"order"`
	Captures []struct {
		ID     string `json:"id"`
		Amount string `json:"amount"`
	} `json:"captures"`
	Refunds []struct {
		ID     string `json:"id"`
		Amount string `json:"amount"`
	} `json:"refunds"`
	Meta map[string]any `json:"meta"`
}

// Checkout creates a checkout session. Tabby pre-scores the customer:
// a "rejected" session fails the payment straight away.
func (g *Gateway) Checkout(ctx context.Context, req *payment.GatewayCheckout) (*payment.Session, error) {
	cur := req.Currency
	addr := req.Customer.Address
	if addr == nil {
		addr = &payment.Address{}
	}
	items := []map[string]any{}
	for _, it := range req.Items {
		items = append(items, map[string]any{
			"title": it.Name, "quantity": it.Quantity, "unit_price": payment.Decimal(it.UnitAmount, cur),
			"category": payment.Pick(it.Category, "General"), "reference_id": it.SKU, "image_url": it.ImageURL,
		})
	}
	if len(items) == 0 {
		items = append(items, map[string]any{"title": payment.Pick(req.Description, "Order "+req.Reference),
			"quantity": 1, "unit_price": payment.Decimal(req.Amount, cur), "category": "General"})
	}
	lang := "en"
	if strings.HasPrefix(req.Locale, "ar") {
		lang = "ar"
	}
	body := map[string]any{
		"payment": map[string]any{
			"amount":      payment.Decimal(req.Amount, cur),
			"currency":    cur,
			"description": payment.Pick(req.Description, "Order "+req.Reference),
			"buyer": map[string]any{
				"name": payment.Pick(req.Customer.Name(), "Customer"), "email": req.Customer.Email, "phone": req.Customer.Phone,
			},
			"shipping_address": map[string]any{
				"city": addr.City, "address": strings.TrimSpace(addr.Line1 + " " + addr.Line2), "zip": addr.PostalCode,
			},
			"order": map[string]any{
				"reference_id":    req.PaymentID,
				"items":           items,
				"shipping_amount": payment.Decimal(req.ShippingAmount, cur),
				"tax_amount":      payment.Decimal(req.TaxAmount, cur),
				"discount_amount": payment.Decimal(req.DiscountAmount, cur),
			},
			"buyer_history": map[string]any{"registered_since": time.Now().UTC().Format(time.RFC3339)},
			"meta":          map[string]any{"order_id": req.Reference, "customer": req.Customer.Email},
		},
		"lang":          lang,
		"merchant_code": g.cfg.Get("merchant_code"),
		"merchant_urls": map[string]string{
			"success": req.ReturnURL, "cancel": req.CancelReturnURL, "failure": req.FailureReturnURL,
		},
	}
	var out struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		Payment struct {
			ID string `json:"id"`
		} `json:"payment"`
		Configuration struct {
			AvailableProducts map[string]json.RawMessage `json:"available_products"`
			Products          map[string]struct {
				RejectionReason string `json:"rejection_reason"`
			} `json:"products"`
		} `json:"configuration"`
	}
	if err := g.call(ctx, http.MethodPost, "/api/v2/checkout", body, &out); err != nil {
		return nil, err
	}
	s := &payment.Session{GatewayRef: out.Payment.ID, Data: map[string]string{"session_id": out.ID}}
	webURL := installmentsURL(out.Configuration.AvailableProducts["installments"])
	if strings.EqualFold(out.Status, "rejected") || webURL == "" {
		reason := "Tabby is not available for this order"
		if p, ok := out.Configuration.Products["installments"]; ok && p.RejectionReason != "" {
			reason = "Tabby declined: " + strings.ReplaceAll(p.RejectionReason, "_", " ")
		}
		s.Status, s.FailureReason, s.Action = payment.StatusFailed, reason, payment.Action{Type: payment.ActionNone}
		return s, nil
	}
	s.Action = payment.Action{Type: payment.ActionRedirect, URL: webURL}
	return s, nil
}

// installmentsURL reads web_url from either documented shape: a list of
// products or a single object.
func installmentsURL(raw json.RawMessage) string {
	var list []struct {
		WebURL string `json:"web_url"`
	}
	if json.Unmarshal(raw, &list) == nil && len(list) > 0 {
		return list[0].WebURL
	}
	var one struct {
		WebURL string `json:"web_url"`
	}
	if json.Unmarshal(raw, &one) == nil {
		return one.WebURL
	}
	return ""
}

func (g *Gateway) toUpdate(tp *tabbyPayment) *payment.Update {
	u := &payment.Update{PaymentID: tp.Order.ReferenceID, GatewayRef: tp.ID, Event: "status " + strings.ToLower(tp.Status)}
	var refunded int64
	for _, r := range tp.Refunds {
		if n, err := payment.ParseDecimal(r.Amount, tp.Currency); err == nil {
			refunded += n
		}
	}
	total, _ := payment.ParseDecimal(tp.Amount, tp.Currency)
	switch strings.ToUpper(tp.Status) {
	case "AUTHORIZED":
		u.Status = payment.StatusAuthorized
	case "CLOSED":
		// closed is captured — or voided before any capture
		switch {
		case refunded > 0 && refunded >= total:
			u.Status, u.Refunded = payment.StatusRefunded, refunded
		case refunded > 0:
			u.Status, u.Refunded = payment.StatusPartiallyRefunded, refunded
		case len(tp.Captures) > 0:
			u.Status = payment.StatusPaid
		default:
			u.Status = payment.StatusCanceled
		}
	case "REJECTED":
		u.Status, u.FailureReason = payment.StatusFailed, "Rejected by Tabby"
	case "EXPIRED":
		u.Status = payment.StatusExpired
	default:
		u.Ignore = true
	}
	return u
}

func (g *Gateway) fetch(ctx context.Context, id string) (*tabbyPayment, error) {
	var tp tabbyPayment
	if err := g.call(ctx, http.MethodGet, "/api/v2/payments/"+url.PathEscape(id), nil, &tp); err != nil {
		return nil, err
	}
	return &tp, nil
}

// ParseWebhook checks the shared header, then reads the payment back from
// the API rather than trusting the body.
func (g *Gateway) ParseWebhook(ctx context.Context, r *http.Request, body []byte) (*payment.Update, error) {
	want := g.cfg.Get("webhook_secret")
	got := r.Header.Get(payment.Pick(g.cfg.Get("webhook_header"), "X-Webhook-Secret"))
	if want == "" || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		return nil, payment.ErrInvalidSignature
	}
	var ev struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &ev); err != nil || ev.ID == "" {
		return nil, payment.ErrInvalidSignature
	}
	tp, err := g.fetch(ctx, ev.ID)
	if err != nil {
		return nil, err
	}
	return g.toUpdate(tp), nil
}

// FetchStatus reads the payment.
func (g *Gateway) FetchStatus(ctx context.Context, p *payment.Payment) (*payment.Update, error) {
	if p.GatewayRef == "" {
		return &payment.Update{Ignore: true}, nil
	}
	tp, err := g.fetch(ctx, p.GatewayRef)
	if err != nil {
		return nil, err
	}
	return g.toUpdate(tp), nil
}

// Capture captures an authorized payment.
func (g *Gateway) Capture(ctx context.Context, p *payment.Payment, amount int64) (*payment.Update, error) {
	var tp tabbyPayment
	err := g.call(ctx, http.MethodPost, "/api/v2/payments/"+url.PathEscape(p.GatewayRef)+"/captures",
		map[string]any{"amount": payment.Decimal(amount, p.Currency), "reference_id": p.ID + "-capture"}, &tp)
	if err != nil {
		return nil, err
	}
	return &payment.Update{Status: payment.StatusPaid, Event: "capture"}, nil
}

// Cancel closes an authorized payment without capturing it.
func (g *Gateway) Cancel(ctx context.Context, p *payment.Payment) (*payment.Update, error) {
	if err := g.call(ctx, http.MethodPost, "/api/v2/payments/"+url.PathEscape(p.GatewayRef)+"/close", map[string]any{}, nil); err != nil {
		return nil, err
	}
	return &payment.Update{Status: payment.StatusCanceled, Event: "close"}, nil
}

// Refund refunds a captured (closed) payment.
func (g *Gateway) Refund(ctx context.Context, p *payment.Payment, amount int64, reason string) (*payment.RefundResult, error) {
	var tp tabbyPayment
	err := g.call(ctx, http.MethodPost, "/api/v2/payments/"+url.PathEscape(p.GatewayRef)+"/refunds",
		map[string]any{"amount": payment.Decimal(amount, p.Currency), "reference_id": payment.NewID("rf"),
			"reason": payment.Pick(reason, "Refund")}, &tp)
	if err != nil {
		return nil, err
	}
	res := &payment.RefundResult{}
	for _, r := range tp.Refunds {
		if n, err := payment.ParseDecimal(r.Amount, p.Currency); err == nil {
			res.Refunded += n
		}
		res.RefundRef = r.ID
	}
	return res, nil
}
