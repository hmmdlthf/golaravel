// Package payment is a universal payment gateway layer. The core knows
// nothing about any one provider: each gateway (Stripe, PayHere, OnePay,
// Tabby, Tamara, or your own) is a plugin that registers a Driver. The
// Manager runs the shared lifecycle on top of those drivers:
//
//   - it creates a Payment record and asks the gateway for a checkout;
//   - it sends the customer to the gateway (a redirect, an auto-posted form,
//     or a client secret for an on-page SDK);
//   - it verifies webhooks and return visits through the driver and moves the
//     payment along a fixed state machine;
//   - it captures and refunds through drivers that support them;
//   - it fires events ("payment.paid", "payment.failed", ...) so the
//     application can fulfil orders.
//
// Gateway settings (API keys and the like) are stored per configured gateway,
// with fields the driver marks secret encrypted at rest.
package payment

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Status is where a payment is in its lifecycle.
type Status string

const (
	// StatusPending: created, the customer has not finished at the gateway.
	StatusPending Status = "pending"
	// StatusAuthorized: the gateway holds the funds; they still need capturing
	// (buy-now-pay-later gateways and manual-capture cards).
	StatusAuthorized Status = "authorized"
	// StatusPaid: the money is captured.
	StatusPaid Status = "paid"
	// StatusPartiallyRefunded: paid, with part of it refunded.
	StatusPartiallyRefunded Status = "partially_refunded"
	// StatusRefunded: the whole amount was refunded.
	StatusRefunded Status = "refunded"
	// StatusFailed: the gateway declined or the payment errored.
	StatusFailed Status = "failed"
	// StatusCanceled: the customer or the merchant canceled it.
	StatusCanceled Status = "canceled"
	// StatusExpired: the checkout or the authorization ran out of time.
	StatusExpired Status = "expired"
)

// Statuses lists every status in lifecycle order.
func Statuses() []Status {
	return []Status{StatusPending, StatusAuthorized, StatusPaid, StatusPartiallyRefunded,
		StatusRefunded, StatusFailed, StatusCanceled, StatusExpired}
}

// Label is the status in words, for admin screens.
func (s Status) Label() string {
	switch s {
	case StatusPartiallyRefunded:
		return "Partially refunded"
	case "":
		return ""
	}
	return strings.ToUpper(string(s[:1])) + string(s[1:])
}

// Successful reports whether money was taken (even if later refunded).
func (s Status) Successful() bool {
	return s == StatusPaid || s == StatusPartiallyRefunded || s == StatusRefunded
}

// Final reports whether nothing more is expected to happen to the payment.
func (s Status) Final() bool {
	return s == StatusRefunded || s == StatusFailed || s == StatusCanceled || s == StatusExpired
}

// CanTransition reports whether a payment may move from one status to
// another. Gateways often deliver notifications late, twice or out of order;
// the state machine keeps a stale "pending" from undoing a "paid".
func CanTransition(from, to Status) bool {
	if from == to {
		return from == StatusPartiallyRefunded // a second partial refund
	}
	switch from {
	case StatusPending:
		return true
	case StatusAuthorized:
		return to == StatusPaid || to == StatusCanceled || to == StatusExpired || to == StatusFailed
	case StatusPaid:
		return to == StatusPartiallyRefunded || to == StatusRefunded
	case StatusPartiallyRefunded:
		return to == StatusRefunded
	case StatusFailed, StatusCanceled, StatusExpired:
		// a gateway may still confirm a payment the customer abandoned and
		// then completed in another tab
		return to == StatusAuthorized || to == StatusPaid
	}
	return false
}

// Payment is one attempt to collect money through a gateway.
type Payment struct {
	ID          string `json:"id"`
	Reference   string `json:"reference"` // the application's order or invoice number
	GatewayID   string `json:"gateway_id"`
	Driver      string `json:"driver"`
	Status      Status `json:"status"`
	Amount      int64  `json:"amount"` // minor units (cents, fils, ...)
	Currency    string `json:"currency"`
	Refunded    int64  `json:"refunded"`
	Description string `json:"description,omitempty"`
	// GatewayRef is the gateway's own id for the payment (a Stripe Checkout
	// Session, a Tabby payment, a Tamara order...).
	GatewayRef    string            `json:"gateway_ref,omitempty"`
	CustomerName  string            `json:"customer_name,omitempty"`
	CustomerEmail string            `json:"customer_email,omitempty"`
	CustomerPhone string            `json:"customer_phone,omitempty"`
	SuccessURL    string            `json:"success_url,omitempty"`
	CancelURL     string            `json:"cancel_url,omitempty"`
	Action        *Action           `json:"action,omitempty"`
	FailureReason string            `json:"failure_reason,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	// Data is driver-owned state (for example a capture id) kept between calls.
	Data      map[string]string `json:"data,omitempty"`
	Mode      Mode              `json:"mode"`
	Version   int               `json:"version"`
	PaidAt    *time.Time        `json:"paid_at,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// Money formats the amount, for example "SAR 1,250.00".
func (p *Payment) Money() string { return FormatMoney(p.Amount, p.Currency) }

// Refundable is what is left to refund.
func (p *Payment) Refundable() int64 {
	if !p.Status.Successful() {
		return 0
	}
	return p.Amount - p.Refunded
}

// Customer is who pays. Buy-now-pay-later gateways need the phone and
// address; card gateways mostly the email.
type Customer struct {
	FirstName string   `json:"first_name,omitempty"`
	LastName  string   `json:"last_name,omitempty"`
	Email     string   `json:"email,omitempty"`
	Phone     string   `json:"phone,omitempty"`
	Address   *Address `json:"address,omitempty"`
}

// Name is the full name.
func (c Customer) Name() string { return strings.TrimSpace(c.FirstName + " " + c.LastName) }

// Address is a postal address.
type Address struct {
	Line1      string `json:"line1,omitempty"`
	Line2      string `json:"line2,omitempty"`
	City       string `json:"city,omitempty"`
	Region     string `json:"region,omitempty"`
	PostalCode string `json:"postal_code,omitempty"`
	Country    string `json:"country,omitempty"` // ISO 3166-1 alpha-2
}

// Item is one order line. UnitAmount is in minor units.
type Item struct {
	SKU        string `json:"sku,omitempty"`
	Name       string `json:"name"`
	Quantity   int    `json:"quantity"`
	UnitAmount int64  `json:"unit_amount"`
	Category   string `json:"category,omitempty"`
	ImageURL   string `json:"image_url,omitempty"`
}

// CheckoutRequest is what the application asks for.
type CheckoutRequest struct {
	Reference      string // order or invoice number, required
	Amount         int64  // minor units, required
	Currency       string // ISO 4217, required
	Description    string
	Customer       Customer
	Items          []Item
	ShippingAmount int64
	TaxAmount      int64
	DiscountAmount int64
	Locale         string // "en", "ar", ...
	// SuccessURL and CancelURL are where the customer lands in the
	// application after the gateway. The manager adds ?payment=<id>.
	SuccessURL string
	CancelURL  string
	Metadata   map[string]string
}

func (r *CheckoutRequest) validate() error {
	switch {
	case strings.TrimSpace(r.Reference) == "":
		return errors.New("payment: checkout needs a Reference")
	case r.Amount <= 0:
		return errors.New("payment: checkout needs a positive Amount")
	case len(r.Currency) != 3:
		return errors.New("payment: checkout needs a 3-letter Currency")
	}
	r.Currency = strings.ToUpper(r.Currency)
	return nil
}

// GatewayCheckout is what a driver receives: the application's request plus
// the URLs the gateway must send the customer and its notifications to.
type GatewayCheckout struct {
	CheckoutRequest
	PaymentID string
	// ReturnURL brings the customer back after a successful payment; the
	// manager confirms the result with the gateway before trusting it.
	ReturnURL string
	// CancelReturnURL brings the customer back after canceling.
	CancelReturnURL string
	// FailureReturnURL brings the customer back after a decline.
	FailureReturnURL string
	// NotifyURL receives the gateway's server-to-server notifications.
	NotifyURL string
}

// ActionType says how the customer is handed to the gateway.
type ActionType string

const (
	// ActionRedirect: send the browser to URL.
	ActionRedirect ActionType = "redirect"
	// ActionPostForm: auto-submit Fields to URL (PayHere and other classic
	// hosted pages).
	ActionPostForm ActionType = "post_form"
	// ActionClientSecret: hand ClientSecret to the gateway's on-page SDK.
	ActionClientSecret ActionType = "client_secret"
	// ActionNone: nothing to do (already paid, or rejected up front).
	ActionNone ActionType = "none"
)

// Action is the next step for the customer.
type Action struct {
	Type         ActionType        `json:"type"`
	URL          string            `json:"url,omitempty"`
	Fields       map[string]string `json:"fields,omitempty"`
	ClientSecret string            `json:"client_secret,omitempty"`
}

// Session is the driver's answer to a checkout.
type Session struct {
	GatewayRef string
	Action     Action
	// Status is usually pending; a driver may report failed when the
	// gateway rejects the customer up front (for example Tabby's pre-scoring).
	Status        Status
	FailureReason string
	Data          map[string]string
}

// Update is a verified fact about a payment, from a webhook, a return visit
// or a status lookup.
type Update struct {
	// PaymentID is the manager's payment id when the gateway echoes it back;
	// otherwise the manager finds the payment by GatewayRef.
	PaymentID  string
	GatewayRef string
	Status     Status
	// Refunded is the total refunded so far, in minor units, when known.
	Refunded int64
	// EventID deduplicates repeated notifications, when the gateway has one.
	EventID       string
	Event         string // the gateway's event name, for the log
	FailureReason string
	Data          map[string]string
	// Ignore means the notification was genuine but says nothing new (a
	// gateway event the manager does not track); it is acknowledged.
	Ignore bool
}

// RefundResult is a driver's answer to a refund.
type RefundResult struct {
	RefundRef string
	// Refunded is the total refunded so far when the gateway reports it;
	// zero means "add the requested amount".
	Refunded int64
}

// Mode is test (sandbox) or live.
type Mode string

const (
	ModeTest Mode = "test"
	ModeLive Mode = "live"
)

// Log is one entry in a payment's history.
type Log struct {
	ID        string    `json:"id"`
	PaymentID string    `json:"payment_id"`
	Event     string    `json:"event"` // "created", "webhook", "return", "refund", ...
	From      Status    `json:"from,omitempty"`
	To        Status    `json:"to,omitempty"`
	Message   string    `json:"message,omitempty"`
	EventID   string    `json:"event_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// NewID returns a random id with a prefix, like "pay_3f9a...".
func NewID(prefix string) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

// ---- money ----------------------------------------------------------------

// Exponent is the number of decimal places a currency uses.
func Exponent(currency string) int {
	switch strings.ToUpper(currency) {
	case "BIF", "CLP", "DJF", "GNF", "ISK", "JPY", "KMF", "KRW", "PYG", "RWF",
		"UGX", "UYI", "VND", "VUV", "XAF", "XOF", "XPF":
		return 0
	case "BHD", "IQD", "JOD", "KWD", "LYD", "OMR", "TND":
		return 3
	}
	return 2
}

// Decimal writes minor units as a plain decimal string ("1250.00"), the
// form most gateway APIs want.
func Decimal(amount int64, currency string) string {
	exp := Exponent(currency)
	neg := amount < 0
	if neg {
		amount = -amount
	}
	s := strconv.FormatInt(amount, 10)
	if exp > 0 {
		for len(s) <= exp {
			s = "0" + s
		}
		s = s[:len(s)-exp] + "." + s[len(s)-exp:]
	}
	if neg {
		s = "-" + s
	}
	return s
}

// ParseDecimal reads a decimal string ("1250.5") into minor units.
func ParseDecimal(s, currency string) (int64, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	exp := Exponent(currency)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}
	if len(frac) > exp {
		// gateways sometimes send 1250.000 for a 2-decimal currency
		if strings.Trim(frac[exp:], "0") != "" {
			return 0, fmt.Errorf("payment: %q has more than %d decimals", s, exp)
		}
		frac = frac[:exp]
	}
	for len(frac) < exp {
		frac += "0"
	}
	n, err := strconv.ParseInt(whole+frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("payment: bad amount %q", s)
	}
	if neg {
		n = -n
	}
	return n, nil
}

// FormatMoney writes an amount for people: "SAR 1,250.00".
func FormatMoney(amount int64, currency string) string {
	d := Decimal(amount, currency)
	neg := strings.HasPrefix(d, "-")
	d = strings.TrimPrefix(d, "-")
	whole, frac, hasFrac := strings.Cut(d, ".")
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	out := b.String()
	if hasFrac {
		out += "." + frac
	}
	if neg {
		out = "-" + out
	}
	return strings.ToUpper(currency) + " " + out
}
