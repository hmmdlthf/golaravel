package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/golaravel/golaravel/pkg/payment"
)

func idOf(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case map[string]any:
		id, _ := x["id"].(string)
		return id
	}
	return ""
}

func (g *Gateway) customer(ctx context.Context, c payment.Customer, meta map[string]string) (string, error) {
	f := url.Values{}
	if c.Email != "" {
		f.Set("email", c.Email)
	}
	if n := c.Name(); n != "" {
		f.Set("name", n)
	}
	if c.Phone != "" {
		f.Set("phone", c.Phone)
	}
	for k, v := range meta {
		f.Set("metadata["+k+"]", v)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := g.call(ctx, http.MethodPost, "/v1/customers", f, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// Preapprove saves a card with a Checkout Session in setup mode.
func (g *Gateway) Preapprove(ctx context.Context, req *payment.GatewayPreapproval) (*payment.Session, error) {
	cus, err := g.customer(ctx, req.Customer, map[string]string{"method_id": req.MethodID, "customer_ref": req.CustomerRef})
	if err != nil {
		return nil, err
	}
	f := url.Values{
		"mode": {"setup"}, "customer": {cus}, "client_reference_id": {req.MethodID},
		"success_url": {req.ReturnURL}, "cancel_url": {req.CancelReturnURL},
		"metadata[method_id]": {req.MethodID}, "setup_intent_data[metadata][method_id]": {req.MethodID},
	}
	if req.Currency != "" {
		f.Set("currency", strings.ToLower(req.Currency))
	} else {
		f.Set("payment_method_types[0]", "card")
	}
	var s session
	if err := g.call(ctx, http.MethodPost, "/v1/checkout/sessions", f, &s); err != nil {
		return nil, err
	}
	return &payment.Session{GatewayRef: "", Data: map[string]string{"customer": cus, "checkout_session": s.ID},
		Action: payment.Action{Type: payment.ActionRedirect, URL: s.URL}}, nil
}

// methodFromSetupIntent reads the saved card behind a setup intent.
func (g *Gateway) methodFromSetupIntent(ctx context.Context, methodID, setupIntent string) (*payment.MethodUpdate, error) {
	var si struct {
		Status        string `json:"status"`
		Customer      any    `json:"customer"`
		PaymentMethod struct {
			ID   string `json:"id"`
			Card struct {
				Brand    string `json:"brand"`
				Last4    string `json:"last4"`
				ExpMonth int    `json:"exp_month"`
				ExpYear  int    `json:"exp_year"`
			} `json:"card"`
			Type string `json:"type"`
		} `json:"payment_method"`
		LastError *struct {
			Message string `json:"message"`
		} `json:"last_setup_error"`
	}
	if err := g.call(ctx, http.MethodGet, "/v1/setup_intents/"+url.PathEscape(setupIntent)+"?expand[]=payment_method", nil, &si); err != nil {
		return nil, err
	}
	mu := &payment.MethodUpdate{MethodID: methodID, GatewayRef: si.PaymentMethod.ID, Token: si.PaymentMethod.ID,
		Data: map[string]string{"customer": idOf(si.Customer), "setup_intent": setupIntent}}
	if si.Status != "succeeded" {
		mu.Status = payment.MethodFailed
		if si.LastError != nil {
			mu.FailureReason = si.LastError.Message
		}
		return mu, nil
	}
	mu.Status = payment.MethodActive
	c := si.PaymentMethod.Card
	mu.Brand = strings.ToUpper(c.Brand[:min(1, len(c.Brand))]) + c.Brand[min(1, len(c.Brand)):]
	mu.Last4 = c.Last4
	if c.ExpMonth > 0 {
		mu.Expiry = fmt.Sprintf("%02d/%02d", c.ExpMonth, c.ExpYear%100)
	}
	if mu.Brand == "" {
		mu.Brand = si.PaymentMethod.Type
	}
	return mu, nil
}

// Charge confirms an off-session payment intent on the saved method.
func (g *Gateway) Charge(ctx context.Context, m *payment.PaymentMethod, p *payment.Payment) (*payment.Update, error) {
	cus := m.Data["customer"]
	if cus == "" || m.Token == "" {
		return nil, errors.New("stripe: the saved method has no customer or payment method")
	}
	f := url.Values{
		"amount": {strconv.FormatInt(p.Amount, 10)}, "currency": {strings.ToLower(p.Currency)},
		"customer": {cus}, "payment_method": {m.Token}, "off_session": {"true"}, "confirm": {"true"},
		"metadata[payment_id]": {p.ID}, "metadata[reference]": {p.Reference},
	}
	if p.Description != "" {
		f.Set("description", p.Description)
	}
	var pi intent
	err := g.call(ctx, http.MethodPost, "/v1/payment_intents", f, &pi)
	var apiErr *payment.APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusPaymentRequired {
		return &payment.Update{Status: payment.StatusFailed, FailureReason: apiErr.Message, Event: "charge declined"}, nil
	}
	if err != nil {
		return nil, err
	}
	u := &payment.Update{GatewayRef: pi.ID, Data: map[string]string{"payment_intent": pi.ID}, Event: "charge " + pi.Status}
	switch pi.Status {
	case "succeeded":
		u.Status = payment.StatusPaid
	case "requires_capture":
		u.Status = payment.StatusAuthorized
	case "processing":
		u.Ignore = true // a webhook settles it
	default:
		u.Status, u.FailureReason = payment.StatusFailed, "The bank asked the customer to authenticate ("+pi.Status+")"
	}
	return u, nil
}

// RevokeMethod detaches the payment method from its customer.
func (g *Gateway) RevokeMethod(ctx context.Context, m *payment.PaymentMethod) error {
	if m.Token == "" {
		return nil
	}
	return g.call(ctx, http.MethodPost, "/v1/payment_methods/"+url.PathEscape(m.Token)+"/detach", url.Values{}, nil)
}

// Subscribe starts a Stripe Billing subscription through Checkout.
func (g *Gateway) Subscribe(ctx context.Context, req *payment.GatewaySubscription) (*payment.Session, error) {
	f := url.Values{
		"mode": {"subscription"}, "client_reference_id": {req.SubscriptionID},
		"success_url": {req.ReturnURL}, "cancel_url": {req.CancelReturnURL},
		"metadata[subscription_id]":                            {req.SubscriptionID},
		"subscription_data[metadata][subscription_id]":         {req.SubscriptionID},
		"subscription_data[metadata][reference]":               {req.Reference},
		"line_items[0][quantity]":                              {"1"},
		"line_items[0][price_data][currency]":                  {strings.ToLower(req.Currency)},
		"line_items[0][price_data][unit_amount]":               {strconv.FormatInt(req.Amount, 10)},
		"line_items[0][price_data][recurring][interval]":       {string(req.Interval)},
		"line_items[0][price_data][recurring][interval_count]": {strconv.Itoa(req.IntervalCount)},
		"line_items[0][price_data][product_data][name]":        {payment.Pick(req.Description, "Subscription "+req.Reference)},
	}
	if req.Customer.Email != "" {
		f.Set("customer_email", req.Customer.Email)
	}
	var s session
	if err := g.call(ctx, http.MethodPost, "/v1/checkout/sessions", f, &s); err != nil {
		return nil, err
	}
	return &payment.Session{Data: map[string]string{"checkout_session": s.ID},
		Action: payment.Action{Type: payment.ActionRedirect, URL: s.URL}}, nil
}

type subscription struct {
	ID               string            `json:"id"`
	Status           string            `json:"status"`
	Metadata         map[string]string `json:"metadata"`
	CurrentPeriodEnd int64             `json:"current_period_end"`
	LatestInvoice    any               `json:"latest_invoice"`
	Items            struct {
		Data []struct {
			CurrentPeriodEnd int64 `json:"current_period_end"`
		} `json:"data"`
	} `json:"items"`
}

func (s *subscription) update() *payment.SubscriptionUpdate {
	su := &payment.SubscriptionUpdate{SubscriptionID: s.Metadata["subscription_id"], GatewayRef: s.ID}
	switch s.Status {
	case "active", "trialing":
		su.Status = payment.SubActive
	case "past_due", "unpaid":
		su.Status = payment.SubPastDue
	case "canceled":
		su.Status = payment.SubCanceled
	case "incomplete_expired":
		su.Status, su.FailureReason = payment.SubFailed, "The first payment was not completed"
	}
	end := s.CurrentPeriodEnd
	if end == 0 && len(s.Items.Data) > 0 {
		end = s.Items.Data[0].CurrentPeriodEnd
	}
	if end > 0 {
		t := time.Unix(end, 0)
		su.NextBillingAt = &t
	}
	return su
}

type invoice struct {
	ID           string `json:"id"`
	AmountPaid   int64  `json:"amount_paid"`
	AmountDue    int64  `json:"amount_due"`
	Subscription any    `json:"subscription"`
	// newer API versions nest the subscription under parent
	Parent struct {
		SubscriptionDetails struct {
			Subscription any               `json:"subscription"`
			Metadata     map[string]string `json:"metadata"`
		} `json:"subscription_details"`
	} `json:"parent"`
	SubscriptionDetails struct {
		Metadata map[string]string `json:"metadata"`
	} `json:"subscription_details"`
	Status string `json:"status"`
}

func (in *invoice) subscriptionRefs() (ours, theirs string) {
	ours = payment.Pick(in.SubscriptionDetails.Metadata["subscription_id"], in.Parent.SubscriptionDetails.Metadata["subscription_id"])
	theirs = payment.Pick(idOf(in.Subscription), idOf(in.Parent.SubscriptionDetails.Subscription))
	return
}

// recurringEvent handles setup and subscription events; handled is false
// for everything else.
func (g *Gateway) recurringEvent(ctx context.Context, typ string, obj json.RawMessage) (*payment.Update, bool, error) {
	switch {
	case typ == "checkout.session.completed" || typ == "checkout.session.expired":
		var s session
		_ = json.Unmarshal(obj, &s)
		switch s.Mode {
		case "setup":
			id := payment.Pick(s.ClientRef, s.Metadata["method_id"])
			if id == "" {
				return &payment.Update{Ignore: true}, true, nil
			}
			if typ == "checkout.session.expired" {
				return &payment.Update{Method: &payment.MethodUpdate{MethodID: id, Status: payment.MethodFailed,
					FailureReason: "The customer did not finish"}}, true, nil
			}
			mu, err := g.methodFromSetupIntent(ctx, id, idOf(s.SetupIntent))
			if err != nil {
				return nil, true, err
			}
			return &payment.Update{Method: mu}, true, nil
		case "subscription":
			id := payment.Pick(s.ClientRef, s.Metadata["subscription_id"])
			if id == "" {
				return &payment.Update{Ignore: true}, true, nil
			}
			su := &payment.SubscriptionUpdate{SubscriptionID: id, GatewayRef: idOf(s.Subscription)}
			if typ == "checkout.session.expired" {
				su.Status, su.FailureReason = payment.SubFailed, "The customer did not finish signing up"
			} else if s.PaymentStatus == "paid" || s.PaymentStatus == "no_payment_required" {
				su.Status = payment.SubActive
			}
			return &payment.Update{Subscription: su}, true, nil
		}
		return nil, false, nil
	case strings.HasPrefix(typ, "customer.subscription."):
		var sub subscription
		_ = json.Unmarshal(obj, &sub)
		if sub.Metadata["subscription_id"] == "" {
			return &payment.Update{Ignore: true}, true, nil // not started by this app
		}
		su := sub.update()
		if typ == "customer.subscription.created" {
			su.Status = "" // activation comes with the first paid invoice
		}
		return &payment.Update{Subscription: su}, true, nil
	case typ == "invoice.paid" || typ == "invoice.payment_failed":
		var in invoice
		_ = json.Unmarshal(obj, &in)
		ours, theirs := in.subscriptionRefs()
		if ours == "" && theirs == "" {
			return &payment.Update{Ignore: true}, true, nil
		}
		su := &payment.SubscriptionUpdate{SubscriptionID: ours, GatewayRef: theirs}
		if typ == "invoice.paid" {
			if in.AmountPaid == 0 {
				return &payment.Update{Ignore: true}, true, nil // trial or credit
			}
			su.Status = payment.SubActive
			su.Installment = &payment.Installment{GatewayRef: in.ID, Amount: in.AmountPaid, Status: payment.StatusPaid}
		} else {
			su.Status, su.FailureReason = payment.SubPastDue, "Renewal payment failed"
			su.Installment = &payment.Installment{GatewayRef: in.ID, Amount: in.AmountDue, Status: payment.StatusFailed,
				FailureReason: su.FailureReason}
		}
		return &payment.Update{Subscription: su}, true, nil
	}
	return nil, false, nil
}

// CancelSubscription cancels at Stripe straight away.
func (g *Gateway) CancelSubscription(ctx context.Context, s *payment.Subscription) (*payment.SubscriptionUpdate, error) {
	if s.GatewayRef == "" {
		return &payment.SubscriptionUpdate{Status: payment.SubCanceled}, nil
	}
	var sub subscription
	if err := g.call(ctx, http.MethodDelete, "/v1/subscriptions/"+url.PathEscape(s.GatewayRef), nil, &sub); err != nil {
		return nil, err
	}
	return &payment.SubscriptionUpdate{Status: payment.SubCanceled}, nil
}

// FetchSubscription reads the subscription.
func (g *Gateway) FetchSubscription(ctx context.Context, s *payment.Subscription) (*payment.SubscriptionUpdate, error) {
	if s.GatewayRef == "" {
		return nil, nil
	}
	var sub subscription
	if err := g.call(ctx, http.MethodGet, "/v1/subscriptions/"+url.PathEscape(s.GatewayRef), nil, &sub); err != nil {
		return nil, err
	}
	return sub.update(), nil
}

// RetrySubscription pays the latest open invoice now.
func (g *Gateway) RetrySubscription(ctx context.Context, s *payment.Subscription) (*payment.SubscriptionUpdate, error) {
	var sub subscription
	if err := g.call(ctx, http.MethodGet, "/v1/subscriptions/"+url.PathEscape(s.GatewayRef), nil, &sub); err != nil {
		return nil, err
	}
	inv := idOf(sub.LatestInvoice)
	if inv == "" {
		return nil, errors.New("stripe: the subscription has no invoice to retry")
	}
	var in invoice
	err := g.call(ctx, http.MethodPost, "/v1/invoices/"+url.PathEscape(inv)+"/pay", url.Values{}, &in)
	var apiErr *payment.APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusPaymentRequired {
		return nil, fmt.Errorf("stripe: retry declined: %s", apiErr.Message)
	}
	if err != nil {
		return nil, err
	}
	if in.Status != "paid" {
		return nil, fmt.Errorf("stripe: the invoice is %s", in.Status)
	}
	return &payment.SubscriptionUpdate{Status: payment.SubActive,
		Installment: &payment.Installment{GatewayRef: in.ID, Amount: in.AmountPaid, Status: payment.StatusPaid}}, nil
}
