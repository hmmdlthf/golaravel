package payhere

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/golaravel/golaravel/pkg/payment"
)

// form builds the customer fields every PayHere hosted form shares.
func (g *Gateway) form(orderID, items, currency, amount, ret, cancel, notify string, c payment.Customer) map[string]string {
	first, last := payment.FirstLast(c, "Customer")
	addr := c.Address
	if addr == nil {
		addr = &payment.Address{}
	}
	return map[string]string{
		"merchant_id": g.cfg.Get("merchant_id"), "return_url": ret, "cancel_url": cancel, "notify_url": notify,
		"order_id": orderID, "items": items, "currency": currency, "amount": amount,
		"first_name": first, "last_name": last, "email": c.Email, "phone": c.Phone,
		"address": strings.TrimSpace(addr.Line1 + " " + addr.Line2), "city": addr.City,
		"country": payment.Pick(countryName(addr.Country), "Sri Lanka"),
		"hash":    CheckoutHash(g.cfg.Get("merchant_id"), orderID, amount, currency, g.cfg.Get("merchant_secret")),
	}
}

// Preapprove posts the customer to PayHere's preapproval page, which saves
// the card and returns a customer_token on notify_url. The hash uses the
// checkout formula with a zero amount.
func (g *Gateway) Preapprove(_ context.Context, req *payment.GatewayPreapproval) (*payment.Session, error) {
	cur := payment.Pick(req.Currency, "LKR")
	f := g.form(req.MethodID, payment.Pick(req.Description, "Save card for future payments"), cur,
		payment.Decimal(0, cur), req.ReturnURL, req.CancelReturnURL, req.NotifyURL, req.Customer)
	return &payment.Session{Action: payment.Action{Type: payment.ActionPostForm, URL: g.base + "/pay/preapprove", Fields: f}}, nil
}

// Charge uses the Charging API with the saved customer_token.
func (g *Gateway) Charge(ctx context.Context, m *payment.PaymentMethod, p *payment.Payment) (*payment.Update, error) {
	tok, err := g.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	var out struct {
		Status int    `json:"status"`
		Msg    string `json:"msg"`
		Data   *struct {
			PaymentID     any    `json:"payment_id"`
			StatusCode    any    `json:"status_code"`
			StatusMessage string `json:"status_message"`
		} `json:"data"`
	}
	err = payment.Call{Gateway: "payhere", Client: g.cfg.Client(), Method: http.MethodPost,
		URL: g.base + "/merchant/v1/payment/charge", Header: payment.Bearer(tok), JSON: map[string]any{
			"type": "PAYMENT", "order_id": p.ID, "items": payment.Pick(p.Description, p.Reference),
			"currency": p.Currency, "amount": json.Number(payment.Decimal(p.Amount, p.Currency)),
			"customer_token": m.Token, "custom_1": p.Reference,
		}}.Do(ctx, &out)
	if err != nil {
		return nil, err
	}
	if out.Status != 1 || out.Data == nil {
		return &payment.Update{Status: payment.StatusFailed, FailureReason: payment.Pick(out.Msg, "Charge refused"), Event: "charge"}, nil
	}
	st, reason, ok := status(fmt.Sprint(out.Data.StatusCode))
	u := &payment.Update{GatewayRef: fmt.Sprint(out.Data.PaymentID), Status: st, Event: "charge " + out.Data.StatusMessage,
		FailureReason: payment.Pick(out.Data.StatusMessage, reason)}
	if !ok || st == payment.StatusPending {
		u.Ignore = true
	}
	if st == payment.StatusPaid {
		u.FailureReason = ""
	}
	return u, nil
}

// recurrence writes PayHere's "1 Month" style periods. PayHere bills by
// week, month or year.
func recurrence(i payment.Interval, n int) (string, error) {
	unit := map[payment.Interval]string{payment.Week: "Week", payment.Month: "Month", payment.Year: "Year"}[i]
	if unit == "" {
		return "", fmt.Errorf("payhere: subscriptions bill weekly, monthly or yearly")
	}
	return strconv.Itoa(max(n, 1)) + " " + unit, nil
}

// Subscribe posts the customer to a recurring checkout.
func (g *Gateway) Subscribe(_ context.Context, req *payment.GatewaySubscription) (*payment.Session, error) {
	rec, err := recurrence(req.Interval, req.IntervalCount)
	if err != nil {
		return nil, err
	}
	duration := "Forever"
	if req.Cycles > 0 {
		duration, _ = recurrence(req.Interval, req.Cycles*max(req.IntervalCount, 1))
	}
	f := g.form(req.SubscriptionID, payment.Pick(req.Description, "Subscription "+req.Reference), req.Currency,
		payment.Decimal(req.Amount, req.Currency), req.ReturnURL, req.CancelReturnURL, req.NotifyURL, req.Customer)
	f["recurrence"], f["duration"], f["custom_1"] = rec, duration, req.Reference
	return &payment.Session{Action: payment.Action{Type: payment.ActionPostForm, URL: g.base + "/pay/checkout", Fields: f}}, nil
}

// recordNotify maps a verified notify for a saved method or subscription.
func (g *Gateway) recordNotify(v url.Values) *payment.Update {
	id := v.Get("order_id")
	ev := "payhere:" + v.Get("payment_id") + ":" + v.Get("message_type") + ":" + v.Get("status_code")
	if payment.Kind(id) == "method" {
		mu := &payment.MethodUpdate{MethodID: id, Token: v.Get("customer_token")}
		if v.Get("status_code") == "2" && mu.Token != "" {
			mu.Status, mu.Brand, mu.Last4 = payment.MethodActive, v.Get("method"), last4(v.Get("card_no"))
			mu.Expiry = v.Get("card_expiry")
		} else if code := v.Get("status_code"); code == "-1" || code == "-2" {
			mu.Status, mu.FailureReason = payment.MethodFailed, payment.Pick(v.Get("status_message"), "Card not saved")
		} else {
			return &payment.Update{Ignore: true}
		}
		return &payment.Update{Method: mu, EventID: ev, Event: "preapproval " + v.Get("status_code")}
	}
	su := &payment.SubscriptionUpdate{SubscriptionID: id, GatewayRef: v.Get("subscription_id")}
	if t, err := time.Parse("2006-01-02", v.Get("item_rec_date_next")); err == nil {
		su.NextBillingAt = &t
	}
	reason := payment.Pick(v.Get("status_message"), "Payment failed")
	switch strings.ToUpper(v.Get("message_type")) {
	case "AUTHORIZATION_SUCCESS", "RECURRING_INSTALLMENT_SUCCESS":
		su.Status = payment.SubActive
		su.Installment = &payment.Installment{GatewayRef: v.Get("payment_id"), Status: payment.StatusPaid}
		if n, err := payment.ParseDecimal(v.Get("payhere_amount"), v.Get("payhere_currency")); err == nil {
			su.Installment.Amount = n
		}
	case "AUTHORIZATION_FAILED":
		su.Status, su.FailureReason = payment.SubFailed, reason
	case "RECURRING_INSTALLMENT_FAILED":
		su.Status, su.FailureReason = payment.SubPastDue, reason
		su.Installment = &payment.Installment{GatewayRef: v.Get("payment_id"), Status: payment.StatusFailed, FailureReason: reason}
	case "RECURRING_COMPLETE":
		su.Status = payment.SubCompleted
	case "RECURRING_STOPPED":
		su.Status = payment.SubCanceled
	default:
		// a plain notify for the first payment, without message_type
		switch v.Get("status_code") {
		case "2":
			su.Status = payment.SubActive
			su.Installment = &payment.Installment{GatewayRef: v.Get("payment_id"), Status: payment.StatusPaid}
		case "-1", "-2":
			su.Status, su.FailureReason = payment.SubFailed, reason
		default:
			return &payment.Update{Ignore: true}
		}
	}
	return &payment.Update{Subscription: su, EventID: ev, Event: "recurring " + v.Get("message_type")}
}

func last4(card string) string {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, card)
	if len(digits) >= 4 {
		return digits[len(digits)-4:]
	}
	return ""
}

func (g *Gateway) subscriptionCall(ctx context.Context, method, path string, body, out any) error {
	tok, err := g.accessToken(ctx)
	if err != nil {
		return err
	}
	var env struct {
		Status int             `json:"status"`
		Msg    string          `json:"msg"`
		Data   json.RawMessage `json:"data"`
	}
	if err := (payment.Call{Gateway: "payhere", Client: g.cfg.Client(), Method: method, URL: g.base + path,
		Header: payment.Bearer(tok), JSON: body}).Do(ctx, &env); err != nil {
		return err
	}
	if env.Status != 1 {
		return fmt.Errorf("payhere: %s", payment.Pick(env.Msg, "request refused"))
	}
	if out != nil && len(env.Data) > 0 {
		return json.Unmarshal(env.Data, out)
	}
	return nil
}

// CancelSubscription uses the Subscription Manager API.
func (g *Gateway) CancelSubscription(ctx context.Context, s *payment.Subscription) (*payment.SubscriptionUpdate, error) {
	if s.GatewayRef == "" {
		return &payment.SubscriptionUpdate{Status: payment.SubCanceled}, nil
	}
	if err := g.subscriptionCall(ctx, http.MethodPost, "/merchant/v1/subscription/cancel",
		map[string]any{"subscription_id": json.Number(s.GatewayRef)}, nil); err != nil {
		return nil, err
	}
	return &payment.SubscriptionUpdate{Status: payment.SubCanceled}, nil
}

// RetrySubscription retries a failed installment; the result arrives on
// notify_url.
func (g *Gateway) RetrySubscription(ctx context.Context, s *payment.Subscription) (*payment.SubscriptionUpdate, error) {
	if err := g.subscriptionCall(ctx, http.MethodPost, "/merchant/v1/subscription/retry",
		map[string]any{"subscription_id": json.Number(s.GatewayRef)}, nil); err != nil {
		return nil, err
	}
	return nil, nil
}

// FetchSubscription finds the subscription in the Subscription Manager list.
func (g *Gateway) FetchSubscription(ctx context.Context, s *payment.Subscription) (*payment.SubscriptionUpdate, error) {
	var list []struct {
		SubscriptionID any    `json:"subscription_id"`
		OrderID        string `json:"order_id"`
		Status         string `json:"status"` // ACTIVE, COMPLETED, CANCELLED, FAILED, PENDING
		NextPaymentAt  string `json:"next_payment_date"`
	}
	if err := g.subscriptionCall(ctx, http.MethodGet, "/merchant/v1/subscription", nil, &list); err != nil {
		return nil, err
	}
	for _, it := range list {
		if it.OrderID != s.ID && fmt.Sprint(it.SubscriptionID) != s.GatewayRef {
			continue
		}
		su := &payment.SubscriptionUpdate{GatewayRef: fmt.Sprint(it.SubscriptionID)}
		switch strings.ToUpper(it.Status) {
		case "ACTIVE":
			su.Status = payment.SubActive
		case "COMPLETED":
			su.Status = payment.SubCompleted
		case "CANCELLED", "CANCELED":
			su.Status = payment.SubCanceled
		case "FAILED":
			su.Status = payment.SubFailed
		}
		if t, err := time.Parse("2006-01-02", it.NextPaymentAt); err == nil {
			su.NextBillingAt = &t
		}
		return su, nil
	}
	return nil, nil
}
