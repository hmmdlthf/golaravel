package payment

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Events for saved methods and subscriptions. Each status change also fires
// "payment_method.<status>" or "subscription.<status>".
const (
	EventMethodUpdated       = "payment_method.updated"
	EventSubscriptionUpdated = "subscription.updated"
	// EventSubscriptionRenewed fires for every paid billing period.
	EventSubscriptionRenewed = "subscription.renewed"
)

// MethodEvent is the payload of saved method events.
type MethodEvent struct {
	Method *PaymentMethod
	From   MethodStatus
	Source string
}

// SubscriptionEvent is the payload of subscription events.
type SubscriptionEvent struct {
	Subscription *Subscription
	From         SubscriptionStatus
	// Payment is the period's payment, for renewals.
	Payment *Payment
	Source  string
}

// ---- saved methods ----------------------------------------------------------

// SavePaymentMethod asks the customer to save a payment method at a
// gateway. Send them to PayURL(method.ID); the method turns active when the
// gateway confirms.
func (m *Manager) SavePaymentMethod(ctx context.Context, gatewayID string, req PreapprovalRequest) (*PaymentMethod, error) {
	gw, cfg, d, err := m.gateway(ctx, gatewayID, true)
	if err != nil {
		return nil, err
	}
	pa, ok := gw.(Preapprover)
	if !ok {
		return nil, fmt.Errorf("payment: %s cannot save payment methods: %w", d.Label, ErrNotSupported)
	}
	req.Currency = strings.ToUpper(req.Currency)
	if req.Currency != "" && !d.SupportsCurrency(req.Currency) {
		return nil, fmt.Errorf("payment: %s does not accept %s", d.Label, req.Currency)
	}
	now := time.Now()
	pm := &PaymentMethod{ID: NewID("pm"), GatewayID: cfg.ID, Driver: d.Name, Status: MethodPending,
		CustomerRef: req.CustomerRef, CustomerName: req.Customer.Name(), CustomerEmail: req.Customer.Email,
		CustomerPhone: req.Customer.Phone, Currency: req.Currency, Description: req.Description,
		SuccessURL: req.SuccessURL, CancelURL: req.CancelURL, Metadata: cloneMap(req.Metadata),
		Data: map[string]string{}, Mode: cfg.Mode, CreatedAt: now, UpdatedAt: now}
	if err := m.store.CreateMethod(ctx, pm); err != nil {
		return nil, err
	}
	m.logf(ctx, pm.ID, "created", "", "", "Saving a payment method via "+cfg.Name)
	ret := func(result string) string { return m.URL(pm.ID+"/return", url.Values{"result": {result}}) }
	sess, err := pa.Preapprove(ctx, &GatewayPreapproval{PreapprovalRequest: req, MethodID: pm.ID,
		ReturnURL: ret("success"), CancelReturnURL: ret("cancel"), FailureReturnURL: ret("failure"),
		NotifyURL: m.URL("webhooks/" + cfg.ID)})
	if err != nil {
		_, _ = m.applyMethod(ctx, pm.ID, &MethodUpdate{Status: MethodFailed, FailureReason: err.Error()}, "checkout")
		return nil, err
	}
	upd := &MethodUpdate{GatewayRef: sess.GatewayRef, Data: sess.Data}
	if sess.Status == StatusFailed {
		upd.Status, upd.FailureReason = MethodFailed, sess.FailureReason
	}
	act := sess.Action
	if _, err := m.mutateMethod(ctx, pm.ID, func(x *PaymentMethod) { x.Action = &act }); err != nil {
		return nil, err
	}
	return m.applyMethod(ctx, pm.ID, upd, "checkout")
}

// PaymentMethod returns a saved method; its token stays encrypted.
func (m *Manager) PaymentMethod(ctx context.Context, id string) (*PaymentMethod, error) {
	pm, err := m.store.Method(ctx, id)
	if err != nil {
		return nil, err
	}
	pm.Token = ""
	return pm, nil
}

// PaymentMethods lists saved methods (tokens left out).
func (m *Manager) PaymentMethods(ctx context.Context, q MethodQuery) ([]*PaymentMethod, int, error) {
	list, n, err := m.store.Methods(ctx, q)
	for _, pm := range list {
		pm.Token = ""
	}
	return list, n, err
}

// RevokePaymentMethod stops a saved method from being charged, removing it
// at the gateway when the driver can.
func (m *Manager) RevokePaymentMethod(ctx context.Context, id string) (*PaymentMethod, error) {
	pm, err := m.store.Method(ctx, id)
	if err != nil {
		return nil, err
	}
	if pm.Status == MethodRevoked {
		return pm, nil
	}
	if gw, _, _, err := m.gateway(ctx, pm.GatewayID, false); err == nil {
		if r, ok := gw.(MethodRevoker); ok && pm.Status == MethodActive {
			if pm.Token, err = m.decrypt(pm.Token); err != nil {
				return nil, err
			}
			if err := r.RevokeMethod(ctx, pm); err != nil {
				return nil, err
			}
		}
	}
	return m.applyMethod(ctx, id, &MethodUpdate{Status: MethodRevoked}, "revoke")
}

// Charge charges a saved method without the customer: a merchant-initiated
// payment. The payment comes back paid or failed (or pending, for
// gateways that confirm later).
func (m *Manager) Charge(ctx context.Context, methodID string, req ChargeRequest) (*Payment, error) {
	pm, err := m.store.Method(ctx, methodID)
	if err != nil {
		return nil, err
	}
	if pm.Status != MethodActive {
		return nil, fmt.Errorf("payment: the payment method is %s", pm.Status)
	}
	return m.charge(ctx, pm, req, "")
}

func (m *Manager) charge(ctx context.Context, pm *PaymentMethod, req ChargeRequest, subscriptionID string) (*Payment, error) {
	gw, cfg, d, err := m.gateway(ctx, pm.GatewayID, false)
	if err != nil {
		return nil, err
	}
	ch, ok := gw.(Charger)
	if !ok {
		return nil, fmt.Errorf("payment: %s cannot charge saved methods: %w", d.Label, ErrNotSupported)
	}
	if req.Currency == "" {
		req.Currency = pm.Currency
	}
	cr := CheckoutRequest{Reference: req.Reference, Amount: req.Amount, Currency: req.Currency,
		Description: req.Description, Items: req.Items, Metadata: req.Metadata}
	if err := cr.validate(); err != nil {
		return nil, err
	}
	now := time.Now()
	p := &Payment{ID: NewID("pay"), Reference: cr.Reference, GatewayID: cfg.ID, Driver: d.Name, Status: StatusPending,
		Amount: cr.Amount, Currency: cr.Currency, Description: cr.Description, CustomerName: pm.CustomerName,
		CustomerEmail: pm.CustomerEmail, CustomerPhone: pm.CustomerPhone, SubscriptionID: subscriptionID,
		PaymentMethodID: pm.ID, Metadata: cloneMap(cr.Metadata), Data: map[string]string{}, Mode: cfg.Mode,
		CreatedAt: now, UpdatedAt: now}
	if err := m.store.CreatePayment(ctx, p); err != nil {
		return nil, err
	}
	m.logf(ctx, p.ID, "created", "", StatusPending, fmt.Sprintf("Charging %s to %s", p.Money(), pm.Display()))
	if pm.Token, err = m.decrypt(pm.Token); err != nil {
		return nil, err
	}
	upd, err := ch.Charge(ctx, pm, p)
	if err != nil {
		// unknown outcome: leave it pending for a sync or webhook to settle
		m.logf(ctx, p.ID, "charge", "", "", "Charge error: "+err.Error())
		np, _ := m.store.Payment(ctx, p.ID)
		return np, err
	}
	if upd == nil {
		upd = &Update{Ignore: true}
	}
	upd.PaymentID = p.ID
	if upd.Ignore {
		return m.store.Payment(ctx, p.ID)
	}
	return m.apply(ctx, p.ID, upd, "charge")
}

func (m *Manager) mutateMethod(ctx context.Context, id string, fn func(*PaymentMethod)) (*PaymentMethod, error) {
	for attempt := 0; ; attempt++ {
		pm, err := m.store.Method(ctx, id)
		if err != nil {
			return nil, err
		}
		fn(pm)
		pm.UpdatedAt = time.Now()
		if err := m.store.UpdateMethod(ctx, pm); err != nil {
			if errors.Is(err, ErrVersionConflict) && attempt < 5 {
				continue
			}
			return nil, err
		}
		return pm, nil
	}
}

func canMethod(from, to MethodStatus) bool {
	switch from {
	case MethodPending:
		return to != MethodPending
	case MethodActive:
		return to == MethodRevoked
	case MethodFailed:
		return to == MethodActive
	}
	return false
}

func (m *Manager) applyMethod(ctx context.Context, id string, upd *MethodUpdate, source string) (*PaymentMethod, error) {
	unlock := m.lock(id)
	defer unlock()
	var from MethodStatus
	changed := false
	var tokenErr error
	pm, err := m.mutateMethod(ctx, id, func(x *PaymentMethod) {
		from, changed = x.Status, upd.Status != "" && canMethod(x.Status, upd.Status)
		if upd.GatewayRef != "" {
			x.GatewayRef = upd.GatewayRef
		}
		if upd.Token != "" {
			enc, err := m.encrypt(upd.Token)
			if err != nil {
				tokenErr = err
				return
			}
			x.Token = enc
		}
		x.Brand, x.Last4, x.Expiry = Pick(upd.Brand, x.Brand), Pick(upd.Last4, x.Last4), Pick(upd.Expiry, x.Expiry)
		for k, v := range upd.Data {
			if x.Data == nil {
				x.Data = map[string]string{}
			}
			x.Data[k] = v
		}
		if changed {
			x.Status = upd.Status
			x.FailureReason = upd.FailureReason
		}
	})
	if err != nil {
		return nil, err
	}
	if tokenErr != nil {
		return nil, tokenErr
	}
	if changed {
		msg := upd.FailureReason
		if upd.Status == MethodActive {
			msg = "Saved " + pm.Display()
		}
		m.logf(ctx, pm.ID, source, Status(from), Status(pm.Status), msg)
		pm.Token = ""
		e := MethodEvent{Method: pm, From: from, Source: source}
		_ = m.events.Dispatch(EventMethodUpdated, e)
		_ = m.events.Dispatch("payment_method."+string(pm.Status), e)
	}
	pm.Token = ""
	return pm, nil
}

// ---- subscriptions ----------------------------------------------------------

// Subscribe starts a subscription. With req.PaymentMethodID the manager
// bills that saved method each period (run RunDueSubscriptions from the
// scheduler); otherwise the gateway runs it and the customer signs up at
// PayURL(subscription.ID).
func (m *Manager) Subscribe(ctx context.Context, gatewayID string, req SubscriptionRequest) (*Subscription, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	now := time.Now()
	if req.PaymentMethodID != "" {
		return m.subscribeManaged(ctx, req, now)
	}
	gw, cfg, d, err := m.gateway(ctx, gatewayID, true)
	if err != nil {
		return nil, err
	}
	sb, ok := gw.(Subscriber)
	if !ok {
		return nil, fmt.Errorf("payment: %s cannot run subscriptions (save a payment method and pass PaymentMethodID instead): %w", d.Label, ErrNotSupported)
	}
	if !d.SupportsCurrency(req.Currency) {
		return nil, fmt.Errorf("payment: %s does not accept %s", d.Label, req.Currency)
	}
	sub := m.newSubscription(req, cfg.ID, d.Name, cfg.Mode, BillingGateway, now)
	if err := m.store.CreateSubscription(ctx, sub); err != nil {
		return nil, err
	}
	m.logf(ctx, sub.ID, "created", "", Status(SubPending), fmt.Sprintf("%s %s via %s", sub.Money(), sub.Schedule(), cfg.Name))
	ret := func(result string) string { return m.URL(sub.ID+"/return", url.Values{"result": {result}}) }
	sess, err := sb.Subscribe(ctx, &GatewaySubscription{SubscriptionRequest: req, SubscriptionID: sub.ID,
		ReturnURL: ret("success"), CancelReturnURL: ret("cancel"), FailureReturnURL: ret("failure"),
		NotifyURL: m.URL("webhooks/" + cfg.ID)})
	if err != nil {
		_, _ = m.applySubscription(ctx, sub.ID, &SubscriptionUpdate{Status: SubFailed, FailureReason: err.Error()}, "checkout")
		return nil, err
	}
	act := sess.Action
	if _, err := m.mutateSub(ctx, sub.ID, func(x *Subscription) { x.Action = &act }); err != nil {
		return nil, err
	}
	upd := &SubscriptionUpdate{GatewayRef: sess.GatewayRef, Data: sess.Data}
	if sess.Status == StatusFailed {
		upd.Status, upd.FailureReason = SubFailed, sess.FailureReason
	}
	return m.applySubscription(ctx, sub.ID, upd, "checkout")
}

func (m *Manager) newSubscription(req SubscriptionRequest, gatewayID, driver string, mode Mode, billing Billing, now time.Time) *Subscription {
	return &Subscription{ID: NewID("sub"), Reference: req.Reference, GatewayID: gatewayID, Driver: driver,
		Status: SubPending, Billing: billing, Amount: req.Amount, Currency: req.Currency, Interval: req.Interval,
		IntervalCount: req.IntervalCount, Cycles: req.Cycles, Description: req.Description,
		PaymentMethodID: req.PaymentMethodID, CustomerRef: req.CustomerRef, CustomerName: req.Customer.Name(),
		CustomerEmail: req.Customer.Email, CustomerPhone: req.Customer.Phone, SuccessURL: req.SuccessURL,
		CancelURL: req.CancelURL, Metadata: cloneMap(req.Metadata), Data: map[string]string{}, Mode: mode,
		CreatedAt: now, UpdatedAt: now}
}

func (m *Manager) subscribeManaged(ctx context.Context, req SubscriptionRequest, now time.Time) (*Subscription, error) {
	pm, err := m.store.Method(ctx, req.PaymentMethodID)
	if err != nil {
		return nil, err
	}
	if pm.Status != MethodActive {
		return nil, fmt.Errorf("payment: the payment method is %s", pm.Status)
	}
	gw, cfg, d, err := m.gateway(ctx, pm.GatewayID, false)
	if err != nil {
		return nil, err
	}
	if _, ok := gw.(Charger); !ok {
		return nil, fmt.Errorf("payment: %s cannot charge saved methods: %w", d.Label, ErrNotSupported)
	}
	if req.Currency == "" {
		req.Currency = pm.Currency
	}
	if req.CustomerRef == "" {
		req.CustomerRef = pm.CustomerRef
	}
	if req.Customer.Email == "" {
		req.Customer = Customer{FirstName: pm.CustomerName, Email: pm.CustomerEmail, Phone: pm.CustomerPhone}
	}
	sub := m.newSubscription(req, cfg.ID, d.Name, cfg.Mode, BillingManager, now)
	start := now
	if req.StartAt.After(now) {
		start = req.StartAt
	}
	sub.Status, sub.NextBillingAt = SubActive, &start
	if err := m.store.CreateSubscription(ctx, sub); err != nil {
		return nil, err
	}
	m.logf(ctx, sub.ID, "created", "", Status(SubActive),
		fmt.Sprintf("%s %s, billed to %s", sub.Money(), sub.Schedule(), pm.Display()))
	if !start.After(now) {
		if _, err := m.bill(ctx, sub.ID, now, false); err != nil {
			return nil, err
		}
	}
	return m.store.Subscription(ctx, sub.ID)
}

// Subscription returns one subscription.
func (m *Manager) Subscription(ctx context.Context, id string) (*Subscription, error) {
	return m.store.Subscription(ctx, id)
}

// Subscriptions lists subscriptions.
func (m *Manager) Subscriptions(ctx context.Context, q SubscriptionQuery) ([]*Subscription, int, error) {
	return m.store.Subscriptions(ctx, q)
}

// SubscriptionPayments lists a subscription's period payments, newest first.
func (m *Manager) SubscriptionPayments(ctx context.Context, id string, page, perPage int) ([]*Payment, int, error) {
	return m.store.Payments(ctx, PaymentQuery{SubscriptionID: id, Page: page, PerPage: perPage})
}

// CancelSubscription stops a subscription (at the gateway when it runs it).
func (m *Manager) CancelSubscription(ctx context.Context, id string) (*Subscription, error) {
	sub, err := m.store.Subscription(ctx, id)
	if err != nil {
		return nil, err
	}
	if sub.Status.Final() {
		return nil, fmt.Errorf("payment: the subscription is already %s", sub.Status)
	}
	upd := &SubscriptionUpdate{Status: SubCanceled}
	if sub.Billing == BillingGateway && sub.Status != SubPending {
		gw, _, d, err := m.gateway(ctx, sub.GatewayID, false)
		if err != nil {
			return nil, err
		}
		c, ok := gw.(SubscriptionCanceler)
		if !ok {
			return nil, fmt.Errorf("payment: %s cannot cancel subscriptions here; cancel it in the gateway's dashboard: %w", d.Label, ErrNotSupported)
		}
		if u, err := c.CancelSubscription(ctx, sub); err != nil {
			return nil, err
		} else if u != nil {
			upd = u
			if upd.Status == "" {
				upd.Status = SubCanceled
			}
		}
	}
	return m.applySubscription(ctx, id, upd, "cancel")
}

// RetrySubscription retries a failed renewal now.
func (m *Manager) RetrySubscription(ctx context.Context, id string) (*Subscription, error) {
	sub, err := m.store.Subscription(ctx, id)
	if err != nil {
		return nil, err
	}
	if sub.Status != SubPastDue && sub.Status != SubFailed {
		return nil, fmt.Errorf("payment: only past-due subscriptions can be retried (this one is %s)", sub.Status)
	}
	if sub.Billing == BillingManager {
		if sub.Status == SubFailed {
			if _, err := m.mutateSub(ctx, id, func(x *Subscription) { x.Status = SubPastDue }); err != nil {
				return nil, err
			}
		}
		return m.bill(ctx, id, time.Now(), true)
	}
	gw, _, d, err := m.gateway(ctx, sub.GatewayID, false)
	if err != nil {
		return nil, err
	}
	r, ok := gw.(SubscriptionRetrier)
	if !ok {
		return nil, fmt.Errorf("payment: %s retries renewals on its own schedule: %w", d.Label, ErrNotSupported)
	}
	upd, err := r.RetrySubscription(ctx, sub)
	if err != nil {
		return nil, err
	}
	if upd == nil {
		return m.store.Subscription(ctx, id)
	}
	return m.applySubscription(ctx, id, upd, "retry")
}

// SyncSubscription reads a gateway-run subscription back from the gateway.
func (m *Manager) SyncSubscription(ctx context.Context, id string) (*Subscription, error) {
	sub, err := m.store.Subscription(ctx, id)
	if err != nil {
		return nil, err
	}
	gw, _, _, err := m.gateway(ctx, sub.GatewayID, false)
	if err != nil {
		return nil, err
	}
	f, ok := gw.(SubscriptionFetcher)
	if !ok || sub.Billing != BillingGateway {
		return nil, ErrNotSupported
	}
	upd, err := f.FetchSubscription(ctx, sub)
	if err != nil {
		return nil, err
	}
	if upd == nil {
		return sub, nil
	}
	return m.applySubscription(ctx, id, upd, "sync")
}

// RetryPolicy is how manager-billed renewals are retried after a decline.
type RetryPolicy struct {
	// Delays between attempts; after the last one the subscription fails.
	Delays []time.Duration
}

// DefaultRetryPolicy retries after 1, 3 and 5 days.
var DefaultRetryPolicy = RetryPolicy{Delays: []time.Duration{24 * time.Hour, 72 * time.Hour, 120 * time.Hour}}

// RunDueSubscriptions bills every manager-billed subscription whose period
// has come. Schedule it (every few minutes or hourly); it is safe to run
// from several processes because each bill is applied once.
func (m *Manager) RunDueSubscriptions(ctx context.Context) (int, error) {
	now := time.Now()
	due, err := m.store.DueSubscriptions(ctx, now, 200)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, sub := range due {
		if _, err := m.bill(ctx, sub.ID, now, false); err != nil {
			m.log.Error("payment: renewal failed", "subscription", sub.ID, "error", err)
			continue
		}
		n++
	}
	return n, nil
}

// bill charges one period of a manager-billed subscription; force bills
// now whatever the schedule says (a manual retry).
func (m *Manager) bill(ctx context.Context, id string, now time.Time, force bool) (*Subscription, error) {
	unlock := m.lock(id + ":bill")
	defer unlock()
	sub, err := m.store.Subscription(ctx, id)
	if err != nil {
		return nil, err
	}
	if sub.Billing != BillingManager || sub.Status.Final() ||
		!force && (sub.NextBillingAt == nil || sub.NextBillingAt.After(now)) {
		return sub, nil
	}
	pm, err := m.store.Method(ctx, sub.PaymentMethodID)
	if err != nil {
		return nil, err
	}
	period := sub.CyclesPaid + 1
	if pm.Status != MethodActive {
		return m.renewalFailed(ctx, sub, nil, "the saved payment method is "+string(pm.Status), now)
	}
	p, err := m.charge(ctx, pm, ChargeRequest{Reference: fmt.Sprintf("%s-%d", sub.Reference, period), Amount: sub.Amount,
		Currency: sub.Currency, Description: Pick(sub.Description, "Subscription "+sub.Reference) + fmt.Sprintf(" (period %d)", period),
		Metadata: map[string]string{"subscription_id": sub.ID}}, sub.ID)
	if err != nil && p == nil {
		return nil, err
	}
	switch {
	case p != nil && p.Status.Successful():
		return m.renewed(ctx, sub.ID, p, "renewal")
	case p != nil && p.Status == StatusFailed:
		return m.renewalFailed(ctx, sub, p, p.FailureReason, now)
	}
	// pending: a webhook settles it; don't charge again meanwhile
	later := now.Add(time.Hour)
	return m.mutateSub(ctx, id, func(x *Subscription) { x.NextBillingAt = &later })
}

func (m *Manager) renewed(ctx context.Context, id string, p *Payment, source string) (*Subscription, error) {
	var from SubscriptionStatus
	sub, err := m.mutateSub(ctx, id, func(x *Subscription) {
		from = x.Status
		retried := x.FailedAttempts > 0
		x.CyclesPaid++
		x.FailedAttempts, x.FailureReason = 0, ""
		if x.Status == SubPending || x.Status == SubPastDue || x.Status == SubFailed {
			x.Status = SubActive
		}
		if x.Billing == BillingManager {
			base := time.Now()
			if x.NextBillingAt != nil && !retried {
				base = *x.NextBillingAt
			}
			next := x.Interval.Next(base, x.IntervalCount)
			x.NextBillingAt = &next
		}
		if x.Cycles > 0 && x.CyclesPaid >= x.Cycles {
			x.Status, x.NextBillingAt = SubCompleted, nil
		}
	})
	if err != nil {
		return nil, err
	}
	m.logf(ctx, sub.ID, source, Status(from), Status(sub.Status), fmt.Sprintf("Period %d paid: %s (%s)", sub.CyclesPaid, p.Money(), p.ID))
	e := SubscriptionEvent{Subscription: sub, From: from, Payment: p, Source: source}
	_ = m.events.Dispatch(EventSubscriptionRenewed, e)
	if from != sub.Status {
		m.fireSub(e)
	}
	if sub.Status == SubCompleted && sub.Billing == BillingGateway {
		// the last cycle is paid: stop gateways that bill until canceled
		if gw, _, _, err := m.gateway(ctx, sub.GatewayID, false); err == nil {
			if c, ok := gw.(SubscriptionCanceler); ok {
				if _, err := c.CancelSubscription(ctx, sub); err != nil {
					m.logf(ctx, sub.ID, "complete", "", "", "Could not stop it at the gateway: "+err.Error())
				}
			}
		}
	}
	return sub, nil
}

func (m *Manager) renewalFailed(ctx context.Context, sub *Subscription, p *Payment, reason string, now time.Time) (*Subscription, error) {
	var from SubscriptionStatus
	out, err := m.mutateSub(ctx, sub.ID, func(x *Subscription) {
		from = x.Status
		x.FailedAttempts++
		x.FailureReason = reason
		if x.FailedAttempts > len(DefaultRetryPolicy.Delays) {
			x.Status, x.NextBillingAt = SubFailed, nil
			return
		}
		x.Status = SubPastDue
		next := now.Add(DefaultRetryPolicy.Delays[x.FailedAttempts-1])
		x.NextBillingAt = &next
	})
	if err != nil {
		return nil, err
	}
	m.logf(ctx, out.ID, "renewal", Status(from), Status(out.Status),
		fmt.Sprintf("Renewal attempt %d failed: %s", out.FailedAttempts, reason))
	if from != out.Status {
		m.fireSub(SubscriptionEvent{Subscription: out, From: from, Payment: p, Source: "renewal"})
	}
	return out, nil
}

func (m *Manager) mutateSub(ctx context.Context, id string, fn func(*Subscription)) (*Subscription, error) {
	for attempt := 0; ; attempt++ {
		sub, err := m.store.Subscription(ctx, id)
		if err != nil {
			return nil, err
		}
		fn(sub)
		sub.UpdatedAt = time.Now()
		if err := m.store.UpdateSubscription(ctx, sub); err != nil {
			if errors.Is(err, ErrVersionConflict) && attempt < 5 {
				continue
			}
			return nil, err
		}
		return sub, nil
	}
}

// applySubscription applies a gateway fact: a status change, and a period
// payment when the update carries one.
func (m *Manager) applySubscription(ctx context.Context, id string, upd *SubscriptionUpdate, source string) (*Subscription, error) {
	unlock := m.lock(id)
	defer unlock()
	var from SubscriptionStatus
	changed := false
	sub, err := m.mutateSub(ctx, id, func(x *Subscription) {
		from = x.Status
		if upd.GatewayRef != "" {
			x.GatewayRef = upd.GatewayRef
		}
		if upd.NextBillingAt != nil {
			x.NextBillingAt = upd.NextBillingAt
		}
		for k, v := range upd.Data {
			if x.Data == nil {
				x.Data = map[string]string{}
			}
			x.Data[k] = v
		}
		if upd.Status != "" && CanTransitionSub(x.Status, upd.Status) {
			changed = true
			x.Status = upd.Status
			if upd.FailureReason != "" || upd.Status == SubActive {
				x.FailureReason = upd.FailureReason
			}
			if upd.Status == SubCanceled {
				t := time.Now()
				x.CanceledAt, x.NextBillingAt = &t, nil
			}
		}
	})
	if err != nil {
		return nil, err
	}
	if changed {
		m.logf(ctx, sub.ID, source, Status(from), Status(sub.Status), upd.FailureReason)
		m.fireSub(SubscriptionEvent{Subscription: sub, From: from, Source: source})
	}
	if in := upd.Installment; in != nil {
		return m.installment(ctx, sub, in, source)
	}
	return sub, nil
}

// installment records a gateway-billed period's payment once.
func (m *Manager) installment(ctx context.Context, sub *Subscription, in *Installment, source string) (*Subscription, error) {
	if in.GatewayRef != "" {
		if p, err := m.store.PaymentByGatewayRef(ctx, sub.GatewayID, in.GatewayRef); err == nil {
			if p.Status == in.Status || p.Status.Successful() {
				return sub, nil // already recorded
			}
			np, err := m.applyLocked(ctx, p.ID, &Update{Status: in.Status, FailureReason: in.FailureReason}, source, "")
			if err == nil && np.Status.Successful() {
				return m.renewed(ctx, sub.ID, np, source)
			}
			return sub, err
		}
	}
	amount := in.Amount
	if amount <= 0 {
		amount = sub.Amount
	}
	now := time.Now()
	p := &Payment{ID: NewID("pay"), Reference: fmt.Sprintf("%s-%d", sub.Reference, sub.CyclesPaid+1), GatewayID: sub.GatewayID,
		Driver: sub.Driver, Status: StatusPending, Amount: amount, Currency: sub.Currency,
		Description: Pick(sub.Description, "Subscription "+sub.Reference), GatewayRef: in.GatewayRef,
		CustomerName: sub.CustomerName, CustomerEmail: sub.CustomerEmail, CustomerPhone: sub.CustomerPhone,
		SubscriptionID: sub.ID, Data: map[string]string{}, Mode: sub.Mode, CreatedAt: now, UpdatedAt: now}
	if err := m.store.CreatePayment(ctx, p); err != nil {
		return nil, err
	}
	m.logf(ctx, p.ID, "created", "", StatusPending, "Subscription period billed by the gateway")
	np, err := m.applyLocked(ctx, p.ID, &Update{Status: in.Status, FailureReason: in.FailureReason}, source, "")
	if err != nil {
		return nil, err
	}
	if np.Status.Successful() {
		return m.renewed(ctx, sub.ID, np, source)
	}
	if np.Status == StatusFailed {
		return m.mutateSub(ctx, sub.ID, func(x *Subscription) { x.FailedAttempts++; x.FailureReason = in.FailureReason })
	}
	return sub, nil
}

func (m *Manager) fireSub(e SubscriptionEvent) {
	_ = m.events.Dispatch(EventSubscriptionUpdated, e)
	_ = m.events.Dispatch("subscription."+string(e.Subscription.Status), e)
}

func (m *Manager) logf(ctx context.Context, id, event string, from, to Status, msg string) {
	_ = m.store.AddLog(ctx, &Log{ID: logID(), PaymentID: id, Event: event, From: from, To: to, Message: msg, CreatedAt: time.Now()})
}

// ---- returns and webhooks for methods and subscriptions ------------------------

func (m *Manager) handleRecordReturn(ctx context.Context, id, result string, r *http.Request) (string, error) {
	var success, cancel string
	var settled bool
	switch Kind(id) {
	case "method":
		pm, err := m.store.Method(ctx, id)
		if err != nil {
			return "", err
		}
		if pm.Status == MethodPending {
			if gw, _, _, err := m.gateway(ctx, pm.GatewayID, false); err == nil {
				if rh, ok := gw.(RecordReturnHandler); ok {
					if upd, err := rh.ParseRecordReturn(ctx, id, r); err == nil && upd != nil {
						_ = m.applyUpdate(ctx, pm.GatewayID, upd, "return")
					}
				}
			}
			pm, _ = m.store.Method(ctx, id)
		}
		success, cancel, settled = pm.SuccessURL, pm.CancelURL, pm.Status == MethodActive
		if pm.Status == MethodPending && result == "success" {
			settled = true
		}
	case "subscription":
		sub, err := m.store.Subscription(ctx, id)
		if err != nil {
			return "", err
		}
		if sub.Status == SubPending {
			if gw, _, _, err := m.gateway(ctx, sub.GatewayID, false); err == nil {
				if rh, ok := gw.(RecordReturnHandler); ok {
					if upd, err := rh.ParseRecordReturn(ctx, id, r); err == nil && upd != nil {
						_ = m.applyUpdate(ctx, sub.GatewayID, upd, "return")
					}
				}
			}
			sub, _ = m.store.Subscription(ctx, id)
		}
		success, cancel = sub.SuccessURL, sub.CancelURL
		settled = sub.Status == SubActive || sub.Status == SubPending && result == "success"
	default:
		return "", ErrNotFound
	}
	dest := cancel
	if settled {
		dest = success
	}
	dest = Pick(dest, success, "/")
	return withQuery(dest, Kind(id), id), nil
}

// RecordReturnHandler verifies a customer's return from saving a method or
// signing up for a subscription (the payment-side ReturnHandler covers
// payments).
type RecordReturnHandler interface {
	ParseRecordReturn(ctx context.Context, id string, r *http.Request) (*Update, error)
}

// applyUpdate applies every part of a verified update.
func (m *Manager) applyUpdate(ctx context.Context, gatewayID string, upd *Update, source string) error {
	did := false
	if mu := upd.Method; mu != nil && mu.MethodID != "" {
		pm, err := m.store.Method(ctx, mu.MethodID)
		if err != nil || pm.GatewayID != gatewayID {
			return ErrNotFound
		}
		if upd.EventID != "" {
			if seen, _ := m.store.SeenEvent(ctx, pm.ID, upd.EventID); seen {
				return nil
			}
		}
		if _, err := m.applyMethod(ctx, pm.ID, mu, source); err != nil {
			return err
		}
		m.markEvent(ctx, pm.ID, upd.EventID, source)
		did = true
	}
	if su := upd.Subscription; su != nil {
		var sub *Subscription
		var err error
		if su.SubscriptionID != "" {
			sub, err = m.store.Subscription(ctx, su.SubscriptionID)
			if err == nil && sub.GatewayID != gatewayID {
				err = ErrNotFound
			}
		} else {
			sub, err = m.store.SubscriptionByGatewayRef(ctx, gatewayID, su.GatewayRef)
		}
		if err != nil {
			return err
		}
		if upd.EventID != "" {
			if seen, _ := m.store.SeenEvent(ctx, sub.ID, upd.EventID); seen {
				return nil
			}
		}
		if _, err := m.applySubscription(ctx, sub.ID, su, source); err != nil {
			return err
		}
		m.markEvent(ctx, sub.ID, upd.EventID, source)
		did = true
	}
	if upd.hasPayment() {
		p, err := m.find(ctx, gatewayID, upd)
		if err != nil {
			if did {
				return nil
			}
			return err
		}
		_, err = m.apply(ctx, p.ID, upd, source)
		return err
	}
	if !did {
		return ErrNotFound
	}
	return nil
}

func (m *Manager) markEvent(ctx context.Context, id, eventID, source string) {
	if eventID != "" {
		_ = m.store.AddLog(ctx, &Log{ID: logID(), PaymentID: id, Event: source, EventID: eventID,
			Message: "Gateway event " + eventID, CreatedAt: time.Now()})
	}
}
