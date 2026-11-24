// Package payhere is the PayHere (Sri Lanka) driver: the hosted checkout
// form, the signed notify_url callback, and — with an OAuth app — the
// Retrieval and Refund APIs.
package payhere

import (
	"context"
	"crypto/md5"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golaravel/golaravel/pkg/payment"
)

const (
	liveBase    = "https://www.payhere.lk"
	sandboxBase = "https://sandbox.payhere.lk"
)

// Driver returns the PayHere driver.
func Driver() *payment.Driver {
	return &payment.Driver{
		Name:        "payhere",
		Label:       "PayHere",
		Description: "Sri Lankan cards, eZ Cash, mCash, Genie, FriMi and bank transfers through PayHere checkout.",
		Website:     "https://www.payhere.lk",
		Countries:   []string{"LK"},
		Currencies:  []string{"LKR", "USD", "GBP", "EUR", "AUD"},
		Features:    payment.Features{Refunds: true, PartialRefunds: true, StatusLookup: true},
		Fields: []payment.Field{
			{Name: "merchant_id", Label: "Merchant ID", Type: payment.FieldText, Required: true},
			{Name: "merchant_secret", Label: "Merchant secret", Type: payment.FieldSecret, Required: true,
				Help: "Settings → Domains & Credentials. The secret is issued per domain: add this site's domain first."},
			{Name: "app_id", Label: "Business app ID", Type: payment.FieldText,
				Help: "Optional. Settings → API Keys → create a business app with Payment Retrieval and Refund. Needed for refunds and status checks."},
			{Name: "app_secret", Label: "Business app secret", Type: payment.FieldSecret},
			{Name: "base_url", Label: "Base URL", Type: payment.FieldText, Help: "Leave blank. For tests and proxies."},
		},
		New: func(cfg payment.Config) (payment.Gateway, error) {
			base := liveBase
			if cfg.Test() {
				base = sandboxBase
			}
			return &Gateway{cfg: cfg, base: strings.TrimRight(payment.Pick(cfg.Get("base_url"), base), "/")}, nil
		},
	}
}

// Gateway is a configured PayHere merchant.
type Gateway struct {
	cfg  payment.Config
	base string

	mu       sync.Mutex
	token    string
	tokenExp time.Time
}

func md5Upper(s string) string {
	h := md5.Sum([]byte(s))
	return strings.ToUpper(hex.EncodeToString(h[:]))
}

// CheckoutHash is PayHere's request hash:
// UPPER(MD5(merchant_id + order_id + amount + currency + UPPER(MD5(secret)))).
func CheckoutHash(merchantID, orderID, amount, currency, secret string) string {
	return md5Upper(merchantID + orderID + amount + currency + md5Upper(secret))
}

// NotifySig is the md5sig PayHere sends to notify_url:
// UPPER(MD5(merchant_id + order_id + payhere_amount + payhere_currency + status_code + UPPER(MD5(secret)))).
func NotifySig(merchantID, orderID, amount, currency, statusCode, secret string) string {
	return md5Upper(merchantID + orderID + amount + currency + statusCode + md5Upper(secret))
}

// Checkout builds the auto-posted checkout form. PayHere's order_id is our
// payment id, so notifications find the payment directly.
func (g *Gateway) Checkout(_ context.Context, req *payment.GatewayCheckout) (*payment.Session, error) {
	amount := payment.Decimal(req.Amount, req.Currency)
	first, last := payment.FirstLast(req.Customer, "Customer")
	addr := req.Customer.Address
	if addr == nil {
		addr = &payment.Address{}
	}
	items := payment.Pick(req.Description, "Order "+req.Reference)
	f := map[string]string{
		"merchant_id": g.cfg.Get("merchant_id"),
		"return_url":  req.ReturnURL,
		"cancel_url":  req.CancelReturnURL,
		"notify_url":  req.NotifyURL,
		"order_id":    req.PaymentID,
		"items":       items,
		"currency":    req.Currency,
		"amount":      amount,
		"first_name":  first,
		"last_name":   last,
		"email":       req.Customer.Email,
		"phone":       req.Customer.Phone,
		"address":     strings.TrimSpace(addr.Line1 + " " + addr.Line2),
		"city":        addr.City,
		"country":     payment.Pick(countryName(addr.Country), "Sri Lanka"),
		"custom_1":    req.Reference,
		"hash":        CheckoutHash(g.cfg.Get("merchant_id"), req.PaymentID, amount, req.Currency, g.cfg.Get("merchant_secret")),
	}
	for i, it := range req.Items {
		n := fmt.Sprint(i + 1)
		f["item_name_"+n] = it.Name
		f["item_number_"+n] = it.SKU
		f["amount_"+n] = payment.Decimal(it.UnitAmount, req.Currency)
		f["quantity_"+n] = fmt.Sprint(it.Quantity)
	}
	return &payment.Session{Action: payment.Action{Type: payment.ActionPostForm, URL: g.base + "/pay/checkout", Fields: f}}, nil
}

func countryName(code string) string {
	switch strings.ToUpper(code) {
	case "", "LK":
		return ""
	}
	return code
}

// status maps a PayHere status_code.
func status(code string) (payment.Status, string, bool) {
	switch code {
	case "2":
		return payment.StatusPaid, "", true
	case "0":
		return payment.StatusPending, "", true
	case "-1":
		return payment.StatusCanceled, "", true
	case "-2":
		return payment.StatusFailed, "Payment failed", true
	case "-3":
		return payment.StatusRefunded, "Charged back", true
	}
	return "", "", false
}

// ParseWebhook verifies a notify_url callback (form-encoded).
func (g *Gateway) ParseWebhook(_ context.Context, _ *http.Request, body []byte) (*payment.Update, error) {
	v, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, payment.ErrInvalidSignature
	}
	if v.Get("merchant_id") != g.cfg.Get("merchant_id") {
		return nil, payment.ErrInvalidSignature
	}
	want := NotifySig(v.Get("merchant_id"), v.Get("order_id"), v.Get("payhere_amount"), v.Get("payhere_currency"),
		v.Get("status_code"), g.cfg.Get("merchant_secret"))
	if subtle.ConstantTimeCompare([]byte(strings.ToUpper(v.Get("md5sig"))), []byte(want)) != 1 {
		return nil, payment.ErrInvalidSignature
	}
	st, reason, ok := status(v.Get("status_code"))
	if !ok {
		return &payment.Update{Ignore: true}, nil
	}
	u := &payment.Update{PaymentID: v.Get("order_id"), GatewayRef: v.Get("payment_id"), Status: st,
		Event: "notify status " + v.Get("status_code"), FailureReason: payment.Pick(v.Get("status_message"), reason),
		EventID: "payhere:" + v.Get("payment_id") + ":" + v.Get("status_code")}
	if v.Get("method") != "" {
		u.Data = map[string]string{"method": v.Get("method"), "card": v.Get("card_no")}
	}
	if st == payment.StatusPending {
		u.Ignore = true
	}
	return u, nil
}

func (g *Gateway) hasAPI() bool { return g.cfg.Get("app_id") != "" && g.cfg.Get("app_secret") != "" }

// accessToken gets (and caches) an OAuth token for the merchant API.
func (g *Gateway) accessToken(ctx context.Context) (string, error) {
	if !g.hasAPI() {
		return "", fmt.Errorf("payhere: set the business app ID and secret to use the PayHere API")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.token != "" && time.Now().Before(g.tokenExp) {
		return g.token, nil
	}
	basic := base64.StdEncoding.EncodeToString([]byte(g.cfg.Get("app_id") + ":" + g.cfg.Get("app_secret")))
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	err := payment.Call{Gateway: "payhere", Client: g.cfg.Client(), Method: http.MethodPost,
		URL: g.base + "/merchant/v1/oauth/token", Header: http.Header{"Authorization": {"Basic " + basic}},
		Form: url.Values{"grant_type": {"client_credentials"}}}.Do(ctx, &out)
	if err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("payhere: no access token in the answer")
	}
	g.token = out.AccessToken
	g.tokenExp = time.Now().Add(time.Duration(max(out.ExpiresIn-60, 30)) * time.Second)
	return g.token, nil
}

// FetchStatus uses the Retrieval API (needs the business app).
func (g *Gateway) FetchStatus(ctx context.Context, p *payment.Payment) (*payment.Update, error) {
	if !g.hasAPI() {
		return &payment.Update{Ignore: true}, nil
	}
	tok, err := g.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	var out struct {
		Status int    `json:"status"`
		Msg    string `json:"msg"`
		Data   []struct {
			PaymentID int64  `json:"payment_id"`
			Status    string `json:"status"` // RECEIVED, REFUND REQUESTED, REFUNDED, CHARGEBACKED, ...
		} `json:"data"`
	}
	err = payment.Call{Gateway: "payhere", Client: g.cfg.Client(), Method: http.MethodGet,
		URL: g.base + "/merchant/v1/payment/search?order_id=" + url.QueryEscape(p.ID), Header: payment.Bearer(tok)}.Do(ctx, &out)
	if err != nil {
		return nil, err
	}
	if out.Status != 1 || len(out.Data) == 0 {
		return &payment.Update{Ignore: true}, nil // "no payments found" while the customer is still paying
	}
	d := out.Data[len(out.Data)-1]
	u := &payment.Update{GatewayRef: fmt.Sprint(d.PaymentID), Event: "retrieval " + d.Status}
	switch strings.ToUpper(d.Status) {
	case "RECEIVED", "REFUND REQUESTED":
		u.Status = payment.StatusPaid
	case "REFUNDED":
		u.Status = payment.StatusRefunded
	case "CHARGEBACKED":
		u.Status, u.FailureReason = payment.StatusRefunded, "Charged back"
	case "FAILED":
		u.Status = payment.StatusFailed
	case "CANCELED", "CANCELLED":
		u.Status = payment.StatusCanceled
	default:
		u.Ignore = true
	}
	return u, nil
}

// Refund uses the Refund API (needs the business app). PayHere refunds the
// full payment unless an amount is given.
func (g *Gateway) Refund(ctx context.Context, p *payment.Payment, amount int64, reason string) (*payment.RefundResult, error) {
	if p.GatewayRef == "" {
		return nil, fmt.Errorf("payhere: payment %s has no PayHere payment id yet", p.ID)
	}
	tok, err := g.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"payment_id": p.GatewayRef, "description": payment.Pick(reason, "Refund")}
	if amount < p.Amount-p.Refunded || p.Refunded > 0 {
		body["amount"] = payment.Decimal(amount, p.Currency)
	}
	var out struct {
		Status int    `json:"status"`
		Msg    string `json:"msg"`
		Data   any    `json:"data"`
	}
	err = payment.Call{Gateway: "payhere", Client: g.cfg.Client(), Method: http.MethodPost,
		URL: g.base + "/merchant/v1/payment/refund", Header: payment.Bearer(tok), JSON: body}.Do(ctx, &out)
	if err != nil {
		return nil, err
	}
	if out.Status != 1 {
		return nil, fmt.Errorf("payhere: refund refused: %s", payment.Pick(out.Msg, "unknown reason"))
	}
	return &payment.RefundResult{RefundRef: fmt.Sprint(out.Data)}, nil
}

// TestConnection checks the business app credentials, when set.
func (g *Gateway) TestConnection(ctx context.Context) error {
	if !g.hasAPI() {
		return fmt.Errorf("payhere: checkout needs no API call to test; add a business app to test the API credentials")
	}
	_, err := g.accessToken(ctx)
	return err
}
