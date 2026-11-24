package payment_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golaravel/golaravel/pkg/payment"
)

// saveCard runs the test gateway's preapproval page and returns the method.
func (h *harness) saveCard(outcome string) *payment.PaymentMethod {
	h.t.Helper()
	ctx := context.Background()
	pm, err := h.m.SavePaymentMethod(ctx, "test", payment.PreapprovalRequest{CustomerRef: "cust-1",
		Customer: payment.Customer{FirstName: "Ama", Email: "ama@example.com"}, Currency: "LKR",
		SuccessURL: "https://shop.test/account", CancelURL: "https://shop.test/account?err=1"})
	if err != nil {
		h.t.Fatal(err)
	}
	if pm.Status != payment.MethodPending || pm.Action == nil {
		h.t.Fatalf("new method %+v", pm)
	}
	r := h.get(h.m.PayURL(&payment.Payment{ID: pm.ID}))
	page := h.get(r.Header.Get("Location"))
	html := body(page)
	if !strings.Contains(html, "Save Visa") {
		h.t.Fatalf("preapproval page: %d %s", page.StatusCode, html)
	}
	back := h.get(linkFor(h.t, html, outcome))
	if outcome == "pay" && !strings.HasPrefix(back.Header.Get("Location"), "https://shop.test/account?method="+pm.ID) {
		h.t.Fatalf("returned to %s", back.Header.Get("Location"))
	}
	got, _ := h.m.PaymentMethod(ctx, pm.ID)
	return got
}

func TestPreapprovalAndCharge(t *testing.T) {
	for name, mk := range storeCases(t) {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, mk())
			h.saveTestGateway("capture", false)
			ctx := context.Background()
			pm := h.saveCard("pay")
			if pm.Status != payment.MethodActive || pm.Display() != "Visa •••• 4242 (12/30)" || pm.Token != "" {
				t.Fatalf("saved method %+v", pm)
			}
			raw, _ := h.m.Store().Method(ctx, pm.ID)
			if !strings.HasPrefix(raw.Token, "enc:") {
				t.Fatalf("token stored as %q", raw.Token)
			}
			p, err := h.m.Charge(ctx, pm.ID, payment.ChargeRequest{Reference: "INV-1", Amount: 150000})
			if err != nil || p.Status != payment.StatusPaid || p.Currency != "LKR" || p.PaymentMethodID != pm.ID {
				t.Fatalf("charge: %v %+v", err, p)
			}
			p, err = h.m.Charge(ctx, pm.ID, payment.ChargeRequest{Reference: "INV-2", Amount: 113})
			if err != nil || p.Status != payment.StatusFailed {
				t.Fatalf("declined charge: %v %+v", err, p)
			}
			if _, err := h.m.RevokePaymentMethod(ctx, pm.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := h.m.Charge(ctx, pm.ID, payment.ChargeRequest{Reference: "INV-3", Amount: 100}); err == nil {
				t.Fatal("charged a revoked method")
			}
			list, n, _ := h.m.PaymentMethods(ctx, payment.MethodQuery{CustomerRef: "cust-1"})
			if n != 1 || list[0].Status != payment.MethodRevoked || list[0].Token != "" {
				t.Fatalf("methods %d %+v", n, list)
			}
		})
	}
	h := newHarness(t, payment.NewMemoryStore())
	h.saveTestGateway("capture", false)
	if pm := h.saveCard("decline"); pm.Status != payment.MethodFailed {
		t.Fatalf("declined preapproval: %s", pm.Status)
	}
}

// due moves a subscription's next bill into the past.
func due(t *testing.T, m *payment.Manager, id string) {
	t.Helper()
	sub, _ := m.Store().Subscription(context.Background(), id)
	past := time.Now().Add(-time.Minute)
	sub.NextBillingAt = &past
	if err := m.Store().UpdateSubscription(context.Background(), sub); err != nil {
		t.Fatal(err)
	}
}

func TestManagerBilledSubscription(t *testing.T) {
	for name, mk := range storeCases(t) {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, mk())
			h.saveTestGateway("capture", false)
			ctx := context.Background()
			pm := h.saveCard("pay")
			var renewals int
			h.m.Events().Listen(payment.EventSubscriptionRenewed, func(any) error { renewals++; return nil })

			sub, err := h.m.Subscribe(ctx, "", payment.SubscriptionRequest{Reference: "PLAN-PRO", Amount: 250000,
				Interval: payment.Month, Cycles: 3, PaymentMethodID: pm.ID})
			if err != nil {
				t.Fatal(err)
			}
			if sub.Status != payment.SubActive || sub.CyclesPaid != 1 || sub.Billing != payment.BillingManager ||
				sub.NextBillingAt == nil || sub.NextBillingAt.Before(time.Now().AddDate(0, 0, 27)) {
				t.Fatalf("after first period %+v", sub)
			}
			// nothing due yet
			if n, _ := h.m.RunDueSubscriptions(ctx); n != 0 {
				t.Fatalf("billed early: %d", n)
			}
			due(t, h.m, sub.ID)
			if n, _ := h.m.RunDueSubscriptions(ctx); n != 1 {
				t.Fatalf("due run billed %d", n)
			}
			due(t, h.m, sub.ID)
			_, _ = h.m.RunDueSubscriptions(ctx)
			sub, _ = h.m.Subscription(ctx, sub.ID)
			if sub.Status != payment.SubCompleted || sub.CyclesPaid != 3 || sub.NextBillingAt != nil {
				t.Fatalf("after 3 cycles %+v", sub)
			}
			pays, n, _ := h.m.SubscriptionPayments(ctx, sub.ID, 1, 10)
			if n != 3 || pays[0].SubscriptionID != sub.ID {
				t.Fatalf("renewal payments %d", n)
			}
			if renewals != 3 {
				t.Fatalf("renewed events %d", renewals)
			}
		})
	}
}

func TestManagerBilledDunning(t *testing.T) {
	h := newHarness(t, payment.NewMemoryStore())
	h.saveTestGateway("capture", false)
	ctx := context.Background()
	pm := h.saveCard("pay")
	// .13 amounts are declined by the test gateway
	sub, err := h.m.Subscribe(ctx, "", payment.SubscriptionRequest{Reference: "PLAN-X", Amount: 1013, PaymentMethodID: pm.ID})
	if err != nil {
		t.Fatal(err)
	}
	if sub.Status != payment.SubPastDue || sub.FailedAttempts != 1 || sub.NextBillingAt.Before(time.Now().Add(23*time.Hour)) {
		t.Fatalf("after a decline %+v", sub)
	}
	for i := 0; i < len(payment.DefaultRetryPolicy.Delays); i++ {
		due(t, h.m, sub.ID)
		_, _ = h.m.RunDueSubscriptions(ctx)
	}
	sub, _ = h.m.Subscription(ctx, sub.ID)
	if sub.Status != payment.SubFailed || sub.NextBillingAt != nil {
		t.Fatalf("after retries %+v", sub)
	}
	// a manual retry after fixing the card is allowed
	sub.Amount = 1000
	if err := h.m.Store().UpdateSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	if sub, err = h.m.RetrySubscription(ctx, sub.ID); err != nil || sub.Status != payment.SubActive || sub.CyclesPaid != 1 {
		t.Fatalf("manual retry %v %+v", err, sub)
	}
	if sub, err = h.m.CancelSubscription(ctx, sub.ID); err != nil || sub.Status != payment.SubCanceled || sub.CanceledAt == nil {
		t.Fatalf("cancel %v %+v", err, sub)
	}
	due(t, h.m, sub.ID)
	if n, _ := h.m.RunDueSubscriptions(ctx); n != 0 {
		t.Fatal("billed a canceled subscription")
	}
}

func TestGatewayRunSubscription(t *testing.T) {
	for name, mk := range storeCases(t) {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, mk())
			h.saveTestGateway("capture", false)
			ctx := context.Background()
			sub, err := h.m.Subscribe(ctx, "test", payment.SubscriptionRequest{Reference: "GYM", Amount: 5000, Currency: "LKR",
				Interval: payment.Month, Customer: payment.Customer{Email: "k@example.com"},
				SuccessURL: "https://shop.test/welcome", CancelURL: "https://shop.test/plans"})
			if err != nil || sub.Status != payment.SubPending || sub.Billing != payment.BillingGateway {
				t.Fatalf("subscribe %v %+v", err, sub)
			}
			r := h.get(h.m.PayURL(&payment.Payment{ID: sub.ID}))
			html := body(h.get(r.Header.Get("Location")))
			back := h.get(linkFor(t, html, "pay"))
			if !strings.HasPrefix(back.Header.Get("Location"), "https://shop.test/welcome?subscription="+sub.ID) {
				t.Fatalf("returned to %s", back.Header.Get("Location"))
			}
			sub, _ = h.m.Subscription(ctx, sub.ID)
			if sub.Status != payment.SubActive || sub.CyclesPaid != 1 {
				t.Fatalf("after sign-up %+v", sub)
			}
			hook := func(outcome string) {
				t.Helper()
				if _, err := h.m.HandleWebhook(ctx, "test", httptest.NewRequest("POST", "/", nil),
					payment.TestWebhookBody("s3cret", sub.GatewayRef, outcome)); err != nil {
					t.Fatalf("%s: %v", outcome, err)
				}
			}
			hook("renew")
			hook("renew_fail")
			sub, _ = h.m.Subscription(ctx, sub.ID)
			if sub.Status != payment.SubPastDue || sub.CyclesPaid != 2 {
				t.Fatalf("after renew + fail %+v", sub)
			}
			if sub, err = h.m.RetrySubscription(ctx, sub.ID); err != nil || sub.Status != payment.SubActive || sub.CyclesPaid != 3 {
				t.Fatalf("gateway retry %v %+v", err, sub)
			}
			_, n, _ := h.m.SubscriptionPayments(ctx, sub.ID, 1, 10)
			if n != 4 { // 3 paid + 1 failed
				t.Fatalf("period payments %d", n)
			}
			if sub, err = h.m.CancelSubscription(ctx, sub.ID); err != nil || sub.Status != payment.SubCanceled {
				t.Fatalf("cancel %v", err)
			}
			hook("renew") // a late renewal after cancel is recorded, but the subscription stays canceled
			if sub, _ = h.m.Subscription(ctx, sub.ID); sub.Status != payment.SubCanceled {
				t.Fatalf("late renewal reopened it: %s", sub.Status)
			}
			if logs, _ := h.m.Logs(ctx, sub.ID); len(logs) < 5 {
				t.Fatalf("subscription history has %d entries", len(logs))
			}
		})
	}
}

func TestSubscribeNeedsSupport(t *testing.T) {
	m := payment.NewManager(payment.Options{})
	m.RegisterDriver(payment.TestDriver())
	ctx := context.Background()
	if _, err := m.Subscribe(ctx, "test", payment.SubscriptionRequest{Reference: "x", Amount: 1, Currency: "USD", Interval: "fortnight"}); err == nil {
		t.Fatal("bad interval accepted")
	}
	if _, err := m.Subscribe(ctx, "", payment.SubscriptionRequest{Reference: "x", Amount: 1, PaymentMethodID: "pm_missing"}); err == nil {
		t.Fatal("missing method accepted")
	}
}
