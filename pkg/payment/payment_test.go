package payment_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/golaravel/golaravel/pkg/database"
	"github.com/golaravel/golaravel/pkg/golaravel"
	"github.com/golaravel/golaravel/pkg/payment"
	"github.com/golaravel/golaravel/pkg/session"
)

func TestMoney(t *testing.T) {
	cases := []struct {
		amount int64
		cur    string
		dec    string
		pretty string
	}{
		{125000, "SAR", "1250.00", "SAR 1,250.00"},
		{5, "USD", "0.05", "USD 0.05"},
		{1500, "JPY", "1500", "JPY 1,500"},
		{12345, "KWD", "12.345", "KWD 12.345"},
		{-250, "LKR", "-2.50", "LKR -2.50"},
	}
	for _, c := range cases {
		if got := payment.Decimal(c.amount, c.cur); got != c.dec {
			t.Errorf("Decimal(%d,%s) = %s, want %s", c.amount, c.cur, got, c.dec)
		}
		if got := payment.FormatMoney(c.amount, c.cur); got != c.pretty {
			t.Errorf("FormatMoney(%d,%s) = %s, want %s", c.amount, c.cur, got, c.pretty)
		}
		if n, err := payment.ParseDecimal(c.dec, c.cur); err != nil || n != c.amount {
			t.Errorf("ParseDecimal(%s) = %d, %v", c.dec, n, err)
		}
	}
	if n, _ := payment.ParseDecimal("10.5", "USD"); n != 1050 {
		t.Errorf("10.5 USD = %d", n)
	}
	if n, _ := payment.ParseDecimal("10.500", "USD"); n != 1050 {
		t.Errorf("10.500 USD = %d", n)
	}
	if _, err := payment.ParseDecimal("10.505", "USD"); err == nil {
		t.Error("10.505 USD should not parse")
	}
}

func TestTransitions(t *testing.T) {
	ok := [][2]payment.Status{
		{payment.StatusPending, payment.StatusPaid},
		{payment.StatusPending, payment.StatusAuthorized},
		{payment.StatusAuthorized, payment.StatusPaid},
		{payment.StatusPaid, payment.StatusPartiallyRefunded},
		{payment.StatusPartiallyRefunded, payment.StatusPartiallyRefunded},
		{payment.StatusPartiallyRefunded, payment.StatusRefunded},
		{payment.StatusCanceled, payment.StatusPaid},
	}
	bad := [][2]payment.Status{
		{payment.StatusPaid, payment.StatusPending},
		{payment.StatusPaid, payment.StatusFailed},
		{payment.StatusRefunded, payment.StatusPaid},
		{payment.StatusAuthorized, payment.StatusPending},
		{payment.StatusPaid, payment.StatusPaid},
	}
	for _, c := range ok {
		if !payment.CanTransition(c[0], c[1]) {
			t.Errorf("%s -> %s should be allowed", c[0], c[1])
		}
	}
	for _, c := range bad {
		if payment.CanTransition(c[0], c[1]) {
			t.Errorf("%s -> %s should not be allowed", c[0], c[1])
		}
	}
}

// harness is an app with the payment routes mounted on a real server.
type harness struct {
	t      *testing.T
	srv    *httptest.Server
	m      *payment.Manager
	client *http.Client
	mu     sync.Mutex
	events []string
}

func newHarness(t *testing.T, store payment.Store) *harness {
	h := &harness{t: t}
	app := golaravel.New()
	h.srv = httptest.NewServer(app)
	t.Cleanup(h.srv.Close)
	h.m = payment.NewManager(payment.Options{BaseURL: h.srv.URL, Store: store,
		Encrypter: session.NewEncrypter("test-app-key")})
	h.m.RegisterDriver(payment.TestDriver())
	h.m.Routes(app.Router)
	h.m.OnStatusChange(func(_ context.Context, e payment.StatusEvent) {
		h.mu.Lock()
		h.events = append(h.events, string(e.From)+">"+string(e.Payment.Status)+"@"+e.Source)
		h.mu.Unlock()
	})
	h.client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return h
}

func (h *harness) get(u string) *http.Response {
	h.t.Helper()
	resp, err := h.client.Get(u)
	if err != nil {
		h.t.Fatal(err)
	}
	return resp
}

func body(r *http.Response) string {
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	return string(b)
}

// linkFor pulls the href of a test-gateway button out of the page.
func linkFor(t *testing.T, page, outcome string) string {
	t.Helper()
	for _, part := range strings.Split(page, `href="`)[1:] {
		href := strings.ReplaceAll(part[:strings.Index(part, `"`)], "&amp;", "&")
		if strings.Contains(href, "outcome="+outcome) {
			return href
		}
	}
	t.Fatalf("no %s button on the page:\n%s", outcome, page)
	return ""
}

func (h *harness) saveTestGateway(flow string, auto bool) {
	h.t.Helper()
	err := h.m.SaveGateway(context.Background(), &payment.GatewayConfig{ID: "test", Driver: "test", Enabled: true,
		AutoCapture: auto, Settings: map[string]string{"signing_secret": "s3cret", "flow": flow}})
	if err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) pay(outcome string) (*payment.Payment, *http.Response) {
	h.t.Helper()
	ctx := context.Background()
	p, err := h.m.Checkout(ctx, "test", payment.CheckoutRequest{Reference: "ORD-1", Amount: 10000, Currency: "usd",
		Customer:   payment.Customer{FirstName: "Sam", Email: "sam@example.com"},
		SuccessURL: "https://shop.test/thanks", CancelURL: "https://shop.test/cart"})
	if err != nil {
		h.t.Fatal(err)
	}
	if p.Currency != "USD" || p.Status != payment.StatusPending || p.Action.Type != payment.ActionRedirect {
		h.t.Fatalf("bad new payment: %+v", p)
	}
	r := h.get(h.m.PayURL(p))
	if r.StatusCode != http.StatusSeeOther {
		h.t.Fatalf("pay: %d", r.StatusCode)
	}
	page := h.get(r.Header.Get("Location"))
	html := body(page)
	if page.StatusCode != 200 || !strings.Contains(html, "USD 100.00") {
		h.t.Fatalf("test page %d: %s", page.StatusCode, html)
	}
	back := h.get(linkFor(h.t, html, outcome))
	np, _ := h.m.Payment(ctx, p.ID)
	return np, back
}

func storeCases(t *testing.T) map[string]func() payment.Store {
	return map[string]func() payment.Store{
		"memory": func() payment.Store { return payment.NewMemoryStore() },
		"sqlite": func() payment.Store {
			db, err := sql.Open("sqlite3", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			db.SetMaxOpenConns(1)
			t.Cleanup(func() { db.Close() })
			s, err := payment.NewSQLStore(db, database.SQLite)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Migrate(context.Background()); err != nil {
				t.Fatal(err)
			}
			return s
		},
	}
}

func TestLifecycle(t *testing.T) {
	for name, mk := range storeCases(t) {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, mk())
			h.saveTestGateway("capture", false)
			ctx := context.Background()

			// secrets are encrypted at rest and masked when read
			raw, _ := h.m.Store().Gateway(ctx, "test")
			if !strings.HasPrefix(raw.Settings["signing_secret"], "enc:") {
				t.Fatalf("secret stored as %q", raw.Settings["signing_secret"])
			}
			shown, _ := h.m.GatewayConfig(ctx, "test")
			if shown.Settings["signing_secret"] != payment.MaskedValue || shown.Settings["flow"] != "capture" {
				t.Fatalf("shown settings %v", shown.Settings)
			}
			// saving the mask back keeps the secret
			shown.Name = "Card (test)"
			if err := h.m.SaveGateway(ctx, shown); err != nil {
				t.Fatal(err)
			}

			p, back := h.pay("pay")
			if p.Status != payment.StatusPaid || p.PaidAt == nil {
				t.Fatalf("status %s", p.Status)
			}
			if loc := back.Header.Get("Location"); loc != "https://shop.test/thanks?payment="+p.ID {
				t.Fatalf("returned to %s", loc)
			}

			// a stale notification cannot undo it
			if _, err := h.m.HandleWebhook(ctx, "test", httptest.NewRequest("POST", "/", nil),
				payment.TestWebhookBody("s3cret", p.GatewayRef, "cancel")); err != nil {
				t.Fatal(err)
			}
			if p, _ = h.m.Payment(ctx, p.ID); p.Status != payment.StatusPaid {
				t.Fatalf("stale cancel moved it to %s", p.Status)
			}

			// partial then full refund
			if p, _ = h.m.Refund(ctx, p.ID, 2500, "damaged"); p.Status != payment.StatusPartiallyRefunded || p.Refunded != 2500 {
				t.Fatalf("after partial refund: %s %d", p.Status, p.Refunded)
			}
			if _, err := h.m.Refund(ctx, p.ID, 9000, ""); err == nil {
				t.Fatal("over-refund accepted")
			}
			if p, _ = h.m.Refund(ctx, p.ID, 0, ""); p.Status != payment.StatusRefunded || p.Refunded != 10000 {
				t.Fatalf("after full refund: %s %d", p.Status, p.Refunded)
			}

			logs, _ := h.m.Logs(ctx, p.ID)
			var trail []string
			for _, l := range logs {
				trail = append(trail, l.Event)
			}
			if got := strings.Join(trail, ","); got != "created,return,webhook,refund,refund" {
				t.Fatalf("log trail %s", got)
			}
			want := "pending>paid@return,paid>partially_refunded@refund,partially_refunded>refunded@refund"
			if got := strings.Join(h.events, ","); got != want {
				t.Fatalf("events %s", got)
			}

			list, total, _ := h.m.Payments(ctx, payment.PaymentQuery{Search: "sam@", PerPage: 10})
			if total != 1 || len(list) != 1 || list[0].ID != p.ID {
				t.Fatalf("search found %d", total)
			}
		})
	}
}

func TestDeclineAndCancel(t *testing.T) {
	h := newHarness(t, payment.NewMemoryStore())
	h.saveTestGateway("capture", false)
	p, back := h.pay("decline")
	if p.Status != payment.StatusFailed || p.FailureReason == "" {
		t.Fatalf("decline: %s %q", p.Status, p.FailureReason)
	}
	if !strings.HasPrefix(back.Header.Get("Location"), "https://shop.test/cart?") {
		t.Fatalf("declined customer sent to %s", back.Header.Get("Location"))
	}
	p, _ = h.pay("cancel")
	if p.Status != payment.StatusCanceled {
		t.Fatalf("cancel: %s", p.Status)
	}
}

func TestForgedReturnIsIgnored(t *testing.T) {
	h := newHarness(t, payment.NewMemoryStore())
	h.saveTestGateway("capture", false)
	ctx := context.Background()
	p, _ := h.m.Checkout(ctx, "test", payment.CheckoutRequest{Reference: "X", Amount: 100, Currency: "USD",
		SuccessURL: "https://shop.test/ok", CancelURL: "https://shop.test/no"})
	r := h.get(h.m.URL(p.ID+"/return", url.Values{"result": {"success"}, "outcome": {"pay"}, "osig": {"forged"}}))
	np, _ := h.m.Payment(ctx, p.ID)
	if np.Status != payment.StatusPending {
		t.Fatalf("forged return made it %s", np.Status)
	}
	// still pending but the customer said success: they land on the
	// success page, which shows "processing"
	if !strings.HasPrefix(r.Header.Get("Location"), "https://shop.test/ok") {
		t.Fatalf("sent to %s", r.Header.Get("Location"))
	}
}

func TestWebhookEndpoint(t *testing.T) {
	h := newHarness(t, payment.NewMemoryStore())
	h.saveTestGateway("capture", false)
	ctx := context.Background()
	p, _ := h.m.Checkout(ctx, "test", payment.CheckoutRequest{Reference: "W", Amount: 500, Currency: "USD"})
	post := func(b []byte) int {
		resp, err := http.Post(h.m.URL("webhooks/test"), "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post(payment.TestWebhookBody("wrong", p.GatewayRef, "pay")); code != http.StatusUnauthorized {
		t.Fatalf("bad signature got %d", code)
	}
	if code := post(payment.TestWebhookBody("s3cret", "test_nope", "pay")); code != http.StatusNotFound {
		t.Fatalf("unknown payment got %d", code)
	}
	if code := post(payment.TestWebhookBody("s3cret", p.GatewayRef, "pay")); code != http.StatusOK {
		t.Fatalf("good webhook got %d", code)
	}
	if p, _ = h.m.Payment(ctx, p.ID); p.Status != payment.StatusPaid {
		t.Fatalf("webhook left it %s", p.Status)
	}
}

func TestAuthorizeAndCapture(t *testing.T) {
	h := newHarness(t, payment.NewMemoryStore())
	h.saveTestGateway("authorize", false)
	ctx := context.Background()
	p, _ := h.pay("pay")
	if p.Status != payment.StatusAuthorized {
		t.Fatalf("authorize flow: %s", p.Status)
	}
	if _, err := h.m.Refund(ctx, p.ID, 0, ""); err == nil {
		t.Fatal("refunded an uncaptured payment")
	}
	if p, _ = h.m.Capture(ctx, p.ID, 0); p.Status != payment.StatusPaid {
		t.Fatalf("capture: %s", p.Status)
	}
}

func TestGatewayValidation(t *testing.T) {
	m := payment.NewManager(payment.Options{})
	m.RegisterDriver(payment.TestDriver())
	ctx := context.Background()
	if err := m.SaveGateway(ctx, &payment.GatewayConfig{ID: "Bad ID", Driver: "test"}); err == nil {
		t.Error("bad id accepted")
	}
	if err := m.SaveGateway(ctx, &payment.GatewayConfig{ID: "x", Driver: "nope"}); err == nil {
		t.Error("unknown driver accepted")
	}
	err := m.SaveGateway(ctx, &payment.GatewayConfig{ID: "x", Driver: "test", Settings: map[string]string{}})
	if err == nil || !strings.Contains(err.Error(), "Signing secret") {
		t.Errorf("missing secret: %v", err)
	}
	err = m.SaveGateway(ctx, &payment.GatewayConfig{ID: "x", Driver: "test", Mode: payment.ModeLive,
		Settings: map[string]string{"signing_secret": "a"}})
	if err == nil {
		t.Error("test gateway accepted live mode")
	}
	_ = m.SaveGateway(ctx, &payment.GatewayConfig{ID: "off", Driver: "test", Settings: map[string]string{"signing_secret": "a"}})
	if _, err := m.Checkout(ctx, "off", payment.CheckoutRequest{Reference: "a", Amount: 1, Currency: "USD"}); !errors.Is(err, payment.ErrGatewayDisabled) {
		t.Errorf("disabled gateway: %v", err)
	}
	opts, _ := m.Options(ctx, "USD")
	if len(opts) != 0 {
		t.Errorf("disabled gateway offered: %v", opts)
	}
}

func TestAutoPostPageEscapes(t *testing.T) {
	page := payment.AutoPostPage(`https://pay.example/"x`, map[string]string{"a": `"><script>`})
	if strings.Contains(page, "<script>") || strings.Contains(page, `/"x`) {
		t.Fatalf("unescaped: %s", page)
	}
}
