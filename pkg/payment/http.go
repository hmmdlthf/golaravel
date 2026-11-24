package payment

import (
	"errors"
	"html"
	"io"
	"net/http"
	"sort"
	"strings"

	gcontext "github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/router"
)

// maxWebhookBody caps notification bodies; gateways send a few KB.
const maxWebhookBody = 1 << 20

// Handlers are the payment endpoints, for mounting them yourself.
type Handlers struct{ m *Manager }

// Handlers returns the payment endpoints.
func (m *Manager) Handlers() Handlers { return Handlers{m} }

// Pay sends the customer to the gateway: GET {prefix}/{payment}/pay.
func (h Handlers) Pay(c *gcontext.Context) error {
	p, err := h.m.Payment(c.Context(), c.Param("payment"))
	if err != nil {
		return c.String(http.StatusNotFound, "Payment not found")
	}
	if p.Status != StatusPending || p.Action == nil {
		_, dest, _ := h.m.HandleReturn(c.Context(), p.ID, "", c.Request)
		return c.Redirect(http.StatusSeeOther, dest)
	}
	return WriteAction(&c.Response, c.Request, p.Action)
}

// Return is where the gateway sends the customer back:
// GET or POST {prefix}/{payment}/return?result=success|cancel|failure.
func (h Handlers) Return(c *gcontext.Context) error {
	_, dest, err := h.m.HandleReturn(c.Context(), c.Param("payment"), c.Query("result"), c.Request)
	if err != nil {
		return c.String(http.StatusNotFound, "Payment not found")
	}
	return c.Redirect(http.StatusSeeOther, dest)
}

// Webhook receives gateway notifications: POST {prefix}/webhooks/{gateway}.
func (h Handlers) Webhook(c *gcontext.Context) error {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxWebhookBody))
	if err != nil {
		return c.String(http.StatusBadRequest, "unreadable body")
	}
	_, err = h.m.HandleWebhook(c.Context(), c.Param("gateway"), c.Request, body)
	switch {
	case err == nil:
		return c.JSON(http.StatusOK, map[string]any{"received": true})
	case errors.Is(err, ErrInvalidSignature):
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid signature"})
	case errors.Is(err, ErrNotFound):
		// unknown payment: a 404 lets the gateway retry if our record is
		// still being written
		return c.JSON(http.StatusNotFound, map[string]string{"error": "unknown payment"})
	default:
		h.m.log.Error("payment: webhook failed", "gateway", c.Param("gateway"), "error", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "webhook failed"})
	}
}

// Routes mounts the payment endpoints under the manager's prefix:
//
//	GET  /payments/{payment}/pay       send the customer to the gateway
//	GET  /payments/{payment}/return    the customer comes back
//	POST /payments/{payment}/return    (gateways that post the customer back)
//	POST /payments/webhooks/{gateway}  gateway notifications
//	GET  /payments/test-gateway        the test driver's checkout page
//
// Webhooks come from the gateway, not the browser: exempt them from CSRF,
// for example middleware.CSRF("/payments/webhooks/*").
func (m *Manager) Routes(r router.RouteRegistrar) {
	h := m.Handlers()
	r.GET(m.prefix+"/:payment/pay", h.Pay)
	r.GET(m.prefix+"/:payment/return", h.Return)
	r.POST(m.prefix+"/:payment/return", h.Return)
	r.POST(m.prefix+"/webhooks/:gateway", h.Webhook)
	r.GET(m.prefix+"/test-gateway", h.TestGateway)
}

// TestGateway serves the test driver's pretend checkout page:
// GET {prefix}/test-gateway.
func (h Handlers) TestGateway(c *gcontext.Context) error {
	h.m.testGatewayPage(&c.Response, c.Request)
	return nil
}

// WriteAction hands the customer to the gateway: a redirect, or a page that
// posts a form to it. ActionClientSecret is answered as JSON for the
// application's own page to use.
func WriteAction(w http.ResponseWriter, r *http.Request, a *Action) error {
	switch a.Type {
	case ActionRedirect:
		http.Redirect(w, r, a.URL, http.StatusSeeOther)
		return nil
	case ActionPostForm:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		_, err := io.WriteString(w, AutoPostPage(a.URL, a.Fields))
		return err
	case ActionClientSecret:
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, err := io.WriteString(w, `{"client_secret":"`+jsonEscape(a.ClientSecret)+`"}`)
		return err
	}
	http.Error(w, "Nothing to pay", http.StatusConflict)
	return nil
}

// AutoPostPage is a minimal page that posts fields to url straight away,
// with a button for browsers without JavaScript.
func AutoPostPage(url string, fields map[string]string) string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Redirecting to payment…</title>`)
	b.WriteString(`<style>body{font-family:system-ui,sans-serif;display:grid;place-items:center;min-height:100vh;margin:0;color:#334155}button{font:inherit;padding:.6rem 1.2rem;border-radius:.5rem;border:1px solid #cbd5e1;background:#fff;cursor:pointer}</style></head>`)
	b.WriteString(`<body onload="document.forms[0].submit()"><form method="post" action="` + html.EscapeString(url) + `">`)
	for _, k := range keys {
		b.WriteString(`<input type="hidden" name="` + html.EscapeString(k) + `" value="` + html.EscapeString(fields[k]) + `">`)
	}
	b.WriteString(`<p>Taking you to the payment page…</p><noscript><button type="submit">Continue to payment</button></noscript></form></body></html>`)
	return b.String()
}

func jsonEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "<", `<`)
	return r.Replace(s)
}
