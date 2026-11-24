// Package stripe is the Stripe driver: hosted Stripe Checkout, signed
// webhooks, status lookup, refunds, and manual capture.
package stripe

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/golaravel/golaravel/pkg/payment"
)

const defaultAPI = "https://api.stripe.com"

// Tolerance is how old a webhook signature may be.
var Tolerance = 5 * time.Minute

// Driver returns the Stripe driver.
func Driver() *payment.Driver {
	return &payment.Driver{
		Name:        "stripe",
		Label:       "Stripe",
		Description: "Cards, Apple Pay, Google Pay and local methods through Stripe Checkout.",
		Website:     "https://stripe.com",
		Features: payment.Features{Refunds: true, PartialRefunds: true, Capture: true, Cancel: true,
			StatusLookup: true},
		Fields: []payment.Field{
			{Name: "secret_key", Label: "Secret key", Type: payment.FieldSecret, Required: true,
				Placeholder: "sk_test_…", Help: "Developers → API keys. Use a restricted key with Checkout, Payment Intents and Refunds write access."},
			{Name: "webhook_secret", Label: "Webhook signing secret", Type: payment.FieldSecret, Required: true,
				Placeholder: "whsec_…", Help: "Developers → Webhooks → add the endpoint shown on this gateway; send checkout.session.*, payment_intent.* and charge.refunded."},
			{Name: "capture", Label: "Capture", Type: payment.FieldSelect, Default: "automatic",
				Options: map[string]string{"automatic": "Charge straight away", "manual": "Authorize, capture later"}},
			{Name: "payment_methods", Label: "Payment methods", Type: payment.FieldText,
				Help: "Comma-separated, for example card,link. Blank uses the methods enabled in your Stripe dashboard."},
			{Name: "api_base", Label: "API base URL", Type: payment.FieldText, Help: "Leave blank. For tests and proxies."},
		},
		New: func(cfg payment.Config) (payment.Gateway, error) {
			key := cfg.Get("secret_key")
			if cfg.Test() && strings.Contains(key, "_live_") || !cfg.Test() && strings.Contains(key, "_test_") {
				return nil, fmt.Errorf("stripe: the secret key does not match %s mode", cfg.Mode)
			}
			return &Gateway{cfg: cfg, key: key, whsec: cfg.Get("webhook_secret"),
				api: strings.TrimRight(payment.Pick(cfg.Get("api_base"), defaultAPI), "/")}, nil
		},
	}
}

// Gateway is a configured Stripe account.
type Gateway struct {
	cfg   payment.Config
	key   string
	whsec string
	api   string
}

func (g *Gateway) call(ctx context.Context, method, path string, form url.Values, out any) error {
	h := payment.Bearer(g.key)
	return payment.Call{Gateway: "stripe", Client: g.cfg.Client(), Method: method, URL: g.api + path, Header: h,
		Form: form}.Do(ctx, out)
}

type session struct {
	ID            string `json:"id"`
	URL           string `json:"url"`
	Status        string `json:"status"`         // open, complete, expired
	PaymentStatus string `json:"payment_status"` // paid, unpaid, no_payment_required
	ClientRef     string `json:"client_reference_id"`
	PaymentIntent any    `json:"payment_intent"` // id string, or an expanded object
	Metadata      map[string]string
}

func (s *session) intentID() string {
	switch v := s.PaymentIntent.(type) {
	case string:
		return v
	case map[string]any:
		id, _ := v["id"].(string)
		return id
	}
	return ""
}

type intent struct {
	ID             string            `json:"id"`
	Status         string            `json:"status"`
	AmountReceived int64             `json:"amount_received"`
	Metadata       map[string]string `json:"metadata"`
	LastError      *struct {
		Message string `json:"message"`
	} `json:"last_payment_error"`
	LatestCharge any `json:"latest_charge"`
}

// Checkout creates a Checkout Session.
func (g *Gateway) Checkout(ctx context.Context, req *payment.GatewayCheckout) (*payment.Session, error) {
	f := url.Values{
		"mode":                 {"payment"},
		"success_url":          {req.ReturnURL},
		"cancel_url":           {req.CancelReturnURL},
		"client_reference_id":  {req.PaymentID},
		"metadata[payment_id]": {req.PaymentID},
		"metadata[reference]":  {req.Reference},
		"payment_intent_data[metadata][payment_id]": {req.PaymentID},
		"payment_intent_data[metadata][reference]":  {req.Reference},
	}
	if req.Customer.Email != "" {
		f.Set("customer_email", req.Customer.Email)
	}
	if req.Locale != "" {
		f.Set("locale", req.Locale)
	}
	if g.cfg.Get("capture") == "manual" {
		f.Set("payment_intent_data[capture_method]", "manual")
	}
	for i, m := range strings.Split(g.cfg.Get("payment_methods"), ",") {
		if m = strings.TrimSpace(m); m != "" {
			f.Set(fmt.Sprintf("payment_method_types[%d]", i), m)
		}
	}
	cur := strings.ToLower(req.Currency)
	if lines := lineItems(req); lines != nil {
		for i, it := range lines {
			p := fmt.Sprintf("line_items[%d]", i)
			f.Set(p+"[quantity]", strconv.Itoa(it.Quantity))
			f.Set(p+"[price_data][currency]", cur)
			f.Set(p+"[price_data][unit_amount]", strconv.FormatInt(it.UnitAmount, 10))
			f.Set(p+"[price_data][product_data][name]", it.Name)
			if it.ImageURL != "" {
				f.Set(p+"[price_data][product_data][images][0]", it.ImageURL)
			}
		}
	} else {
		f.Set("line_items[0][quantity]", "1")
		f.Set("line_items[0][price_data][currency]", cur)
		f.Set("line_items[0][price_data][unit_amount]", strconv.FormatInt(req.Amount, 10))
		f.Set("line_items[0][price_data][product_data][name]", payment.Pick(req.Description, "Order "+req.Reference))
	}
	for k, v := range req.Metadata {
		f.Set("metadata["+k+"]", v)
	}
	var s session
	if err := g.call(ctx, http.MethodPost, "/v1/checkout/sessions", f, &s); err != nil {
		return nil, err
	}
	return &payment.Session{GatewayRef: s.ID, Action: payment.Action{Type: payment.ActionRedirect, URL: s.URL}}, nil
}

// lineItems returns the order lines when they add up to the amount;
// otherwise nil, and the checkout shows one line for the total.
func lineItems(req *payment.GatewayCheckout) []payment.Item {
	if len(req.Items) == 0 || req.DiscountAmount != 0 {
		return nil
	}
	var sum int64
	for _, it := range req.Items {
		if it.Quantity <= 0 || it.UnitAmount < 0 {
			return nil
		}
		sum += it.UnitAmount * int64(it.Quantity)
	}
	items := append([]payment.Item{}, req.Items...)
	if req.ShippingAmount > 0 {
		items = append(items, payment.Item{Name: "Shipping", Quantity: 1, UnitAmount: req.ShippingAmount})
		sum += req.ShippingAmount
	}
	if req.TaxAmount > 0 {
		items = append(items, payment.Item{Name: "Tax", Quantity: 1, UnitAmount: req.TaxAmount})
		sum += req.TaxAmount
	}
	if sum != req.Amount {
		return nil
	}
	return items
}

func (g *Gateway) fromSession(s *session) *payment.Update {
	u := &payment.Update{PaymentID: payment.Pick(s.ClientRef, s.Metadata["payment_id"]), GatewayRef: s.ID}
	if id := s.intentID(); id != "" {
		u.Data = map[string]string{"payment_intent": id}
	}
	switch {
	case s.PaymentStatus == "paid":
		u.Status = payment.StatusPaid
	case s.Status == "complete" && s.PaymentStatus == "unpaid":
		// manual capture finished the session with the funds held, or an
		// asynchronous method (bank debit) is still clearing
		if g.cfg.Get("capture") == "manual" {
			u.Status = payment.StatusAuthorized
		} else {
			u.Ignore = true
		}
	case s.Status == "expired":
		u.Status = payment.StatusExpired
	default:
		u.Ignore = true
	}
	return u
}

// FetchStatus reads the Checkout Session (and its intent for captures).
func (g *Gateway) FetchStatus(ctx context.Context, p *payment.Payment) (*payment.Update, error) {
	if p.GatewayRef == "" {
		return &payment.Update{Ignore: true}, nil
	}
	var s session
	if err := g.call(ctx, http.MethodGet, "/v1/checkout/sessions/"+url.PathEscape(p.GatewayRef)+"?expand[]=payment_intent", nil, &s); err != nil {
		return nil, err
	}
	u := g.fromSession(&s)
	if pi, ok := s.PaymentIntent.(map[string]any); ok {
		if st, _ := pi["status"].(string); st == "requires_capture" {
			u.Status, u.Ignore = payment.StatusAuthorized, false
		} else if st == "canceled" {
			u.Status, u.Ignore = payment.StatusCanceled, false
		}
	}
	return u, nil
}

// ParseWebhook verifies the Stripe-Signature header and reads the event.
func (g *Gateway) ParseWebhook(_ context.Context, r *http.Request, body []byte) (*payment.Update, error) {
	if err := VerifySignature(body, r.Header.Get("Stripe-Signature"), g.whsec, time.Now()); err != nil {
		return nil, err
	}
	var ev struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Data struct {
			Object json.RawMessage `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		return nil, payment.ErrInvalidSignature
	}
	var u *payment.Update
	switch {
	case strings.HasPrefix(ev.Type, "checkout.session."):
		var s session
		_ = json.Unmarshal(ev.Data.Object, &s)
		u = g.fromSession(&s)
		if ev.Type == "checkout.session.async_payment_failed" {
			u.Status, u.Ignore, u.FailureReason = payment.StatusFailed, false, "The payment did not clear"
		}
	case strings.HasPrefix(ev.Type, "payment_intent."):
		var pi intent
		_ = json.Unmarshal(ev.Data.Object, &pi)
		u = &payment.Update{PaymentID: pi.Metadata["payment_id"], Data: map[string]string{"payment_intent": pi.ID}}
		switch ev.Type {
		case "payment_intent.succeeded":
			u.Status = payment.StatusPaid
		case "payment_intent.amount_capturable_updated":
			u.Status = payment.StatusAuthorized
		case "payment_intent.payment_failed":
			u.Status = payment.StatusFailed
			if pi.LastError != nil {
				u.FailureReason = pi.LastError.Message
			}
		case "payment_intent.canceled":
			u.Status = payment.StatusCanceled
		default:
			u.Ignore = true
		}
	case ev.Type == "charge.refunded":
		var ch struct {
			AmountRefunded int64             `json:"amount_refunded"`
			Amount         int64             `json:"amount"`
			Metadata       map[string]string `json:"metadata"`
			PaymentIntent  string            `json:"payment_intent"`
		}
		_ = json.Unmarshal(ev.Data.Object, &ch)
		u = &payment.Update{PaymentID: ch.Metadata["payment_id"], Refunded: ch.AmountRefunded,
			Status: payment.StatusPartiallyRefunded}
		if ch.AmountRefunded >= ch.Amount {
			u.Status = payment.StatusRefunded
		}
	default:
		return &payment.Update{Ignore: true}, nil
	}
	u.EventID, u.Event = ev.ID, ev.Type
	if u.PaymentID == "" && u.GatewayRef == "" {
		u.Ignore = true // an event for a payment made outside this app
	}
	return u, nil
}

func (g *Gateway) intentID(ctx context.Context, p *payment.Payment) (string, error) {
	if id := p.Data["payment_intent"]; id != "" {
		return id, nil
	}
	var s session
	if err := g.call(ctx, http.MethodGet, "/v1/checkout/sessions/"+url.PathEscape(p.GatewayRef), nil, &s); err != nil {
		return "", err
	}
	if id := s.intentID(); id != "" {
		return id, nil
	}
	return "", fmt.Errorf("stripe: payment %s has no payment intent yet", p.ID)
}

// Refund refunds through the payment intent.
func (g *Gateway) Refund(ctx context.Context, p *payment.Payment, amount int64, reason string) (*payment.RefundResult, error) {
	pi, err := g.intentID(ctx, p)
	if err != nil {
		return nil, err
	}
	f := url.Values{"payment_intent": {pi}, "amount": {strconv.FormatInt(amount, 10)},
		"metadata[payment_id]": {p.ID}}
	if reason != "" {
		f.Set("metadata[reason]", reason)
	}
	var re struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := g.call(ctx, http.MethodPost, "/v1/refunds", f, &re); err != nil {
		return nil, err
	}
	if re.Status == "failed" || re.Status == "canceled" {
		return nil, fmt.Errorf("stripe: refund %s %s", re.ID, re.Status)
	}
	return &payment.RefundResult{RefundRef: re.ID}, nil
}

// Capture captures a manual-capture payment intent.
func (g *Gateway) Capture(ctx context.Context, p *payment.Payment, amount int64) (*payment.Update, error) {
	pi, err := g.intentID(ctx, p)
	if err != nil {
		return nil, err
	}
	var out intent
	if err := g.call(ctx, http.MethodPost, "/v1/payment_intents/"+url.PathEscape(pi)+"/capture",
		url.Values{"amount_to_capture": {strconv.FormatInt(amount, 10)}}, &out); err != nil {
		return nil, err
	}
	if out.Status != "succeeded" {
		return nil, fmt.Errorf("stripe: capture left the payment %s", out.Status)
	}
	return &payment.Update{Status: payment.StatusPaid, Event: "capture"}, nil
}

// Cancel voids a held payment intent.
func (g *Gateway) Cancel(ctx context.Context, p *payment.Payment) (*payment.Update, error) {
	pi, err := g.intentID(ctx, p)
	if err != nil {
		return nil, err
	}
	if err := g.call(ctx, http.MethodPost, "/v1/payment_intents/"+url.PathEscape(pi)+"/cancel", url.Values{}, nil); err != nil {
		return nil, err
	}
	return &payment.Update{Status: payment.StatusCanceled, Event: "cancel"}, nil
}

// TestConnection reads the account balance, which any valid key can.
func (g *Gateway) TestConnection(ctx context.Context) error {
	return g.call(ctx, http.MethodGet, "/v1/balance", nil, nil)
}

// VerifySignature checks a Stripe-Signature header ("t=..,v1=..").
func VerifySignature(body []byte, header, secret string, now time.Time) error {
	if secret == "" || header == "" {
		return payment.ErrInvalidSignature
	}
	var ts string
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch k {
		case "t":
			ts = v
		case "v1":
			sigs = append(sigs, v)
		}
	}
	t, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || len(sigs) == 0 {
		return payment.ErrInvalidSignature
	}
	if math.Abs(now.Sub(time.Unix(t, 0)).Seconds()) > Tolerance.Seconds() {
		return payment.ErrInvalidSignature
	}
	want := Sign(body, secret, t)
	for _, s := range sigs {
		if hmac.Equal([]byte(s), []byte(want)) {
			return nil
		}
	}
	return payment.ErrInvalidSignature
}

// Sign computes the v1 signature Stripe sends, for tests.
func Sign(body []byte, secret string, t int64) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(strconv.FormatInt(t, 10) + "."))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// SignatureHeader builds a Stripe-Signature header, for tests.
func SignatureHeader(body []byte, secret string, t time.Time) string {
	return fmt.Sprintf("t=%d,v1=%s", t.Unix(), Sign(body, secret, t.Unix()))
}
