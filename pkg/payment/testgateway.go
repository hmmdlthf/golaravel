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
)

// TestDriver is a gateway that needs no account: its checkout page is a
// local page with "Pay", "Decline" and "Cancel" buttons. Use it for demos,
// development and tests. It refuses live mode.
//
// The page is served by the manager's routes at {prefix}/test-gateway, so
// TestDriver needs Manager.Routes (or Handlers().TestGateway) mounted.
func TestDriver() *Driver {
	return &Driver{
		Name:        "test",
		Label:       "Test gateway",
		Description: "A pretend card gateway for demos and development. No real money moves.",
		Features:    Features{Refunds: true, PartialRefunds: true, StatusLookup: true, Capture: false, Cancel: true},
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

func (g *testGateway) Checkout(_ context.Context, req *GatewayCheckout) (*Session, error) {
	ref := "test_" + strings.TrimPrefix(req.PaymentID, "pay_")
	base := strings.TrimSuffix(req.NotifyURL, "/webhooks/"+gatewayIDFromNotify(req.NotifyURL))
	q := fmt.Sprintf("?ref=%s&amount=%s&currency=%s&ret=%s&cancel=%s&fail=%s&sig=%s",
		ref, Decimal(req.Amount, req.Currency), req.Currency,
		urlq(req.ReturnURL), urlq(req.CancelReturnURL), urlq(req.FailureReturnURL),
		g.sign(ref, req.ReturnURL))
	return &Session{GatewayRef: ref, Action: Action{Type: ActionRedirect, URL: base + "/test-gateway" + q}}, nil
}

// ParseReturn trusts the result only with a valid signature from the page.
func (g *testGateway) ParseReturn(_ context.Context, p *Payment, r *http.Request) (*Update, error) {
	q := r.URL.Query()
	outcome := q.Get("outcome")
	if outcome == "" {
		return &Update{Ignore: true}, nil
	}
	if !hmac.Equal([]byte(q.Get("osig")), []byte(g.sign(p.GatewayRef, outcome))) {
		return nil, ErrInvalidSignature
	}
	return g.update(p.GatewayRef, outcome), nil
}

func (g *testGateway) update(ref, outcome string) *Update {
	u := &Update{GatewayRef: ref, Event: "test." + outcome}
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
	return u
}

// ParseWebhook accepts {"ref":..,"outcome":..,"sig":..} signed with the
// secret, so tests can simulate notifications.
func (g *testGateway) ParseWebhook(_ context.Context, _ *http.Request, body []byte) (*Update, error) {
	var v struct{ Ref, Outcome, Sig string }
	if json.Unmarshal(body, &v) != nil || !hmac.Equal([]byte(v.Sig), []byte(g.sign(v.Ref, v.Outcome))) {
		return nil, ErrInvalidSignature
	}
	return g.update(v.Ref, v.Outcome), nil
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

func (g *testGateway) TestConnection(context.Context) error { return nil }

// TestWebhookBody builds a signed notification body for the test gateway.
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
	p, err := m.store.Payment(r.Context(), "pay_"+strings.TrimPrefix(ref, "test_"))
	if err != nil || p.GatewayRef != ref {
		http.Error(w, "Unknown test payment", http.StatusNotFound)
		return
	}
	gw, _, _, err := m.gateway(r.Context(), p.GatewayID, false)
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
		return `<a class="btn ` + style + `" href="` + html.EscapeString(u.String()) + `">` + label + `</a>`
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Test checkout</title>
<style>:root{color-scheme:light dark}body{font-family:system-ui,sans-serif;margin:0;min-height:100vh;display:grid;place-items:center;background:Canvas;color:CanvasText}
.card{width:min(380px,calc(100vw - 32px));border:1px solid #8884;border-radius:16px;padding:28px;box-sizing:border-box}
.tag{display:inline-block;font-size:12px;padding:2px 8px;border-radius:99px;background:#f59e0b33;color:#b45309;font-weight:600}
h1{font-size:28px;margin:14px 0 4px}p{margin:0 0 22px;opacity:.7}.btn{display:block;text-align:center;padding:12px;border-radius:10px;text-decoration:none;font-weight:600;margin-top:10px}
.pay{background:#4f46e5;color:#fff}.decline{border:1px solid #dc262666;color:#dc2626}.cancel{color:inherit;opacity:.7}</style></head>
<body><div class="card"><span class="tag">TEST MODE</span><h1>%s</h1><p>%s · %s</p>%s%s%s</div></body></html>`,
		html.EscapeString(p.Money()), html.EscapeString(p.Reference), html.EscapeString(Pick(p.Description, "Test payment")),
		button(q.Get("ret"), "pay", "Pay "+html.EscapeString(p.Money()), "pay"),
		button(q.Get("fail"), "decline", "Decline card", "decline"),
		button(q.Get("cancel"), "cancel", "Cancel and go back", "cancel"))
}
