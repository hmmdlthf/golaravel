// Package onepay is the OnePay (Sri Lanka) IPG driver: payment links and
// callbacks confirmed against OnePay's transaction status API.
//
// OnePay's API reference is not public. The endpoints and the hash below
// follow OnePay's v3 merchant integration as merchants receive it; both
// endpoint paths are settings so they can be matched to the documents
// OnePay sends you. Check them against a sandbox account before going live.
// Callbacks are never trusted on their own: the driver reads the
// transaction back from the status endpoint and refuses the callback if it
// cannot.
package onepay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/golaravel/golaravel/pkg/payment"
)

const defaultAPI = "https://merchant-api-live-v2.onepay.lk"

// Driver returns the OnePay driver.
func Driver() *payment.Driver {
	return &payment.Driver{
		Name:        "onepay",
		Label:       "OnePay",
		Description: "Sri Lankan Visa, Mastercard, Amex and LankaQR through the OnePay payment gateway.",
		Website:     "https://www.onepay.lk",
		Countries:   []string{"LK"},
		Currencies:  []string{"LKR", "USD"},
		Features:    payment.Features{StatusLookup: true},
		Fields: []payment.Field{
			{Name: "app_id", Label: "App ID", Type: payment.FieldText, Required: true},
			{Name: "app_token", Label: "App token", Type: payment.FieldSecret, Required: true},
			{Name: "hash_salt", Label: "Hash salt", Type: payment.FieldSecret, Required: true},
			{Name: "api_base", Label: "API base URL", Type: payment.FieldText, Default: defaultAPI,
				Help: "OnePay gives sandbox merchants their own base URL."},
			{Name: "create_path", Label: "Payment link path", Type: payment.FieldText,
				Default: "/api/ipg/gateway/request-payment-link/"},
			{Name: "status_path", Label: "Transaction status path", Type: payment.FieldText,
				Default: "/api/ipg/gateway/request-transaction/",
				Help:    "Used to confirm callbacks and return visits."},
		},
		New: func(cfg payment.Config) (payment.Gateway, error) {
			return &Gateway{cfg: cfg, api: strings.TrimRight(payment.Pick(cfg.Get("api_base"), defaultAPI), "/")}, nil
		},
	}
}

// Gateway is a configured OnePay app.
type Gateway struct {
	cfg payment.Config
	api string
}

// Hash is OnePay's request hash: SHA-256 hex of app_id + currency + amount + salt.
func Hash(appID, currency, amount, salt string) string {
	h := sha256.Sum256([]byte(appID + currency + amount + salt))
	return hex.EncodeToString(h[:])
}

func (g *Gateway) post(ctx context.Context, path, hash string, body, out any) error {
	u := g.api + "/" + strings.TrimLeft(path, "/")
	if hash != "" {
		u += "?hash=" + url.QueryEscape(hash)
	}
	return payment.Call{Gateway: "onepay", Client: g.cfg.Client(), Method: http.MethodPost, URL: u,
		Header: http.Header{"Authorization": {g.cfg.Get("app_token")}}, JSON: body}.Do(ctx, out)
}

// Checkout requests a payment link. OnePay's reference is our payment id.
func (g *Gateway) Checkout(ctx context.Context, req *payment.GatewayCheckout) (*payment.Session, error) {
	amount := payment.Decimal(req.Amount, req.Currency)
	first, last := payment.FirstLast(req.Customer, "Customer")
	hash := Hash(g.cfg.Get("app_id"), req.Currency, amount, g.cfg.Get("hash_salt"))
	body := map[string]any{
		"currency":                 req.Currency,
		"amount":                   json.Number(amount),
		"app_id":                   g.cfg.Get("app_id"),
		"hash":                     hash,
		"reference":                req.PaymentID,
		"customer_first_name":      first,
		"customer_last_name":       last,
		"customer_phone_number":    req.Customer.Phone,
		"customer_email":           req.Customer.Email,
		"transaction_redirect_url": req.ReturnURL,
		"additional_data":          req.Reference,
	}
	var out struct {
		Status  int    `json:"status"`
		Message string `json:"message"`
		Data    struct {
			IPGTransactionID string `json:"ipg_transaction_id"`
			Gateway          struct {
				RedirectURL string `json:"redirect_url"`
			} `json:"gateway"`
		} `json:"data"`
	}
	if err := g.post(ctx, g.cfg.Get("create_path"), hash, body, &out); err != nil {
		return nil, err
	}
	if out.Data.Gateway.RedirectURL == "" {
		return nil, fmt.Errorf("onepay: no payment link in the answer (%d %s)", out.Status, out.Message)
	}
	return &payment.Session{GatewayRef: out.Data.IPGTransactionID,
		Action: payment.Action{Type: payment.ActionRedirect, URL: out.Data.Gateway.RedirectURL}}, nil
}

// lookup reads a transaction from the status endpoint.
func (g *Gateway) lookup(ctx context.Context, txID string) (*payment.Update, error) {
	var out struct {
		Status int `json:"status"`
		Data   struct {
			IPGTransactionID string `json:"ipg_transaction_id"`
			Reference        string `json:"reference"`
			Status           any    `json:"status"` // 1 / "SUCCESS" / true, depending on the version
			StatusMessage    string `json:"status_message"`
		} `json:"data"`
	}
	body := map[string]any{"app_id": g.cfg.Get("app_id"), "onepay_transaction_id": txID}
	if err := g.post(ctx, g.cfg.Get("status_path"), "", body, &out); err != nil {
		return nil, err
	}
	u := &payment.Update{PaymentID: out.Data.Reference, GatewayRef: payment.Pick(out.Data.IPGTransactionID, txID),
		Event: "status " + payment.Pick(out.Data.StatusMessage, fmt.Sprint(out.Data.Status))}
	switch strings.ToUpper(fmt.Sprint(out.Data.Status)) {
	case "1", "SUCCESS", "TRUE", "PAID":
		u.Status = payment.StatusPaid
	case "0", "FAILED", "FALSE", "DECLINED":
		u.Status, u.FailureReason = payment.StatusFailed, out.Data.StatusMessage
	default:
		u.Ignore = true
	}
	return u, nil
}

// ParseWebhook reads the callback's transaction id and confirms it with
// the status endpoint; the callback body itself is not signed.
func (g *Gateway) ParseWebhook(ctx context.Context, _ *http.Request, body []byte) (*payment.Update, error) {
	var ev struct {
		TransactionID    string `json:"transaction_id"`
		IPGTransactionID string `json:"ipg_transaction_id"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		if v, err := url.ParseQuery(string(body)); err == nil {
			ev.TransactionID = v.Get("transaction_id")
		}
	}
	tx := payment.Pick(ev.TransactionID, ev.IPGTransactionID)
	if tx == "" {
		return nil, payment.ErrInvalidSignature
	}
	u, err := g.lookup(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("onepay: cannot confirm callback for %s: %w", tx, err)
	}
	u.EventID = "onepay:" + tx + ":" + string(u.Status)
	return u, nil
}

// FetchStatus confirms a payment with the status endpoint.
func (g *Gateway) FetchStatus(ctx context.Context, p *payment.Payment) (*payment.Update, error) {
	if p.GatewayRef == "" {
		return &payment.Update{Ignore: true}, nil
	}
	return g.lookup(ctx, p.GatewayRef)
}
