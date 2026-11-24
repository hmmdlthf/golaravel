package payment

import (
	"context"
	"errors"
	"strings"
	"time"
)

// ---- saved payment methods (preapproval) -----------------------------------

// MethodStatus is where a saved payment method is.
type MethodStatus string

const (
	// MethodPending: the customer has not finished saving the card.
	MethodPending MethodStatus = "pending"
	// MethodActive: the method can be charged.
	MethodActive MethodStatus = "active"
	// MethodFailed: the gateway refused to save it.
	MethodFailed MethodStatus = "failed"
	// MethodRevoked: removed; it can no longer be charged.
	MethodRevoked MethodStatus = "revoked"
)

// Label is the status in words.
func (s MethodStatus) Label() string { return label(string(s)) }

// PaymentMethod is a card (or wallet) the customer authorized the merchant
// to charge later: PayHere's preapproval, a Stripe saved payment method, ...
type PaymentMethod struct {
	ID        string       `json:"id"`
	GatewayID string       `json:"gateway_id"`
	Driver    string       `json:"driver"`
	Status    MethodStatus `json:"status"`
	// CustomerRef is the application's customer id, for finding a
	// customer's saved methods.
	CustomerRef   string `json:"customer_ref,omitempty"`
	CustomerName  string `json:"customer_name,omitempty"`
	CustomerEmail string `json:"customer_email,omitempty"`
	CustomerPhone string `json:"customer_phone,omitempty"`
	Currency      string `json:"currency,omitempty"`
	Description   string `json:"description,omitempty"`
	// GatewayRef is the gateway's id for the method (a Stripe payment method).
	GatewayRef string `json:"gateway_ref,omitempty"`
	// Token is what the gateway charges (PayHere's customer_token). The
	// manager stores it encrypted; drivers get it decrypted.
	Token         string            `json:"token,omitempty"`
	Brand         string            `json:"brand,omitempty"`
	Last4         string            `json:"last4,omitempty"`
	Expiry        string            `json:"expiry,omitempty"` // "MM/YY"
	SuccessURL    string            `json:"success_url,omitempty"`
	CancelURL     string            `json:"cancel_url,omitempty"`
	Action        *Action           `json:"action,omitempty"`
	FailureReason string            `json:"failure_reason,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	Data          map[string]string `json:"data,omitempty"`
	Mode          Mode              `json:"mode"`
	Version       int               `json:"version"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

// Display describes the method for people: "Visa •••• 4242 (12/27)".
func (m *PaymentMethod) Display() string {
	s := strings.TrimSpace(m.Brand + " •••• " + m.Last4)
	if m.Last4 == "" {
		s = Pick(m.Brand, "Saved method")
	}
	if m.Expiry != "" {
		s += " (" + m.Expiry + ")"
	}
	return s
}

// PreapprovalRequest asks a customer to save a payment method.
type PreapprovalRequest struct {
	CustomerRef string
	Customer    Customer
	// Currency the method will be charged in (some gateways tie it).
	Currency    string
	Description string
	SuccessURL  string
	CancelURL   string
	Metadata    map[string]string
}

// GatewayPreapproval is what a driver receives to save a method.
type GatewayPreapproval struct {
	PreapprovalRequest
	MethodID         string
	ReturnURL        string
	CancelReturnURL  string
	FailureReturnURL string
	NotifyURL        string
}

// MethodUpdate is a verified fact about a saved method.
type MethodUpdate struct {
	MethodID      string
	GatewayRef    string
	Token         string
	Status        MethodStatus
	Brand         string
	Last4         string
	Expiry        string
	FailureReason string
	Data          map[string]string
}

// ChargeRequest charges a saved method without the customer present.
type ChargeRequest struct {
	Reference   string // order or invoice number, required
	Amount      int64  // minor units, required
	Currency    string // defaults to the method's currency
	Description string
	Items       []Item
	Metadata    map[string]string
}

// Preapprover saves payment methods for later charges.
type Preapprover interface {
	Preapprove(ctx context.Context, req *GatewayPreapproval) (*Session, error)
}

// Charger charges a saved method (a merchant-initiated payment). A decline
// is an Update with StatusFailed, not an error; errors mean the outcome is
// unknown.
type Charger interface {
	Charge(ctx context.Context, m *PaymentMethod, p *Payment) (*Update, error)
}

// MethodRevoker removes a saved method at the gateway.
type MethodRevoker interface {
	RevokeMethod(ctx context.Context, m *PaymentMethod) error
}

// ---- subscriptions ----------------------------------------------------------

// Interval is a billing period unit.
type Interval string

const (
	Day   Interval = "day"
	Week  Interval = "week"
	Month Interval = "month"
	Year  Interval = "year"
)

// Next moves t forward by count intervals.
func (i Interval) Next(t time.Time, count int) time.Time {
	if count < 1 {
		count = 1
	}
	switch i {
	case Day:
		return t.AddDate(0, 0, count)
	case Week:
		return t.AddDate(0, 0, 7*count)
	case Year:
		return t.AddDate(count, 0, 0)
	}
	return t.AddDate(0, count, 0)
}

// SubscriptionStatus is where a subscription is.
type SubscriptionStatus string

const (
	// SubPending: the customer has not finished signing up.
	SubPending SubscriptionStatus = "pending"
	// SubActive: billing on schedule.
	SubActive SubscriptionStatus = "active"
	// SubPastDue: the last renewal failed; it is being retried.
	SubPastDue SubscriptionStatus = "past_due"
	// SubCanceled: stopped by the customer or the merchant.
	SubCanceled SubscriptionStatus = "canceled"
	// SubCompleted: all its billing cycles are paid.
	SubCompleted SubscriptionStatus = "completed"
	// SubFailed: sign-up failed, or renewals failed too often.
	SubFailed SubscriptionStatus = "failed"
)

// SubscriptionStatuses lists the statuses in lifecycle order.
func SubscriptionStatuses() []SubscriptionStatus {
	return []SubscriptionStatus{SubPending, SubActive, SubPastDue, SubCanceled, SubCompleted, SubFailed}
}

// Label is the status in words.
func (s SubscriptionStatus) Label() string { return label(string(s)) }

// Final reports whether nothing more will be billed.
func (s SubscriptionStatus) Final() bool {
	return s == SubCanceled || s == SubCompleted || s == SubFailed
}

// CanTransitionSub reports whether a subscription may move between statuses.
func CanTransitionSub(from, to SubscriptionStatus) bool {
	if from == to {
		return false
	}
	switch from {
	case SubPending:
		return true
	case SubActive:
		return to == SubPastDue || to == SubCanceled || to == SubCompleted || to == SubFailed
	case SubPastDue:
		return to == SubActive || to == SubCanceled || to == SubFailed || to == SubCompleted
	case SubFailed:
		return to == SubActive // a gateway retry that went through
	}
	return false
}

func label(s string) string {
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "_", " ")
	return strings.ToUpper(s[:1]) + s[1:]
}

// Billing says who runs the schedule.
type Billing string

const (
	// BillingGateway: the gateway charges each period (PayHere recurring,
	// Stripe and PayPal subscriptions) and reports renewals by webhook.
	BillingGateway Billing = "gateway"
	// BillingManager: the manager charges a saved method each period
	// (RunDueSubscriptions from the scheduler). Works on any gateway that
	// can charge saved methods.
	BillingManager Billing = "manager"
)

// Subscription is a recurring charge.
type Subscription struct {
	ID            string             `json:"id"`
	Reference     string             `json:"reference"` // the application's plan or account reference
	GatewayID     string             `json:"gateway_id"`
	Driver        string             `json:"driver"`
	Status        SubscriptionStatus `json:"status"`
	Billing       Billing            `json:"billing"`
	Amount        int64              `json:"amount"` // per period, minor units
	Currency      string             `json:"currency"`
	Interval      Interval           `json:"interval"`
	IntervalCount int                `json:"interval_count"`
	// Cycles is how many periods to bill; 0 means until canceled.
	Cycles int `json:"cycles"`
	// CyclesPaid counts the periods paid so far.
	CyclesPaid      int               `json:"cycles_paid"`
	FailedAttempts  int               `json:"failed_attempts"`
	Description     string            `json:"description,omitempty"`
	GatewayRef      string            `json:"gateway_ref,omitempty"`
	PaymentMethodID string            `json:"payment_method_id,omitempty"`
	CustomerRef     string            `json:"customer_ref,omitempty"`
	CustomerName    string            `json:"customer_name,omitempty"`
	CustomerEmail   string            `json:"customer_email,omitempty"`
	CustomerPhone   string            `json:"customer_phone,omitempty"`
	SuccessURL      string            `json:"success_url,omitempty"`
	CancelURL       string            `json:"cancel_url,omitempty"`
	Action          *Action           `json:"action,omitempty"`
	FailureReason   string            `json:"failure_reason,omitempty"`
	NextBillingAt   *time.Time        `json:"next_billing_at,omitempty"`
	CanceledAt      *time.Time        `json:"canceled_at,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	Data            map[string]string `json:"data,omitempty"`
	Mode            Mode              `json:"mode"`
	Version         int               `json:"version"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

// Money formats the amount per period, for example "LKR 1,500.00".
func (s *Subscription) Money() string { return FormatMoney(s.Amount, s.Currency) }

// Schedule describes the period: "every month", "every 3 months, 12 times".
func (s *Subscription) Schedule() string {
	n := s.IntervalCount
	if n < 1 {
		n = 1
	}
	out := "every " + string(s.Interval)
	if n > 1 {
		out = "every " + itoa(n) + " " + string(s.Interval) + "s"
	}
	if s.Cycles > 0 {
		out += ", " + itoa(s.Cycles) + " times"
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// SubscriptionRequest starts a subscription.
type SubscriptionRequest struct {
	Reference     string // required
	Amount        int64  // per period, minor units, required
	Currency      string // required
	Interval      Interval
	IntervalCount int // default 1
	Cycles        int // 0 = until canceled
	Description   string
	Customer      Customer
	CustomerRef   string
	Items         []Item
	// PaymentMethodID bills a saved method on the manager's schedule
	// (BillingManager) instead of a gateway subscription. The first period
	// is charged straight away unless StartAt is in the future.
	PaymentMethodID string
	StartAt         time.Time
	SuccessURL      string
	CancelURL       string
	Metadata        map[string]string
}

func (r *SubscriptionRequest) validate() error {
	switch {
	case strings.TrimSpace(r.Reference) == "":
		return errors.New("payment: subscription needs a Reference")
	case r.Amount <= 0:
		return errors.New("payment: subscription needs a positive Amount")
	case r.PaymentMethodID == "" && len(r.Currency) != 3:
		return errors.New("payment: subscription needs a 3-letter Currency")
	}
	switch r.Interval {
	case "":
		r.Interval = Month
	case Day, Week, Month, Year:
	default:
		return errors.New("payment: interval must be day, week, month or year")
	}
	if r.IntervalCount < 1 {
		r.IntervalCount = 1
	}
	if r.Cycles < 0 {
		r.Cycles = 0
	}
	r.Currency = strings.ToUpper(r.Currency)
	return nil
}

// GatewaySubscription is what a driver receives to start a subscription.
type GatewaySubscription struct {
	SubscriptionRequest
	SubscriptionID   string
	ReturnURL        string
	CancelReturnURL  string
	FailureReturnURL string
	NotifyURL        string
}

// Installment is one billing period's payment reported by a gateway.
type Installment struct {
	GatewayRef    string // the gateway's id for this period's payment
	Amount        int64  // 0 means the subscription amount
	Status        Status // paid or failed
	FailureReason string
}

// SubscriptionUpdate is a verified fact about a subscription.
type SubscriptionUpdate struct {
	SubscriptionID string
	GatewayRef     string
	Status         SubscriptionStatus
	NextBillingAt  *time.Time
	FailureReason  string
	Data           map[string]string
	// Installment, when set, records a period's payment (paid or failed).
	Installment *Installment
}

// Subscriber starts gateway-run subscriptions.
type Subscriber interface {
	Subscribe(ctx context.Context, req *GatewaySubscription) (*Session, error)
}

// SubscriptionCanceler stops a gateway-run subscription.
type SubscriptionCanceler interface {
	CancelSubscription(ctx context.Context, s *Subscription) (*SubscriptionUpdate, error)
}

// SubscriptionRetrier retries a failed renewal at the gateway.
type SubscriptionRetrier interface {
	RetrySubscription(ctx context.Context, s *Subscription) (*SubscriptionUpdate, error)
}

// SubscriptionFetcher reads a gateway-run subscription.
type SubscriptionFetcher interface {
	FetchSubscription(ctx context.Context, s *Subscription) (*SubscriptionUpdate, error)
}

// MethodQuery filters saved methods.
type MethodQuery struct {
	Search      string
	Status      MethodStatus
	GatewayID   string
	CustomerRef string
	Page        int
	PerPage     int
}

// SubscriptionQuery filters subscriptions.
type SubscriptionQuery struct {
	Search      string
	Status      SubscriptionStatus
	GatewayID   string
	Reference   string
	CustomerRef string
	Billing     Billing
	Page        int
	PerPage     int
}

// ID prefixes tell which record an id names.
const (
	PaymentPrefix      = "pay_"
	MethodPrefix       = "pm_"
	SubscriptionPrefix = "sub_"
)

// Kind says what an id names: "payment", "method", "subscription" or "".
func Kind(id string) string {
	switch {
	case strings.HasPrefix(id, PaymentPrefix):
		return "payment"
	case strings.HasPrefix(id, MethodPrefix):
		return "method"
	case strings.HasPrefix(id, SubscriptionPrefix):
		return "subscription"
	}
	return ""
}
