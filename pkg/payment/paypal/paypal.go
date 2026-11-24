// Package paypal is the PayPal driver: Orders v2 checkout with capture on
// return (and on the approval webhook, for customers who close the tab),
// refunds, webhooks verified with PayPal's verify-webhook-signature API, and
// PayPal Billing subscriptions.
//
// Saving payment methods (PayPal's vault) is not supported yet.
package paypal

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golaravel/golaravel/pkg/payment"
)

const (
	liveAPI    = "https://api-m.paypal.com"
	sandboxAPI = "https://api-m.sandbox.paypal.com"
)

// Driver returns the PayPal driver.
func Driver() *payment.Driver {
	return &payment.Driver{
		Name:        "paypal",
		Label:       "PayPal",
		Description: "PayPal balance, cards and Pay Later through PayPal Checkout, plus PayPal subscriptions.",
		Website:     "https://developer.paypal.com",
		Features:    payment.Features{Refunds: true, PartialRefunds: true, StatusLookup: true, Subscriptions: true},
		Fields: []payment.Field{
			{Name: "client_id", Label: "Client ID", Type: payment.FieldText, Required: true,
				Help: "Developer Dashboard → Apps & Credentials (sandbox or live, matching the mode)."},
			{Name: "client_secret", Label: "Secret", Type: payment.FieldSecret, Required: true},
			{Name: "webhook_id", Label: "Webhook ID", Type: payment.FieldText, Required: true,
				Help: "Add this gateway's webhook URL to the app with Checkout order, Payment capture, Payment sale and Billing subscription events, then paste the webhook's ID."},
			{Name: "brand_name", Label: "Brand name", Type: payment.FieldText, Help: "Shown on PayPal's pages."},
			{Name: "api_base", Label: "API base URL", Type: payment.FieldText, Help: "Leave blank. For tests and proxies."},
		},
		New: func(cfg payment.Config) (payment.Gateway, error) {
			base := liveAPI
			if cfg.Test() {
				base = sandboxAPI
			}
			return &Gateway{cfg: cfg, api: strings.TrimRight(payment.Pick(cfg.Get("api_base"), base), "/")}, nil
		},
	}
}

// Gateway is a configured PayPal app.
type Gateway struct {
	cfg payment.Config
	api string

	mu       sync.Mutex
	token    string
	tokenExp time.Time
}

func (g *Gateway) accessToken(ctx context.Context) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.token != "" && time.Now().Before(g.tokenExp) {
		return g.token, nil
	}
	basic := base64.StdEncoding.EncodeToString([]byte(g.cfg.Get("client_id") + ":" + g.cfg.Get("client_secret")))
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	err := payment.Call{Gateway: "paypal", Client: g.cfg.Client(), Method: http.MethodPost, URL: g.api + "/v1/oauth2/token",
		Header: http.Header{"Authorization": {"Basic " + basic}}, Form: url.Values{"grant_type": {"client_credentials"}}}.Do(ctx, &out)
	if err != nil {
		return "", err
	}
	g.token, g.tokenExp = out.AccessToken, time.Now().Add(time.Duration(max(out.ExpiresIn-60, 30))*time.Second)
	return g.token, nil
}

func (g *Gateway) call(ctx context.Context, method, path string, body, out any, requestID ...string) error {
	tok, err := g.accessToken(ctx)
	if err != nil {
		return err
	}
	h := payment.Bearer(tok)
	if len(requestID) > 0 && requestID[0] != "" {
		h.Set("PayPal-Request-Id", requestID[0]) // idempotency
	}
	if body == nil && method == http.MethodPost {
		body = map[string]any{}
	}
	return payment.Call{Gateway: "paypal", Client: g.cfg.Client(), Method: method, URL: g.api + path, Header: h, JSON: body}.Do(ctx, out)
}

func money(amount int64, cur string) map[string]string {
	return map[string]string{"currency_code": cur, "value": payment.Decimal(amount, cur)}
}

type link struct {
	Href string `json:"href"`
	Rel  string `json:"rel"`
}

func linkTo(links []link, rels ...string) string {
	for _, r := range rels {
		for _, l := range links {
			if l.Rel == r {
				return l.Href
			}
		}
	}
	return ""
}

type order struct {
	ID            string `json:"id"`
	Status        string `json:"status"` // CREATED, APPROVED, COMPLETED, VOIDED, PAYER_ACTION_REQUIRED
	Links         []link `json:"links"`
	PurchaseUnits []struct {
		CustomID string `json:"custom_id"`
		Payments struct {
			Captures []struct {
				ID     string `json:"id"`
				Status string `json:"status"` // COMPLETED, PENDING, DECLINED, ...
			} `json:"captures"`
		} `json:"payments"`
	} `json:"purchase_units"`
}

func (o *order) update() *payment.Update {
	u := &payment.Update{GatewayRef: o.ID, Event: "order " + strings.ToLower(o.Status)}
	if len(o.PurchaseUnits) > 0 {
		u.PaymentID = o.PurchaseUnits[0].CustomID
		if caps := o.PurchaseUnits[0].Payments.Captures; len(caps) > 0 {
			u.Data = map[string]string{"capture_id": caps[0].ID}
			switch caps[0].Status {
			case "COMPLETED":
				u.Status = payment.StatusPaid
			case "DECLINED", "FAILED":
				u.Status, u.FailureReason = payment.StatusFailed, "PayPal declined the payment"
			case "REFUNDED":
				u.Status = payment.StatusRefunded
			}
			if u.Status != "" {
				return u
			}
		}
	}
	switch o.Status {
	case "VOIDED":
		u.Status = payment.StatusCanceled
	default:
		u.Ignore = true
	}
	return u
}

// Checkout creates an order; the customer approves it at PayPal.
func (g *Gateway) Checkout(ctx context.Context, req *payment.GatewayCheckout) (*payment.Session, error) {
	unit := map[string]any{
		"reference_id": req.PaymentID, "custom_id": req.PaymentID, "invoice_id": req.Reference,
		"description": payment.Pick(req.Description, "Order "+req.Reference), "amount": money(req.Amount, req.Currency),
	}
	body := map[string]any{
		"intent":         "CAPTURE",
		"purchase_units": []any{unit},
		"payment_source": map[string]any{"paypal": map[string]any{"experience_context": map[string]any{
			"return_url": req.ReturnURL, "cancel_url": req.CancelReturnURL, "user_action": "PAY_NOW",
			"brand_name": payment.Pick(g.cfg.Get("brand_name"), "Checkout"), "shipping_preference": "NO_SHIPPING",
		}}},
	}
	var o order
	if err := g.call(ctx, http.MethodPost, "/v2/checkout/orders", body, &o, req.PaymentID); err != nil {
		return nil, err
	}
	href := linkTo(o.Links, "payer-action", "approve")
	if href == "" {
		return nil, errors.New("paypal: no approval link in the order")
	}
	return &payment.Session{GatewayRef: o.ID, Action: payment.Action{Type: payment.ActionRedirect, URL: href}}, nil
}

// capture captures an approved order; an already captured order is read.
func (g *Gateway) capture(ctx context.Context, orderID string) (*payment.Update, error) {
	var o order
	err := g.call(ctx, http.MethodPost, "/v2/checkout/orders/"+url.PathEscape(orderID)+"/capture", nil, &o, "capture-"+orderID)
	var apiErr *payment.APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusUnprocessableEntity {
		if strings.Contains(apiErr.Body, "ORDER_ALREADY_CAPTURED") {
			return g.order(ctx, orderID)
		}
		if strings.Contains(apiErr.Body, "INSTRUMENT_DECLINED") {
			return &payment.Update{GatewayRef: orderID, Status: payment.StatusFailed, FailureReason: "PayPal declined the payment method"}, nil
		}
	}
	if err != nil {
		return nil, err
	}
	return o.update(), nil
}

func (g *Gateway) order(ctx context.Context, id string) (*payment.Update, error) {
	var o order
	if err := g.call(ctx, http.MethodGet, "/v2/checkout/orders/"+url.PathEscape(id), nil, &o); err != nil {
		return nil, err
	}
	if o.Status == "APPROVED" {
		return g.capture(ctx, id)
	}
	return o.update(), nil
}

// ParseReturn captures the order the customer just approved. PayPal sends
// the order id back as ?token=.
func (g *Gateway) ParseReturn(ctx context.Context, p *payment.Payment, r *http.Request) (*payment.Update, error) {
	if r.URL.Query().Get("result") != "success" || p.GatewayRef == "" {
		return &payment.Update{Ignore: true}, nil
	}
	if tok := r.URL.Query().Get("token"); tok != "" && tok != p.GatewayRef {
		return nil, payment.ErrInvalidSignature
	}
	return g.order(ctx, p.GatewayRef)
}

// FetchStatus reads the order (capturing it if it is approved).
func (g *Gateway) FetchStatus(ctx context.Context, p *payment.Payment) (*payment.Update, error) {
	if p.GatewayRef == "" {
		return &payment.Update{Ignore: true}, nil
	}
	return g.order(ctx, p.GatewayRef)
}

// Refund refunds the order's capture.
func (g *Gateway) Refund(ctx context.Context, p *payment.Payment, amount int64, reason string) (*payment.RefundResult, error) {
	capID := p.Data["capture_id"]
	if capID == "" {
		return nil, fmt.Errorf("paypal: payment %s has no capture yet", p.ID)
	}
	body := map[string]any{"amount": money(amount, p.Currency)}
	if reason != "" {
		body["note_to_payer"] = reason
	}
	var out struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := g.call(ctx, http.MethodPost, "/v2/payments/captures/"+url.PathEscape(capID)+"/refund", body, &out, payment.NewID("rf")); err != nil {
		return nil, err
	}
	if out.Status == "FAILED" || out.Status == "CANCELLED" {
		return nil, fmt.Errorf("paypal: refund %s", strings.ToLower(out.Status))
	}
	return &payment.RefundResult{RefundRef: out.ID}, nil
}

// TestConnection gets an access token.
func (g *Gateway) TestConnection(ctx context.Context) error {
	_, err := g.accessToken(ctx)
	return err
}

// verify asks PayPal whether a webhook delivery is genuine.
func (g *Gateway) verify(ctx context.Context, r *http.Request, body []byte) error {
	h := r.Header
	if g.cfg.Get("webhook_id") == "" || h.Get("PAYPAL-TRANSMISSION-SIG") == "" {
		return payment.ErrInvalidSignature
	}
	var out struct {
		VerificationStatus string `json:"verification_status"`
	}
	err := g.call(ctx, http.MethodPost, "/v1/notifications/verify-webhook-signature", map[string]any{
		"auth_algo": h.Get("PAYPAL-AUTH-ALGO"), "cert_url": h.Get("PAYPAL-CERT-URL"),
		"transmission_id": h.Get("PAYPAL-TRANSMISSION-ID"), "transmission_sig": h.Get("PAYPAL-TRANSMISSION-SIG"),
		"transmission_time": h.Get("PAYPAL-TRANSMISSION-TIME"), "webhook_id": g.cfg.Get("webhook_id"),
		"webhook_event": json.RawMessage(body),
	}, &out)
	if err != nil {
		return err
	}
	if out.VerificationStatus != "SUCCESS" {
		return payment.ErrInvalidSignature
	}
	return nil
}

// ParseWebhook verifies the delivery with PayPal, then maps the event.
func (g *Gateway) ParseWebhook(ctx context.Context, r *http.Request, body []byte) (*payment.Update, error) {
	if err := g.verify(ctx, r, body); err != nil {
		return nil, err
	}
	var ev struct {
		ID        string          `json:"id"`
		EventType string          `json:"event_type"`
		Resource  json.RawMessage `json:"resource"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		return nil, payment.ErrInvalidSignature
	}
	u, err := g.event(ctx, ev.EventType, ev.Resource)
	if err != nil || u == nil {
		return u, err
	}
	u.EventID, u.Event = ev.ID, ev.EventType
	return u, nil
}

func (g *Gateway) event(ctx context.Context, typ string, res json.RawMessage) (*payment.Update, error) {
	switch {
	case typ == "CHECKOUT.ORDER.APPROVED":
		var o order
		_ = json.Unmarshal(res, &o)
		return g.capture(ctx, o.ID) // the customer may never come back to the return URL
	case strings.HasPrefix(typ, "PAYMENT.CAPTURE."):
		var c struct {
			ID                string `json:"id"`
			CustomID          string `json:"custom_id"`
			SupplementaryData struct {
				RelatedIDs struct {
					OrderID string `json:"order_id"`
				} `json:"related_ids"`
			} `json:"supplementary_data"`
		}
		_ = json.Unmarshal(res, &c)
		u := &payment.Update{PaymentID: c.CustomID, GatewayRef: c.SupplementaryData.RelatedIDs.OrderID,
			Data: map[string]string{"capture_id": c.ID}}
		switch typ {
		case "PAYMENT.CAPTURE.COMPLETED":
			u.Status = payment.StatusPaid
		case "PAYMENT.CAPTURE.DENIED", "PAYMENT.CAPTURE.DECLINED":
			u.Status, u.FailureReason = payment.StatusFailed, "PayPal declined the payment"
		default:
			u.Ignore = true // refunds are recorded when made here; PENDING waits for COMPLETED
		}
		if u.PaymentID == "" && u.GatewayRef == "" {
			u.Ignore = true
		}
		return u, nil
	case strings.HasPrefix(typ, "BILLING.SUBSCRIPTION."):
		var s ppSubscription
		_ = json.Unmarshal(res, &s)
		if s.CustomID == "" {
			return &payment.Update{Ignore: true}, nil
		}
		su := s.update()
		if typ == "BILLING.SUBSCRIPTION.PAYMENT.FAILED" {
			su.Status, su.FailureReason = payment.SubPastDue, "PayPal could not collect the renewal"
			su.Installment = &payment.Installment{GatewayRef: "fail-" + s.ID + "-" + s.BillingInfo.LastFailedPayment.Time,
				Status: payment.StatusFailed, FailureReason: su.FailureReason}
		}
		return &payment.Update{Subscription: su}, nil
	case typ == "PAYMENT.SALE.COMPLETED":
		var sale struct {
			ID                 string `json:"id"`
			BillingAgreementID string `json:"billing_agreement_id"`
			CustomID           string `json:"custom"`
			Amount             struct {
				Total    string `json:"total"`
				Currency string `json:"currency"`
			} `json:"amount"`
		}
		_ = json.Unmarshal(res, &sale)
		if sale.BillingAgreementID == "" {
			return &payment.Update{Ignore: true}, nil
		}
		in := &payment.Installment{GatewayRef: sale.ID, Status: payment.StatusPaid}
		if n, err := payment.ParseDecimal(sale.Amount.Total, sale.Amount.Currency); err == nil {
			in.Amount = n
		}
		return &payment.Update{Subscription: &payment.SubscriptionUpdate{SubscriptionID: sale.CustomID,
			GatewayRef: sale.BillingAgreementID, Status: payment.SubActive, Installment: in}}, nil
	}
	return &payment.Update{Ignore: true}, nil
}

// ---- subscriptions ----------------------------------------------------------

type ppSubscription struct {
	ID          string `json:"id"`
	Status      string `json:"status"` // APPROVAL_PENDING, APPROVED, ACTIVE, SUSPENDED, CANCELLED, EXPIRED
	CustomID    string `json:"custom_id"`
	Links       []link `json:"links"`
	BillingInfo struct {
		NextBillingTime   string `json:"next_billing_time"`
		LastFailedPayment struct {
			Time string `json:"time"`
		} `json:"last_failed_payment"`
	} `json:"billing_info"`
}

func (s *ppSubscription) update() *payment.SubscriptionUpdate {
	su := &payment.SubscriptionUpdate{SubscriptionID: s.CustomID, GatewayRef: s.ID}
	switch s.Status {
	case "ACTIVE":
		su.Status = payment.SubActive
	case "SUSPENDED":
		su.Status = payment.SubPastDue
	case "CANCELLED":
		su.Status = payment.SubCanceled
	case "EXPIRED":
		su.Status = payment.SubCompleted
	}
	if t, err := time.Parse(time.RFC3339, s.BillingInfo.NextBillingTime); err == nil {
		su.NextBillingAt = &t
	}
	return su
}

var intervalUnit = map[payment.Interval]string{payment.Day: "DAY", payment.Week: "WEEK", payment.Month: "MONTH", payment.Year: "YEAR"}

// Subscribe creates a product and plan for the price, then the
// subscription; the customer approves it at PayPal.
func (g *Gateway) Subscribe(ctx context.Context, req *payment.GatewaySubscription) (*payment.Session, error) {
	name := payment.Pick(req.Description, "Subscription "+req.Reference)
	var product struct {
		ID string `json:"id"`
	}
	if err := g.call(ctx, http.MethodPost, "/v1/catalogs/products", map[string]any{"name": name, "type": "SERVICE"},
		&product, "product-"+req.SubscriptionID); err != nil {
		return nil, err
	}
	var plan struct {
		ID string `json:"id"`
	}
	if err := g.call(ctx, http.MethodPost, "/v1/billing/plans", map[string]any{
		"product_id": product.ID, "name": name,
		"billing_cycles": []any{map[string]any{
			"frequency":   map[string]any{"interval_unit": intervalUnit[req.Interval], "interval_count": req.IntervalCount},
			"tenure_type": "REGULAR", "sequence": 1, "total_cycles": req.Cycles,
			"pricing_scheme": map[string]any{"fixed_price": money(req.Amount, req.Currency)},
		}},
		"payment_preferences": map[string]any{"auto_bill_outstanding": true, "payment_failure_threshold": 3},
	}, &plan, "plan-"+req.SubscriptionID); err != nil {
		return nil, err
	}
	first, last := payment.FirstLast(req.Customer, "Customer")
	sub := map[string]any{
		"plan_id": plan.ID, "custom_id": req.SubscriptionID,
		"application_context": map[string]any{"return_url": req.ReturnURL, "cancel_url": req.CancelReturnURL,
			"user_action": "SUBSCRIBE_NOW", "brand_name": payment.Pick(g.cfg.Get("brand_name"), "Subscription"),
			"shipping_preference": "NO_SHIPPING"},
	}
	if req.Customer.Email != "" {
		sub["subscriber"] = map[string]any{"email_address": req.Customer.Email,
			"name": map[string]string{"given_name": first, "surname": last}}
	}
	var s ppSubscription
	if err := g.call(ctx, http.MethodPost, "/v1/billing/subscriptions", sub, &s, "sub-"+req.SubscriptionID); err != nil {
		return nil, err
	}
	href := linkTo(s.Links, "approve")
	if href == "" {
		return nil, errors.New("paypal: no approval link in the subscription")
	}
	return &payment.Session{GatewayRef: s.ID, Data: map[string]string{"plan_id": plan.ID},
		Action: payment.Action{Type: payment.ActionRedirect, URL: href}}, nil
}

func (g *Gateway) subscription(ctx context.Context, id string) (*ppSubscription, error) {
	var s ppSubscription
	if err := g.call(ctx, http.MethodGet, "/v1/billing/subscriptions/"+url.PathEscape(id), nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// ParseRecordReturn reads the subscription the customer just approved.
func (g *Gateway) ParseRecordReturn(ctx context.Context, id string, r *http.Request) (*payment.Update, error) {
	ref := r.URL.Query().Get("subscription_id")
	if payment.Kind(id) != "subscription" || ref == "" {
		return &payment.Update{Ignore: true}, nil
	}
	s, err := g.subscription(ctx, ref)
	if err != nil {
		return nil, err
	}
	if s.CustomID != id {
		return nil, payment.ErrInvalidSignature
	}
	return &payment.Update{Subscription: s.update()}, nil
}

// FetchSubscription reads the subscription.
func (g *Gateway) FetchSubscription(ctx context.Context, sub *payment.Subscription) (*payment.SubscriptionUpdate, error) {
	if sub.GatewayRef == "" {
		return nil, nil
	}
	s, err := g.subscription(ctx, sub.GatewayRef)
	if err != nil {
		return nil, err
	}
	return s.update(), nil
}

// CancelSubscription cancels it at PayPal.
func (g *Gateway) CancelSubscription(ctx context.Context, sub *payment.Subscription) (*payment.SubscriptionUpdate, error) {
	if sub.GatewayRef == "" {
		return &payment.SubscriptionUpdate{Status: payment.SubCanceled}, nil
	}
	err := g.call(ctx, http.MethodPost, "/v1/billing/subscriptions/"+url.PathEscape(sub.GatewayRef)+"/cancel",
		map[string]string{"reason": "Canceled by the merchant"}, nil)
	if err != nil {
		return nil, err
	}
	return &payment.SubscriptionUpdate{Status: payment.SubCanceled}, nil
}

// RetrySubscription collects the outstanding balance now.
func (g *Gateway) RetrySubscription(ctx context.Context, sub *payment.Subscription) (*payment.SubscriptionUpdate, error) {
	var out struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	err := g.call(ctx, http.MethodPost, "/v1/billing/subscriptions/"+url.PathEscape(sub.GatewayRef)+"/capture",
		map[string]any{"note": "Retrying the failed renewal", "capture_type": "OUTSTANDING_BALANCE",
			"amount": money(sub.Amount, sub.Currency)}, &out, payment.NewID("retry"))
	if err != nil {
		return nil, err
	}
	if out.Status != "COMPLETED" {
		return nil, nil // the sale webhook settles it
	}
	return &payment.SubscriptionUpdate{Status: payment.SubActive,
		Installment: &payment.Installment{GatewayRef: out.ID, Status: payment.StatusPaid}}, nil
}
