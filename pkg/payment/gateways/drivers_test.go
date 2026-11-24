package gateways_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golaravel/golaravel/pkg/payment"
	"github.com/golaravel/golaravel/pkg/payment/gateways"
	"github.com/golaravel/golaravel/pkg/payment/onepay"
	"github.com/golaravel/golaravel/pkg/payment/payhere"
	"github.com/golaravel/golaravel/pkg/payment/stripe"
	"github.com/golaravel/golaravel/pkg/payment/tamara"
)

// fakeAPI records requests and answers from a route table.
type fakeAPI struct {
	*httptest.Server
	mu    sync.Mutex
	calls []call
}

type call struct {
	Method, Path, Query, Auth, Body string
}

func newFakeAPI(t *testing.T, routes map[string]func(c call) (int, any)) *fakeAPI {
	f := &fakeAPI{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c := call{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), string(b)}
		f.mu.Lock()
		f.calls = append(f.calls, c)
		f.mu.Unlock()
		h, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			t.Errorf("unexpected call %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		code, out := h(c)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeAPI) last(path string) call {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].Path == path {
			return f.calls[i]
		}
	}
	return call{}
}

func manager(t *testing.T, id, driver string, mode payment.Mode, settings map[string]string) *payment.Manager {
	t.Helper()
	reg := payment.NewRegistry()
	gateways.Register(reg)
	m := payment.NewManager(payment.Options{BaseURL: "https://shop.test", Registry: reg})
	if err := m.SaveGateway(context.Background(), &payment.GatewayConfig{ID: id, Driver: driver, Enabled: true,
		Mode: mode, Settings: settings}); err != nil {
		t.Fatal(err)
	}
	return m
}

var order = payment.CheckoutRequest{
	Reference: "ORD-77", Amount: 25000, Currency: "SAR", Description: "Two mugs",
	Customer: payment.Customer{FirstName: "Noura", LastName: "Ali", Email: "noura@example.com", Phone: "+966500000001",
		Address: &payment.Address{Line1: "King Fahd Rd", City: "Riyadh", Country: "SA"}},
	Items:      []payment.Item{{SKU: "MUG", Name: "Mug", Quantity: 2, UnitAmount: 12500}},
	SuccessURL: "https://shop.test/done", CancelURL: "https://shop.test/cart",
}

func TestAllDriversInstalled(t *testing.T) {
	reg := payment.NewRegistry()
	gateways.Register(reg)
	var names []string
	for _, d := range reg.Drivers() {
		names = append(names, d.Name)
		if d.Description == "" || len(d.Fields) == 0 {
			t.Errorf("%s needs a description and fields", d.Name)
		}
	}
	if got := strings.Join(names, ","); got != "onepay,payhere,paypal,stripe,tabby,tamara,test" {
		t.Fatalf("drivers %s", got)
	}
}

func TestStripe(t *testing.T) {
	api := newFakeAPI(t, map[string]func(call) (int, any){
		"POST /v1/checkout/sessions": func(c call) (int, any) {
			return 200, map[string]any{"id": "cs_test_1", "url": "https://checkout.stripe.com/c/pay/cs_test_1"}
		},
		"POST /v1/refunds": func(c call) (int, any) { return 200, map[string]any{"id": "re_1", "status": "succeeded"} },
	})
	m := manager(t, "stripe", "stripe", payment.ModeTest, map[string]string{
		"secret_key": "sk_test_abc", "webhook_secret": "whsec_x", "api_base": api.URL})
	ctx := context.Background()
	req := order
	req.Currency = "USD"
	p, err := m.Checkout(ctx, "stripe", req)
	if err != nil {
		t.Fatal(err)
	}
	if p.GatewayRef != "cs_test_1" || p.Action.URL != "https://checkout.stripe.com/c/pay/cs_test_1" {
		t.Fatalf("session %+v", p)
	}
	sent := api.last("/v1/checkout/sessions")
	form, _ := url.ParseQuery(sent.Body)
	if sent.Auth != "Bearer sk_test_abc" || form.Get("client_reference_id") != p.ID ||
		form.Get("line_items[0][price_data][unit_amount]") != "12500" || form.Get("line_items[0][quantity]") != "2" ||
		form.Get("success_url") != "https://shop.test/payments/"+p.ID+"/return?result=success" {
		t.Fatalf("checkout form %v", form)
	}

	ev := []byte(`{"id":"evt_1","type":"checkout.session.completed","data":{"object":{"id":"cs_test_1","status":"complete","payment_status":"paid","client_reference_id":"` + p.ID + `","payment_intent":"pi_1"}}}`)
	bad := httptest.NewRequest("POST", "/", nil)
	bad.Header.Set("Stripe-Signature", stripe.SignatureHeader(ev, "whsec_wrong", time.Now()))
	if _, err := m.HandleWebhook(ctx, "stripe", bad, ev); err != payment.ErrInvalidSignature {
		t.Fatalf("wrong secret: %v", err)
	}
	old := httptest.NewRequest("POST", "/", nil)
	old.Header.Set("Stripe-Signature", stripe.SignatureHeader(ev, "whsec_x", time.Now().Add(-time.Hour)))
	if _, err := m.HandleWebhook(ctx, "stripe", old, ev); err != payment.ErrInvalidSignature {
		t.Fatalf("replayed: %v", err)
	}
	good := httptest.NewRequest("POST", "/", nil)
	good.Header.Set("Stripe-Signature", stripe.SignatureHeader(ev, "whsec_x", time.Now()))
	if p, err = m.HandleWebhook(ctx, "stripe", good, ev); err != nil || p.Status != payment.StatusPaid || p.Data["payment_intent"] != "pi_1" {
		t.Fatalf("webhook: %v %+v", err, p)
	}
	// the same event twice is applied once
	if _, err = m.HandleWebhook(ctx, "stripe", good, ev); err != nil {
		t.Fatal(err)
	}
	if logs, _ := m.Logs(ctx, p.ID); len(logs) != 2 {
		t.Fatalf("duplicate event logged: %d entries", len(logs))
	}
	if p, err = m.Refund(ctx, p.ID, 1000, "late"); err != nil || p.Status != payment.StatusPartiallyRefunded {
		t.Fatalf("refund: %v", err)
	}
	if f, _ := url.ParseQuery(api.last("/v1/refunds").Body); f.Get("payment_intent") != "pi_1" || f.Get("amount") != "1000" {
		t.Fatalf("refund form %v", f)
	}

	if err := m.SaveGateway(ctx, &payment.GatewayConfig{ID: "live", Driver: "stripe", Mode: payment.ModeLive,
		Settings: map[string]string{"secret_key": "sk_test_abc", "webhook_secret": "w"}}); err == nil {
		t.Fatal("test key accepted in live mode")
	}
}

func TestPayHere(t *testing.T) {
	m := manager(t, "payhere", "payhere", payment.ModeTest, map[string]string{"merchant_id": "1211149", "merchant_secret": "MS"})
	ctx := context.Background()
	req := order
	req.Currency = "LKR"
	p, err := m.Checkout(ctx, "payhere", req)
	if err != nil {
		t.Fatal(err)
	}
	f := p.Action.Fields
	if p.Action.Type != payment.ActionPostForm || p.Action.URL != "https://sandbox.payhere.lk/pay/checkout" ||
		f["order_id"] != p.ID || f["amount"] != "250.00" || f["notify_url"] != "https://shop.test/payments/webhooks/payhere" {
		t.Fatalf("form %+v", p.Action)
	}
	if f["hash"] != payhere.CheckoutHash("1211149", p.ID, "250.00", "LKR", "MS") {
		t.Fatal("hash mismatch")
	}
	// known vector: UPPER(MD5("1211149" + "ORD1" + "1000.00" + "LKR" + UPPER(MD5("MS"))))
	if got := payhere.CheckoutHash("1211149", "ORD1", "1000.00", "LKR", "MS"); len(got) != 32 || strings.ToUpper(got) != got {
		t.Fatalf("hash shape %s", got)
	}
	notify := func(code, secret string) url.Values {
		return url.Values{"merchant_id": {"1211149"}, "order_id": {p.ID}, "payment_id": {"320025"},
			"payhere_amount": {"250.00"}, "payhere_currency": {"LKR"}, "status_code": {code},
			"md5sig": {payhere.NotifySig("1211149", p.ID, "250.00", "LKR", code, secret)}, "method": {"VISA"}}
	}
	r := httptest.NewRequest("POST", "/", nil)
	if _, err := m.HandleWebhook(ctx, "payhere", r, []byte(notify("2", "WRONG").Encode())); err != payment.ErrInvalidSignature {
		t.Fatalf("forged notify: %v", err)
	}
	if p, err = m.HandleWebhook(ctx, "payhere", r, []byte(notify("2", "MS").Encode())); err != nil || p.Status != payment.StatusPaid || p.GatewayRef != "320025" {
		t.Fatalf("notify: %v %+v", err, p)
	}
	if _, err := m.Refund(ctx, p.ID, 0, ""); err == nil || !strings.Contains(err.Error(), "business app") {
		t.Fatalf("refund without app: %v", err)
	}
}

func TestTabby(t *testing.T) {
	status := "AUTHORIZED"
	api := newFakeAPI(t, map[string]func(call) (int, any){
		"POST /api/v2/checkout": func(c call) (int, any) {
			return 200, map[string]any{"id": "sess_1", "status": "created", "payment": map[string]any{"id": "pay_tabby_1"},
				"configuration": map[string]any{"available_products": map[string]any{
					"installments": []any{map[string]any{"web_url": "https://checkout.tabby.ai/?sessionId=sess_1"}}}}}
		},
		"GET /api/v2/payments/pay_tabby_1": func(c call) (int, any) {
			out := map[string]any{"id": "pay_tabby_1", "status": status, "amount": "250.00", "currency": "SAR",
				"order": map[string]any{"reference_id": ""}}
			if status == "CLOSED" {
				out["captures"] = []any{map[string]any{"id": "cap", "amount": "250.00"}}
			}
			return 200, out
		},
		"POST /api/v2/payments/pay_tabby_1/captures": func(c call) (int, any) { status = "CLOSED"; return 200, map[string]any{} },
	})
	m := manager(t, "tabby", "tabby", payment.ModeTest, map[string]string{
		"secret_key": "sk_test_t", "merchant_code": "sa", "webhook_secret": "hook", "api_base": api.URL})
	ctx := context.Background()
	p, err := m.Checkout(ctx, "tabby", order)
	if err != nil || p.Action.URL != "https://checkout.tabby.ai/?sessionId=sess_1" || p.GatewayRef != "pay_tabby_1" {
		t.Fatalf("checkout %v %+v", err, p)
	}
	var sent map[string]any
	_ = json.Unmarshal([]byte(api.last("/api/v2/checkout").Body), &sent)
	if sent["merchant_code"] != "sa" || sent["payment"].(map[string]any)["amount"] != "250.00" {
		t.Fatalf("sent %v", sent)
	}
	body := []byte(`{"id":"pay_tabby_1","status":"authorized"}`)
	r := httptest.NewRequest("POST", "/", nil)
	if _, err := m.HandleWebhook(ctx, "tabby", r, body); err != payment.ErrInvalidSignature {
		t.Fatalf("no header: %v", err)
	}
	r.Header.Set("X-Webhook-Secret", "hook")
	if p, err = m.HandleWebhook(ctx, "tabby", r, body); err != nil || p.Status != payment.StatusAuthorized {
		t.Fatalf("webhook %v %+v", err, p)
	}
	if p, err = m.Capture(ctx, p.ID, 0); err != nil || p.Status != payment.StatusPaid {
		t.Fatalf("capture %v", err)
	}

	// pre-scoring rejection fails the payment up front
	rej := newFakeAPI(t, map[string]func(call) (int, any){
		"POST /api/v2/checkout": func(c call) (int, any) {
			return 200, map[string]any{"id": "s2", "status": "rejected", "payment": map[string]any{"id": "p2"},
				"configuration": map[string]any{"available_products": map[string]any{},
					"products": map[string]any{"installments": map[string]any{"rejection_reason": "order_amount_too_high"}}}}
		},
	})
	m2 := manager(t, "tabby", "tabby", payment.ModeTest, map[string]string{
		"secret_key": "k", "merchant_code": "sa", "webhook_secret": "h", "api_base": rej.URL})
	p, err = m2.Checkout(ctx, "tabby", order)
	if err != nil || p.Status != payment.StatusFailed || !strings.Contains(p.FailureReason, "order amount too high") {
		t.Fatalf("rejection %v %+v", err, p)
	}
}

func TestTamara(t *testing.T) {
	var ref string
	api := newFakeAPI(t, map[string]func(call) (int, any){
		"POST /checkout": func(c call) (int, any) {
			var b map[string]any
			_ = json.Unmarshal([]byte(c.Body), &b)
			ref = b["order_reference_id"].(string)
			return 200, map[string]any{"order_id": "tm_1", "checkout_id": "ck_1", "checkout_url": "https://checkout-sandbox.tamara.co/ck_1", "status": "new"}
		},
		"GET /orders/tm_1": func(c call) (int, any) {
			return 200, map[string]any{"order_id": "tm_1", "order_reference_id": ref, "status": "approved"}
		},
		"POST /orders/tm_1/authorise": func(c call) (int, any) {
			return 200, map[string]any{"order_id": "tm_1", "status": "authorised", "auto_captured": false}
		},
	})
	m := manager(t, "tamara", "tamara", payment.ModeTest, map[string]string{
		"api_token": "tok", "notification_token": "nt", "api_base": api.URL})
	ctx := context.Background()
	p, err := m.Checkout(ctx, "tamara", order)
	if err != nil || p.GatewayRef != "tm_1" || p.Action.URL != "https://checkout-sandbox.tamara.co/ck_1" {
		t.Fatalf("checkout %v %+v", err, p)
	}
	if !strings.Contains(api.last("/checkout").Body, `"amount":250.00`) {
		t.Fatalf("amount not sent as a number: %s", api.last("/checkout").Body)
	}
	body := []byte(`{"order_id":"tm_1","order_reference_id":"` + p.ID + `","event_type":"order_approved"}`)
	forged := httptest.NewRequest("POST", "/?tamaraToken="+tamara.SignToken("other", map[string]any{}), nil)
	if _, err := m.HandleWebhook(ctx, "tamara", forged, body); err != payment.ErrInvalidSignature {
		t.Fatalf("forged token: %v", err)
	}
	expired := httptest.NewRequest("POST", "/?tamaraToken="+tamara.SignToken("nt", map[string]any{"exp": time.Now().Add(-time.Hour).Unix()}), nil)
	if _, err := m.HandleWebhook(ctx, "tamara", expired, body); err != payment.ErrInvalidSignature {
		t.Fatalf("expired token: %v", err)
	}
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("Authorization", "Bearer "+tamara.SignToken("nt", map[string]any{"exp": time.Now().Add(time.Hour).Unix()}))
	if p, err = m.HandleWebhook(ctx, "tamara", r, body); err != nil || p.Status != payment.StatusAuthorized {
		t.Fatalf("approved webhook %v %+v", err, p)
	}
	if api.last("/orders/tm_1/authorise").Method != "POST" {
		t.Fatal("order was not authorised")
	}
}

func TestOnePay(t *testing.T) {
	api := newFakeAPI(t, map[string]func(call) (int, any){
		"POST /api/ipg/gateway/request-payment-link/": func(c call) (int, any) {
			return 200, map[string]any{"status": 1000, "message": "success", "data": map[string]any{
				"ipg_transaction_id": "OP123", "gateway": map[string]any{"redirect_url": "https://gateway.onepay.lk/redirect/OP123"}}}
		},
		"POST /api/ipg/gateway/request-transaction/": func(c call) (int, any) {
			return 200, map[string]any{"status": 1000, "data": map[string]any{"ipg_transaction_id": "OP123", "status": 1, "status_message": "SUCCESS"}}
		},
	})
	m := manager(t, "onepay", "onepay", payment.ModeTest, map[string]string{
		"app_id": "APP1", "app_token": "TOKEN", "hash_salt": "SALT", "api_base": api.URL})
	ctx := context.Background()
	req := order
	req.Currency = "LKR"
	p, err := m.Checkout(ctx, "onepay", req)
	if err != nil || p.GatewayRef != "OP123" {
		t.Fatalf("checkout %v %+v", err, p)
	}
	c := api.last("/api/ipg/gateway/request-payment-link/")
	if c.Auth != "TOKEN" || c.Query != "hash="+onepay.Hash("APP1", "LKR", "250.00", "SALT") {
		t.Fatalf("auth %q query %q", c.Auth, c.Query)
	}
	if p, err = m.HandleWebhook(ctx, "onepay", httptest.NewRequest("POST", "/", nil),
		[]byte(`{"transaction_id":"OP123","status":1}`)); err != nil || p.Status != payment.StatusPaid {
		t.Fatalf("callback %v %+v", err, p)
	}
}
