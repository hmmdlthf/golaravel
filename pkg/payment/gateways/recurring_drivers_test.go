package gateways_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golaravel/golaravel/pkg/payment"
	"github.com/golaravel/golaravel/pkg/payment/payhere"
	"github.com/golaravel/golaravel/pkg/payment/stripe"
)

var subReq = payment.SubscriptionRequest{Reference: "PLAN-PRO", Amount: 150000, Currency: "LKR",
	Interval: payment.Month, Cycles: 12, Customer: payment.Customer{FirstName: "Nimal", Email: "nimal@example.com"},
	SuccessURL: "https://shop.test/welcome", CancelURL: "https://shop.test/plans"}

func TestPayHereRecurring(t *testing.T) {
	api := newFakeAPI(t, map[string]func(call) (int, any){
		"POST /merchant/v1/oauth/token": func(c call) (int, any) {
			return 200, map[string]any{"access_token": "at", "expires_in": 600}
		},
		"POST /merchant/v1/payment/charge": func(c call) (int, any) {
			var b map[string]any
			_ = json.Unmarshal([]byte(c.Body), &b)
			code := 2
			if b["amount"] == 13.0 {
				code = -2
			}
			return 200, map[string]any{"status": 1, "msg": "ok", "data": map[string]any{
				"payment_id": 320099, "status_code": code, "status_message": map[int]string{2: "Successfully completed", -2: "Card declined"}[code]}}
		},
		"POST /merchant/v1/subscription/cancel": func(c call) (int, any) { return 200, map[string]any{"status": 1, "msg": "ok"} },
	})
	m := manager(t, "payhere", "payhere", payment.ModeTest, map[string]string{"merchant_id": "1211149",
		"merchant_secret": "MS", "app_id": "app", "app_secret": "sec", "base_url": api.URL})
	ctx := context.Background()
	notify := func(v url.Values) *payment.Payment {
		t.Helper()
		v.Set("merchant_id", "1211149")
		v.Set("md5sig", payhere.NotifySig("1211149", v.Get("order_id"), v.Get("payhere_amount"), v.Get("payhere_currency"), v.Get("status_code"), "MS"))
		p, err := m.HandleWebhook(ctx, "payhere", httptest.NewRequest("POST", "/", nil), []byte(v.Encode()))
		if err != nil {
			t.Fatalf("notify %v: %v", v, err)
		}
		return p
	}

	// preapproval: hosted form, then a notify with the customer token
	pm, err := m.SavePaymentMethod(ctx, "payhere", payment.PreapprovalRequest{Customer: subReq.Customer, Currency: "LKR"})
	if err != nil || pm.Action.URL != api.URL+"/pay/preapprove" || pm.Action.Fields["order_id"] != pm.ID ||
		pm.Action.Fields["hash"] != payhere.CheckoutHash("1211149", pm.ID, "0.00", "LKR", "MS") {
		t.Fatalf("preapprove %v %+v", err, pm)
	}
	notify(url.Values{"order_id": {pm.ID}, "payment_id": {"0"}, "payhere_amount": {"0.00"}, "payhere_currency": {"LKR"},
		"status_code": {"2"}, "customer_token": {"CT-777"}, "card_no": {"************1292"}, "card_expiry": {"12/27"}, "method": {"VISA"}})
	pm, _ = m.PaymentMethod(ctx, pm.ID)
	if pm.Status != payment.MethodActive || pm.Last4 != "1292" || pm.Display() != "VISA •••• 1292 (12/27)" {
		t.Fatalf("saved method %+v", pm)
	}

	// charging API with the token
	p, err := m.Charge(ctx, pm.ID, payment.ChargeRequest{Reference: "INV-5", Amount: 50000})
	if err != nil || p.Status != payment.StatusPaid || p.GatewayRef != "320099" {
		t.Fatalf("charge %v %+v", err, p)
	}
	if b := api.last("/merchant/v1/payment/charge"); !strings.Contains(b.Body, `"customer_token":"CT-777"`) || b.Auth != "Bearer at" {
		t.Fatalf("charge sent %+v", b)
	}
	if p, _ = m.Charge(ctx, pm.ID, payment.ChargeRequest{Reference: "INV-6", Amount: 1300}); p.Status != payment.StatusFailed || p.FailureReason != "Card declined" {
		t.Fatalf("declined charge %+v", p)
	}

	// recurring checkout and its notifications
	sub, err := m.Subscribe(ctx, "payhere", subReq)
	if err != nil {
		t.Fatal(err)
	}
	if f := sub.Action.Fields; f["recurrence"] != "1 Month" || f["duration"] != "12 Month" || f["amount"] != "1500.00" || f["order_id"] != sub.ID {
		t.Fatalf("recurring form %+v", f)
	}
	notify(url.Values{"order_id": {sub.ID}, "payment_id": {"500001"}, "subscription_id": {"420001"}, "payhere_amount": {"1500.00"},
		"payhere_currency": {"LKR"}, "status_code": {"2"}, "message_type": {"AUTHORIZATION_SUCCESS"}, "item_rec_date_next": {"2027-01-24"}})
	notify(url.Values{"order_id": {sub.ID}, "payment_id": {"500002"}, "subscription_id": {"420001"}, "payhere_amount": {"1500.00"},
		"payhere_currency": {"LKR"}, "status_code": {"-2"}, "message_type": {"RECURRING_INSTALLMENT_FAILED"}})
	sub, _ = m.Subscription(ctx, sub.ID)
	if sub.Status != payment.SubPastDue || sub.GatewayRef != "420001" || sub.CyclesPaid != 1 {
		t.Fatalf("after notifies %+v", sub)
	}
	notify(url.Values{"order_id": {sub.ID}, "payment_id": {"500003"}, "subscription_id": {"420001"}, "payhere_amount": {"1500.00"},
		"payhere_currency": {"LKR"}, "status_code": {"2"}, "message_type": {"RECURRING_INSTALLMENT_SUCCESS"}})
	if sub, _ = m.Subscription(ctx, sub.ID); sub.Status != payment.SubActive || sub.CyclesPaid != 2 {
		t.Fatalf("after recovery %+v", sub)
	}
	if sub, err = m.CancelSubscription(ctx, sub.ID); err != nil || sub.Status != payment.SubCanceled {
		t.Fatalf("cancel %v", err)
	}
	if !strings.Contains(api.last("/merchant/v1/subscription/cancel").Body, `"subscription_id":420001`) {
		t.Fatal("cancel did not name the subscription")
	}
}

func stripeEvent(t *testing.T, m *payment.Manager, typ, object string) {
	t.Helper()
	body := []byte(`{"id":"evt_` + typ + time.Now().Format("150405.000000000") + `","type":"` + typ + `","data":{"object":` + object + `}}`)
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("Stripe-Signature", stripe.SignatureHeader(body, "whsec_x", time.Now()))
	if _, err := m.HandleWebhook(context.Background(), "stripe", r, body); err != nil {
		t.Fatalf("%s: %v", typ, err)
	}
}

func TestStripeRecurring(t *testing.T) {
	api := newFakeAPI(t, map[string]func(call) (int, any){
		"POST /v1/customers": func(c call) (int, any) { return 200, map[string]any{"id": "cus_1"} },
		"POST /v1/checkout/sessions": func(c call) (int, any) {
			return 200, map[string]any{"id": "cs_1", "url": "https://checkout.stripe.com/c/cs_1"}
		},
		"GET /v1/setup_intents/seti_1": func(c call) (int, any) {
			return 200, map[string]any{"status": "succeeded", "customer": "cus_1", "payment_method": map[string]any{
				"id": "pm_card_1", "type": "card", "card": map[string]any{"brand": "visa", "last4": "4242", "exp_month": 4, "exp_year": 2031}}}
		},
		"POST /v1/payment_intents": func(c call) (int, any) {
			f, _ := url.ParseQuery(c.Body)
			if f.Get("amount") == "1300" {
				return 402, map[string]any{"error": map[string]any{"message": "Your card was declined."}}
			}
			return 200, map[string]any{"id": "pi_9", "status": "succeeded"}
		},
		"DELETE /v1/subscriptions/sub_S1": func(c call) (int, any) { return 200, map[string]any{"id": "sub_S1", "status": "canceled"} },
	})
	m := manager(t, "stripe", "stripe", payment.ModeTest, map[string]string{
		"secret_key": "sk_test_abc", "webhook_secret": "whsec_x", "api_base": api.URL})
	ctx := context.Background()

	pm, err := m.SavePaymentMethod(ctx, "stripe", payment.PreapprovalRequest{Customer: subReq.Customer, Currency: "USD"})
	if err != nil || pm.Action.URL != "https://checkout.stripe.com/c/cs_1" {
		t.Fatalf("preapprove %v %+v", err, pm)
	}
	if f, _ := url.ParseQuery(api.last("/v1/checkout/sessions").Body); f.Get("mode") != "setup" || f.Get("customer") != "cus_1" {
		t.Fatalf("setup session %v", f)
	}
	stripeEvent(t, m, "checkout.session.completed", `{"id":"cs_1","mode":"setup","status":"complete","client_reference_id":"`+pm.ID+`","setup_intent":"seti_1"}`)
	pm, _ = m.PaymentMethod(ctx, pm.ID)
	if pm.Status != payment.MethodActive || pm.Display() != "Visa •••• 4242 (04/31)" {
		t.Fatalf("saved %+v", pm)
	}
	p, err := m.Charge(ctx, pm.ID, payment.ChargeRequest{Reference: "INV-1", Amount: 5000})
	if err != nil || p.Status != payment.StatusPaid {
		t.Fatalf("charge %v %+v", err, p)
	}
	if f, _ := url.ParseQuery(api.last("/v1/payment_intents").Body); f.Get("payment_method") != "pm_card_1" || f.Get("off_session") != "true" || f.Get("customer") != "cus_1" {
		t.Fatalf("charge form %v", f)
	}
	if p, _ = m.Charge(ctx, pm.ID, payment.ChargeRequest{Reference: "INV-2", Amount: 1300}); p.Status != payment.StatusFailed || p.FailureReason != "Your card was declined." {
		t.Fatalf("decline %+v", p)
	}

	req := subReq
	req.Currency, req.Cycles = "USD", 2
	sub, err := m.Subscribe(ctx, "stripe", req)
	if err != nil {
		t.Fatal(err)
	}
	if f, _ := url.ParseQuery(api.last("/v1/checkout/sessions").Body); f.Get("mode") != "subscription" ||
		f.Get("line_items[0][price_data][recurring][interval]") != "month" || f.Get("subscription_data[metadata][subscription_id]") != sub.ID {
		t.Fatalf("subscription session %v", f)
	}
	// the invoice can arrive before the checkout completion: our id is in its metadata
	stripeEvent(t, m, "invoice.paid", `{"id":"in_1","amount_paid":150000,"subscription":"sub_S1","subscription_details":{"metadata":{"subscription_id":"`+sub.ID+`"}}}`)
	stripeEvent(t, m, "checkout.session.completed", `{"id":"cs_2","mode":"subscription","status":"complete","payment_status":"paid","client_reference_id":"`+sub.ID+`","subscription":"sub_S1"}`)
	sub, _ = m.Subscription(ctx, sub.ID)
	if sub.Status != payment.SubActive || sub.GatewayRef != "sub_S1" || sub.CyclesPaid != 1 {
		t.Fatalf("after sign-up %+v", sub)
	}
	stripeEvent(t, m, "invoice.payment_failed", `{"id":"in_2","amount_due":150000,"parent":{"subscription_details":{"subscription":"sub_S1","metadata":{"subscription_id":"`+sub.ID+`"}}}}`)
	if sub, _ = m.Subscription(ctx, sub.ID); sub.Status != payment.SubPastDue {
		t.Fatalf("after failed invoice %s", sub.Status)
	}
	// the last cycle completes the subscription and stops it at Stripe
	stripeEvent(t, m, "invoice.paid", `{"id":"in_2","amount_paid":150000,"subscription":"sub_S1","subscription_details":{"metadata":{"subscription_id":"`+sub.ID+`"}}}`)
	if sub, _ = m.Subscription(ctx, sub.ID); sub.Status != payment.SubCompleted || sub.CyclesPaid != 2 {
		t.Fatalf("after last cycle %+v", sub)
	}
	if api.last("/v1/subscriptions/sub_S1").Method != "DELETE" {
		t.Fatal("completed subscription not stopped at Stripe")
	}
	if _, n, _ := m.SubscriptionPayments(ctx, sub.ID, 1, 10); n != 2 { // in_1 paid; in_2 failed then paid
		t.Fatalf("period payments %d", n)
	}
}

func TestPayPal(t *testing.T) {
	captured := false
	api := newFakeAPI(t, map[string]func(call) (int, any){
		"POST /v1/oauth2/token": func(c call) (int, any) { return 200, map[string]any{"access_token": "A21", "expires_in": 3600} },
		"POST /v2/checkout/orders": func(c call) (int, any) {
			return 201, map[string]any{"id": "ORD-1", "status": "PAYER_ACTION_REQUIRED",
				"links": []any{map[string]any{"rel": "payer-action", "href": "https://www.sandbox.paypal.com/checkoutnow?token=ORD-1"}}}
		},
		"GET /v2/checkout/orders/ORD-1": func(c call) (int, any) { return 200, map[string]any{"id": "ORD-1", "status": "APPROVED"} },
		"POST /v2/checkout/orders/ORD-1/capture": func(c call) (int, any) {
			if captured {
				return 422, map[string]any{"name": "UNPROCESSABLE_ENTITY", "details": []any{map[string]any{"issue": "ORDER_ALREADY_CAPTURED"}}}
			}
			captured = true
			return 201, map[string]any{"id": "ORD-1", "status": "COMPLETED", "purchase_units": []any{map[string]any{
				"custom_id": "", "payments": map[string]any{"captures": []any{map[string]any{"id": "CAP-1", "status": "COMPLETED"}}}}}}
		},
		"POST /v2/payments/captures/CAP-1/refund": func(c call) (int, any) { return 201, map[string]any{"id": "RF-1", "status": "COMPLETED"} },
		"POST /v1/notifications/verify-webhook-signature": func(c call) (int, any) {
			ok := "FAILURE"
			if strings.Contains(c.Body, `"transmission_sig":"good"`) {
				ok = "SUCCESS"
			}
			return 200, map[string]any{"verification_status": ok}
		},
		"POST /v1/catalogs/products": func(c call) (int, any) { return 201, map[string]any{"id": "PROD-1"} },
		"POST /v1/billing/plans":     func(c call) (int, any) { return 201, map[string]any{"id": "P-1"} },
		"POST /v1/billing/subscriptions": func(c call) (int, any) {
			return 201, map[string]any{"id": "I-SUB1", "status": "APPROVAL_PENDING",
				"links": []any{map[string]any{"rel": "approve", "href": "https://www.sandbox.paypal.com/webapps/billing/subscriptions?ba_token=BA-1"}}}
		},
		"POST /v1/billing/subscriptions/I-SUB1/cancel": func(c call) (int, any) { return 204, nil },
	})
	m := manager(t, "paypal", "paypal", payment.ModeTest, map[string]string{
		"client_id": "cid", "client_secret": "cs", "webhook_id": "WH-1", "api_base": api.URL})
	ctx := context.Background()
	req := order
	req.Currency = "USD"
	p, err := m.Checkout(ctx, "paypal", req)
	if err != nil || p.GatewayRef != "ORD-1" || !strings.Contains(p.Action.URL, "checkoutnow?token=ORD-1") {
		t.Fatalf("checkout %v %+v", err, p)
	}
	if b := api.last("/v2/checkout/orders"); !strings.Contains(b.Body, `"custom_id":"`+p.ID+`"`) || b.Auth != "Bearer A21" {
		t.Fatalf("order body %s", b.Body)
	}
	// the customer comes back: the order is captured
	r := httptest.NewRequest("GET", "/payments/"+p.ID+"/return?result=success&token=ORD-1&PayerID=X", nil)
	if p, _, err = m.HandleReturn(ctx, p.ID, "success", r); err != nil || p.Status != payment.StatusPaid || p.Data["capture_id"] != "CAP-1" {
		t.Fatalf("return %v %+v", err, p)
	}
	if p, err = m.Refund(ctx, p.ID, 5000, "late"); err != nil || p.Status != payment.StatusPartiallyRefunded {
		t.Fatalf("refund %v", err)
	}

	// webhooks are verified with PayPal
	ev := []byte(`{"id":"WH-EV-1","event_type":"PAYMENT.CAPTURE.COMPLETED","resource":{"id":"CAP-1","custom_id":"` + p.ID + `"}}`)
	bad := httptest.NewRequest("POST", "/", nil)
	bad.Header.Set("PAYPAL-TRANSMISSION-SIG", "forged")
	if _, err := m.HandleWebhook(ctx, "paypal", bad, ev); err != payment.ErrInvalidSignature {
		t.Fatalf("forged webhook: %v", err)
	}
	good := httptest.NewRequest("POST", "/", nil)
	good.Header.Set("PAYPAL-TRANSMISSION-SIG", "good")
	if _, err := m.HandleWebhook(ctx, "paypal", good, ev); err != nil {
		t.Fatalf("webhook: %v", err)
	}
	if !strings.Contains(api.last("/v1/notifications/verify-webhook-signature").Body, `"webhook_id":"WH-1"`) {
		t.Fatal("verification did not send the webhook id")
	}

	// subscriptions
	sreq := subReq
	sreq.Currency = "USD"
	sub, err := m.Subscribe(ctx, "paypal", sreq)
	if err != nil || sub.GatewayRef != "I-SUB1" || !strings.Contains(sub.Action.URL, "ba_token=BA-1") {
		t.Fatalf("subscribe %v %+v", err, sub)
	}
	if b := api.last("/v1/billing/plans"); !strings.Contains(b.Body, `"interval_unit":"MONTH"`) || !strings.Contains(b.Body, `"total_cycles":12`) {
		t.Fatalf("plan %s", b.Body)
	}
	hook := func(body string) {
		t.Helper()
		r := httptest.NewRequest("POST", "/", nil)
		r.Header.Set("PAYPAL-TRANSMISSION-SIG", "good")
		if _, err := m.HandleWebhook(ctx, "paypal", r, []byte(body)); err != nil {
			t.Fatalf("%s: %v", body, err)
		}
	}
	hook(`{"id":"WH-2","event_type":"BILLING.SUBSCRIPTION.ACTIVATED","resource":{"id":"I-SUB1","status":"ACTIVE","custom_id":"` + sub.ID + `"}}`)
	hook(`{"id":"WH-3","event_type":"PAYMENT.SALE.COMPLETED","resource":{"id":"SALE-1","billing_agreement_id":"I-SUB1","amount":{"total":"1500.00","currency":"USD"}}}`)
	sub, _ = m.Subscription(ctx, sub.ID)
	if sub.Status != payment.SubActive || sub.CyclesPaid != 1 {
		t.Fatalf("after activation and sale %+v", sub)
	}
	if sub, err = m.CancelSubscription(ctx, sub.ID); err != nil || sub.Status != payment.SubCanceled {
		t.Fatalf("cancel %v", err)
	}
}
