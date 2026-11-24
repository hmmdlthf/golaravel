package payment

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// TestDriver is a gateway that needs no account: its checkout page is a
// local page with "Pay", "Decline" and "Cancel" buttons. It also saves
// pretend cards (charges of an amount ending in .13 are declined) and runs
// pretend subscriptions. Use it for demos, development and tests. It
// refuses live mode.
//
// The page is served by the manager's routes at {prefix}/test-gateway, so
// TestDriver needs Manager.Routes (or Handlers().TestGateway) mounted.
func TestDriver() *Driver {
	return &Driver{
		Name:        "test",
		Label:       "Test gateway",
		Description: "A pretend card gateway for demos and development. No real money moves.",
		Features: Features{Refunds: true, PartialRefunds: true, StatusLookup: true, Capture: true, Cancel: true,
			Preapproval: true, Charging: true, Subscriptions: true},
		Fields: []Field{
			{Name: "signing_secret", Label: "Signing secret", Type: FieldSecret, Required: true,
				Help: "Any random string; it signs the pretend checkout result."},
			{Name: "flow", Label: "Flow", Type: FieldSelect, Default: "capture",
				Options: map[string]string{"capture": "Charge straight away", "authorize": "Authorize, capture later"},
				Help:    "\"Authorize\" behaves like buy-now-pay-later gateways."},
		},
		New: func(cfg Config) (Gateway, error) {
			if !cfg.Test() {
				return nil, fmt.Errorf("payment: the test gateway cannot run in live mode")
			}
			return &testGateway{secret: cfg.Get("signing_secret"), authorize: cfg.Get("flow") == "authorize"}, nil
		},
	}
}

type testGateway struct {
	secret    string
	authorize bool
}

func (g *testGateway) sign(parts ...string) string {
	m := hmac.New(sha256.New, []byte(g.secret))
	m.Write([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(m.Sum(nil))
}

// testRef is the test gateway's id for a record: "test_" + the record id.
func testRef(id string) string { return "test_" + id }

func (g *testGateway) page(notifyURL, ref, ret, cancel, fail string) string {
	base := strings.TrimSuffix(notifyURL, "/webhooks/"+gatewayIDFromNotify(notifyURL))
	return fmt.Sprintf("%s/test-gateway?ref=%s&ret=%s&cancel=%s&fail=%s&sig=%s", base, urlq(ref),
		urlq(ret), urlq(cancel), urlq(fail), g.sign(ref, ret))
}

func (g *testGateway) Checkout(_ context.Context, req *GatewayCheckout) (*Session, error) {
	ref := testRef(req.PaymentID)
	return &Session{GatewayRef: ref, Action: Action{Type: ActionRedirect,
		URL: g.page(req.NotifyURL, ref, req.ReturnURL, req.CancelReturnURL, req.FailureReturnURL)}}, nil
}

func (g *testGateway) Preapprove(_ context.Context, req *GatewayPreapproval) (*Session, error) {
	ref := testRef(req.MethodID)
	return &Session{GatewayRef: ref, Action: Action{Type: ActionRedirect,
		URL: g.page(req.NotifyURL, ref, req.ReturnURL, req.CancelReturnURL, req.FailureReturnURL)}}, nil
}

func (g *testGateway) Subscribe(_ context.Context, req *GatewaySubscription) (*Session, error) {
	ref := testRef(req.SubscriptionID)
	return &Session{GatewayRef: ref, Action: Action{Type: ActionRedirect,
		URL: g.page(req.NotifyURL, ref, req.ReturnURL, req.CancelReturnURL, req.FailureReturnURL)}}, nil
}

// ParseReturn trusts the result only with a valid signature from the page.
func (g *testGateway) ParseReturn(_ context.Context, p *Payment, r *http.Request) (*Update, error) {
	return g.signedOutcome(r, p.GatewayRef)
}

// ParseRecordReturn does the same for saved methods and subscriptions.
func (g *testGateway) ParseRecordReturn(_ context.Context, id string, r *http.Request) (*Update, error) {
	return g.signedOutcome(r, testRef(id))
}

func (g *testGateway) signedOutcome(r *http.Request, ref string) (*Update, error) {
	q := r.URL.Query()
	outcome := q.Get("outcome")
	if outcome == "" {
		return &Update{Ignore: true}, nil
	}
	if !hmac.Equal([]byte(q.Get("osig")), []byte(g.sign(ref, outcome))) {
		return nil, ErrInvalidSignature
	}
	return g.update(ref, outcome), nil
}

// update maps a pretend outcome for any record kind.
func (g *testGateway) update(ref, outcome string) *Update {
	id := strings.TrimPrefix(ref, "test_")
	u := &Update{Event: "test." + outcome}
	switch Kind(id) {
	case "method":
		mu := &MethodUpdate{MethodID: id, GatewayRef: ref}
		switch outcome {
		case "pay":
			mu.Status, mu.Token, mu.Brand, mu.Last4, mu.Expiry = MethodActive, "tok_"+id, "Visa", "4242", "12/30"
		case "decline":
			mu.Status, mu.FailureReason = MethodFailed, "Card declined (test)"
		case "cancel":
			mu.Status, mu.FailureReason = MethodFailed, "Canceled by the customer"
		default:
			u.Ignore = true
		}
		u.Method = mu
	case "subscription":
		su := &SubscriptionUpdate{SubscriptionID: id, GatewayRef: ref}
		switch outcome {
		case "pay", "renew":
			su.Status = SubActive
			su.Installment = &Installment{GatewayRef: NewID("test_inv"), Status: StatusPaid}
		case "renew_fail", "decline":
			if outcome == "decline" {
				su.Status, su.FailureReason = SubFailed, "Card declined (test)"
			} else {
				su.Status, su.FailureReason = SubPastDue, "Renewal declined (test)"
				su.Installment = &Installment{GatewayRef: NewID("test_inv"), Status: StatusFailed, FailureReason: su.FailureReason}
			}
		case "cancel":
			su.Status = SubCanceled
		default:
			u.Ignore = true
		}
		u.Subscription = su
	default:
		u.GatewayRef = ref
		switch outcome {
		case "pay":
			u.Status = StatusPaid
			if g.authorize {
				u.Status = StatusAuthorized
			}
		case "decline":
			u.Status, u.FailureReason = StatusFailed, "Card declined (test)"
		case "cancel":
			u.Status = StatusCanceled
		default:
			u.Ignore = true
		}
	}
	return u
}

// ParseWebhook accepts {"ref":..,"outcome":..,"sig":..} signed with the
// secret, so tests can simulate notifications. Outcomes: pay, decline,
// cancel; for subscriptions also renew and renew_fail.
func (g *testGateway) ParseWebhook(_ context.Context, _ *http.Request, body []byte) (*Update, error) {
	var v struct{ Ref, Outcome, Sig string }
	if json.Unmarshal(body, &v) != nil || !hmac.Equal([]byte(v.Sig), []byte(g.sign(v.Ref, v.Outcome))) {
		return nil, ErrInvalidSignature
	}
	u := g.update(v.Ref, v.Outcome)
	u.EventID = v.Sig[:16] + fmt.Sprint(time.Now().UnixNano()) // every simulated delivery is new
	return u, nil
}

func (g *testGateway) FetchStatus(_ context.Context, p *Payment) (*Update, error) {
	return &Update{Ignore: true}, nil
}

func (g *testGateway) Refund(_ context.Context, p *Payment, amount int64, _ string) (*RefundResult, error) {
	return &RefundResult{RefundRef: NewID("re")}, nil
}

func (g *testGateway) Capture(_ context.Context, p *Payment, amount int64) (*Update, error) {
	return &Update{Status: StatusPaid, Event: "test.capture"}, nil
}

func (g *testGateway) Cancel(_ context.Context, p *Payment) (*Update, error) {
	return &Update{Status: StatusCanceled, Event: "test.void"}, nil
}

// Charge declines amounts ending in .13 (minor units 13), like test cards.
func (g *testGateway) Charge(_ context.Context, m *PaymentMethod, p *Payment) (*Update, error) {
	if m.Token == "" {
		return nil, fmt.Errorf("test: the method has no token")
	}
	if p.Amount%100 == 13 {
		return &Update{Status: StatusFailed, FailureReason: "Card declined (test amount ending in .13)",
			GatewayRef: NewID("test_ch"), Event: "test.charge"}, nil
	}
	return &Update{Status: StatusPaid, GatewayRef: NewID("test_ch"), Event: "test.charge"}, nil
}

func (g *testGateway) RevokeMethod(context.Context, *PaymentMethod) error { return nil }

func (g *testGateway) CancelSubscription(_ context.Context, s *Subscription) (*SubscriptionUpdate, error) {
	return &SubscriptionUpdate{Status: SubCanceled}, nil
}

func (g *testGateway) RetrySubscription(_ context.Context, s *Subscription) (*SubscriptionUpdate, error) {
	return &SubscriptionUpdate{Status: SubActive,
		Installment: &Installment{GatewayRef: NewID("test_inv"), Status: StatusPaid}}, nil
}

func (g *testGateway) FetchSubscription(_ context.Context, s *Subscription) (*SubscriptionUpdate, error) {
	return &SubscriptionUpdate{}, nil
}

func (g *testGateway) TestConnection(context.Context) error { return nil }

// TestWebhookBody builds a signed notification body for the test gateway.
// ref is the record's GatewayRef ("test_" + its id).
func TestWebhookBody(secret, ref, outcome string) []byte {
	g := &testGateway{secret: secret}
	b, _ := json.Marshal(map[string]string{"ref": ref, "outcome": outcome, "sig": g.sign(ref, outcome)})
	return b
}

func gatewayIDFromNotify(u string) string {
	if i := strings.LastIndex(u, "/webhooks/"); i >= 0 {
		return u[i+len("/webhooks/"):]
	}
	return ""
}

func urlq(s string) string { return url.QueryEscape(s) }

// testGatewayPage renders the pretend checkout page.
func (m *Manager) testGatewayPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ref := q.Get("ref")
	id := strings.TrimPrefix(ref, "test_")
	var gatewayID, title, subtitle, payLabel string
	switch Kind(id) {
	case "payment":
		p, err := m.store.Payment(r.Context(), id)
		if err != nil || p.GatewayRef != ref {
			http.Error(w, "Unknown test payment", http.StatusNotFound)
			return
		}
		gatewayID, title = p.GatewayID, p.Money()
		subtitle = p.Reference + " · " + Pick(p.Description, "Test payment")
		payLabel = "Pay " + p.Money()
	case "method":
		pm, err := m.store.Method(r.Context(), id)
		if err != nil {
			http.Error(w, "Unknown test card", http.StatusNotFound)
			return
		}
		gatewayID, title, subtitle, payLabel = pm.GatewayID, "Save a card", Pick(pm.Description, "For future payments"), "Save Visa •••• 4242"
	case "subscription":
		sub, err := m.store.Subscription(r.Context(), id)
		if err != nil {
			http.Error(w, "Unknown test subscription", http.StatusNotFound)
			return
		}
		gatewayID, title = sub.GatewayID, sub.Money()
		subtitle = sub.Reference + " · " + sub.Schedule()
		payLabel = "Subscribe: " + sub.Money() + " " + sub.Schedule()
	default:
		http.Error(w, "Unknown test record", http.StatusNotFound)
		return
	}
	gw, _, _, err := m.gateway(r.Context(), gatewayID, false)
	tg, ok := gw.(*testGateway)
	if err != nil || !ok || !hmac.Equal([]byte(q.Get("sig")), []byte(tg.sign(ref, q.Get("ret")))) {
		http.Error(w, "Invalid test payment link", http.StatusForbidden)
		return
	}
	button := func(target, outcome, label, style string) string {
		u, err := url.Parse(target)
		if err != nil {
			return ""
		}
		v := u.Query()
		v.Set("outcome", outcome)
		v.Set("osig", tg.sign(ref, outcome))
		u.RawQuery = v.Encode()
		return `<a class="btn ` + style + `" href="` + html.EscapeString(u.String()) + `">` + html.EscapeString(label) + `</a>`
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Test checkout</title>
<style>:root{color-scheme:light dark}body{font-family:system-ui,sans-serif;margin:0;min-height:100vh;display:grid;place-items:center;background:Canvas;color:CanvasText}
.card{width:min(380px,calc(100vw - 32px));border:1px solid #8884;border-radius:16px;padding:28px;box-sizing:border-box}
.tag{display:inline-block;font-size:12px;padding:2px 8px;border-radius:99px;background:#f59e0b33;color:#b45309;font-weight:600}
h1{font-size:28px;margin:14px 0 4px}p{margin:0 0 22px;opacity:.7}.btn{display:block;text-align:center;padding:12px;border-radius:10px;text-decoration:none;font-weight:600;margin-top:10px}
.pay{background:#4f46e5;color:#fff}.decline{border:1px solid #dc262666;color:#dc2626}.cancel{color:inherit;opacity:.7}</style></head>
<body><div class="card"><span class="tag">TEST MODE</span><h1>%s</h1><p>%s</p>%s%s%s</div></body></html>`,
		html.EscapeString(title), html.EscapeString(subtitle),
		button(q.Get("ret"), "pay", payLabel, "pay"),
		button(q.Get("fail"), "decline", "Decline card", "decline"),
		button(q.Get("cancel"), "cancel", "Cancel and go back", "cancel"))
}
