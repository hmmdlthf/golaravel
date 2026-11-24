package payment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golaravel/golaravel/pkg/events"
)

// Encrypter protects secret settings at rest. session.Encrypter fits.
type Encrypter interface {
	Encrypt(plaintext []byte) (string, error)
	Decrypt(ciphertext string) ([]byte, error)
}

// MaskedValue stands in for a secret when settings are shown; saving it
// back (or leaving the field blank) keeps the stored secret.
const MaskedValue = "••••••••"

const encPrefix = "enc:"

// Events fired on the manager's dispatcher. Every status change fires
// EventUpdated and "payment." + the new status ("payment.paid", ...).
const (
	EventUpdated = "payment.updated"
	EventCreated = "payment.created"
)

// StatusEvent is the payload of payment events.
type StatusEvent struct {
	Payment *Payment
	From    Status
	Source  string // "checkout", "webhook", "return", "sync", "refund", "capture", "cancel"
}

// Options configure a Manager.
type Options struct {
	// Store persists gateways and payments; defaults to a MemoryStore.
	Store Store
	// Registry holds the drivers; defaults to DefaultRegistry().
	Registry *Registry
	// BaseURL is the application's public URL ("https://shop.example.com"),
	// used to build the return and webhook URLs handed to gateways.
	BaseURL string
	// Prefix is where Routes mounts the payment endpoints; default "/payments".
	Prefix string
	// Encrypter encrypts secret settings. Without one they are stored as
	// typed, which is fine only for tests.
	Encrypter Encrypter
	// HTTPClient is used by drivers for outbound calls.
	HTTPClient *http.Client
	// Events receives payment events; defaults to a private dispatcher.
	Events *events.Dispatcher
	// Logger reports background problems (auto-capture failures and such).
	Logger *slog.Logger
}

// Manager runs payments through configured gateways.
type Manager struct {
	store    Store
	registry *Registry
	baseURL  string
	prefix   string
	enc      Encrypter
	client   *http.Client
	events   *events.Dispatcher
	log      *slog.Logger

	mu    sync.Mutex
	cache map[string]cachedGateway
	locks map[string]*sync.Mutex
	hooks []func(ctx context.Context, e StatusEvent)
}

type cachedGateway struct {
	stamp time.Time
	gw    Gateway
}

// NewManager creates a manager.
func NewManager(opts Options) *Manager {
	m := &Manager{
		store:    opts.Store,
		registry: opts.Registry,
		baseURL:  strings.TrimRight(opts.BaseURL, "/"),
		prefix:   "/" + strings.Trim(opts.Prefix, "/"),
		enc:      opts.Encrypter,
		client:   opts.HTTPClient,
		events:   opts.Events,
		log:      opts.Logger,
		cache:    map[string]cachedGateway{},
		locks:    map[string]*sync.Mutex{},
	}
	if m.store == nil {
		m.store = NewMemoryStore()
	}
	if m.registry == nil {
		m.registry = DefaultRegistry()
	}
	if opts.Prefix == "" {
		m.prefix = "/payments"
	}
	if m.client == nil {
		m.client = &http.Client{Timeout: 30 * time.Second}
	}
	if m.events == nil {
		m.events = events.NewDispatcher()
	}
	if m.log == nil {
		m.log = slog.Default()
	}
	return m
}

// Store returns the store.
func (m *Manager) Store() Store { return m.store }

// Registry returns the driver registry.
func (m *Manager) Registry() *Registry { return m.registry }

// Prefix returns the route prefix ("/payments").
func (m *Manager) Prefix() string { return m.prefix }

// Events returns the dispatcher payment events go to.
func (m *Manager) Events() *events.Dispatcher { return m.events }

// RegisterDriver installs a driver in this manager's registry.
func (m *Manager) RegisterDriver(d *Driver) { m.registry.Register(d) }

// Drivers lists the installed drivers.
func (m *Manager) Drivers() []*Driver { return m.registry.Drivers() }

// OnStatusChange runs fn after every status change, in the request that
// caused it. Use it (or the events) to fulfil orders.
func (m *Manager) OnStatusChange(fn func(ctx context.Context, e StatusEvent)) {
	m.mu.Lock()
	m.hooks = append(m.hooks, fn)
	m.mu.Unlock()
}

// ---- gateways ---------------------------------------------------------------

var gatewayID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// SaveGateway creates or updates a configured gateway. Secret settings left
// blank (or sent back as MaskedValue) keep their stored value.
func (m *Manager) SaveGateway(ctx context.Context, g *GatewayConfig) error {
	if !gatewayID.MatchString(g.ID) {
		return fmt.Errorf("payment: gateway id %q must be lowercase letters, digits, - or _", g.ID)
	}
	d, ok := m.registry.Driver(g.Driver)
	if !ok {
		return fmt.Errorf("payment: no %q driver is installed", g.Driver)
	}
	if g.Mode == "" {
		g.Mode = ModeTest
	}
	if g.Mode != ModeTest && g.Mode != ModeLive {
		return fmt.Errorf("payment: mode must be test or live")
	}
	if g.Name == "" {
		g.Name = d.Label
	}
	settings := map[string]string{}
	for _, f := range d.Fields { // only known fields are kept
		if v, ok := g.Settings[f.Name]; ok {
			settings[f.Name] = strings.TrimSpace(v)
		}
	}
	old, err := m.store.Gateway(ctx, g.ID)
	switch {
	case errors.Is(err, ErrNotFound):
		old = nil
		g.CreatedAt = time.Now()
	case err != nil:
		return err
	default:
		g.CreatedAt = old.CreatedAt
		if old.Driver != g.Driver {
			old = nil // a different driver's secrets do not carry over
		}
	}
	plain := map[string]string{}
	for k, v := range settings {
		plain[k] = v
	}
	for _, f := range d.Fields {
		if !f.Secret() {
			continue
		}
		if v := plain[f.Name]; v == "" || v == MaskedValue {
			plain[f.Name] = ""
			if old != nil && old.Settings[f.Name] != "" {
				dec, err := m.decrypt(old.Settings[f.Name])
				if err != nil {
					return err
				}
				plain[f.Name] = dec
			}
		}
	}
	if err := d.Validate(plain); err != nil {
		return err
	}
	if _, err := d.New(Config{GatewayID: g.ID, Mode: g.Mode, Settings: plain, HTTPClient: m.client}); err != nil {
		return err
	}
	stored := map[string]string{}
	for k, v := range plain {
		if f, ok := d.Field(k); ok && f.Secret() && v != "" {
			enc, err := m.encrypt(v)
			if err != nil {
				return err
			}
			v = enc
		}
		stored[k] = v
	}
	g.Settings = stored
	g.UpdatedAt = time.Now()
	if err := m.store.SaveGateway(ctx, g); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.cache, g.ID)
	m.mu.Unlock()
	g.Settings = m.mask(d, stored)
	return nil
}

// DeleteGateway removes a configured gateway. Its payments stay.
func (m *Manager) DeleteGateway(ctx context.Context, id string) error {
	m.mu.Lock()
	delete(m.cache, id)
	m.mu.Unlock()
	return m.store.DeleteGateway(ctx, id)
}

// GatewayConfig returns a configured gateway with secrets masked.
func (m *Manager) GatewayConfig(ctx context.Context, id string) (*GatewayConfig, error) {
	g, err := m.store.Gateway(ctx, id)
	if err != nil {
		return nil, err
	}
	if d, ok := m.registry.Driver(g.Driver); ok {
		g.Settings = m.mask(d, g.Settings)
	}
	return g, nil
}

// GatewayConfigs lists configured gateways with secrets masked.
func (m *Manager) GatewayConfigs(ctx context.Context) ([]*GatewayConfig, error) {
	gs, err := m.store.Gateways(ctx)
	if err != nil {
		return nil, err
	}
	for _, g := range gs {
		if d, ok := m.registry.Driver(g.Driver); ok {
			g.Settings = m.mask(d, g.Settings)
		}
	}
	return gs, nil
}

// Option is a gateway a customer can choose at checkout.
type Option struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Driver      string `json:"driver"`
	Logo        string `json:"logo,omitempty"`
	Installment bool   `json:"installments"`
}

// Options lists the enabled gateways that accept a currency, for a
// checkout page's payment method picker.
func (m *Manager) Options(ctx context.Context, currency string) ([]Option, error) {
	gs, err := m.store.Gateways(ctx)
	if err != nil {
		return nil, err
	}
	var out []Option
	for _, g := range gs {
		d, ok := m.registry.Driver(g.Driver)
		if !ok || !g.Enabled || (currency != "" && !d.SupportsCurrency(currency)) {
			continue
		}
		out = append(out, Option{ID: g.ID, Name: g.Name, Driver: d.Name, Logo: d.Logo, Installment: d.Features.Installments})
	}
	return out, nil
}

// TestGateway checks a gateway's credentials when its driver can.
func (m *Manager) TestGateway(ctx context.Context, id string) error {
	gw, _, _, err := m.gateway(ctx, id, false)
	if err != nil {
		return err
	}
	t, ok := gw.(ConnectionTester)
	if !ok {
		return ErrNotSupported
	}
	return t.TestConnection(ctx)
}

func (m *Manager) mask(d *Driver, settings map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range settings {
		if f, ok := d.Field(k); ok && f.Secret() {
			if v != "" {
				v = MaskedValue
			}
		}
		out[k] = v
	}
	return out
}

func (m *Manager) encrypt(v string) (string, error) {
	if m.enc == nil {
		return v, nil
	}
	s, err := m.enc.Encrypt([]byte(v))
	if err != nil {
		return "", err
	}
	return encPrefix + s, nil
}

func (m *Manager) decrypt(v string) (string, error) {
	if !strings.HasPrefix(v, encPrefix) {
		return v, nil
	}
	if m.enc == nil {
		return "", errors.New("payment: gateway secrets are encrypted but the manager has no Encrypter")
	}
	b, err := m.enc.Decrypt(strings.TrimPrefix(v, encPrefix))
	if err != nil {
		return "", fmt.Errorf("payment: cannot decrypt gateway secret (was the app key changed?): %w", err)
	}
	return string(b), nil
}

// gateway builds (or reuses) the driver instance for a configured gateway.
func (m *Manager) gateway(ctx context.Context, id string, mustBeEnabled bool) (Gateway, *GatewayConfig, *Driver, error) {
	cfg, err := m.store.Gateway(ctx, id)
	if err != nil {
		return nil, nil, nil, err
	}
	if mustBeEnabled && !cfg.Enabled {
		return nil, nil, nil, ErrGatewayDisabled
	}
	d, ok := m.registry.Driver(cfg.Driver)
	if !ok {
		return nil, nil, nil, fmt.Errorf("payment: gateway %q uses the %q driver, which is not installed", id, cfg.Driver)
	}
	m.mu.Lock()
	c, hit := m.cache[id]
	m.mu.Unlock()
	if hit && c.stamp.Equal(cfg.UpdatedAt) {
		return c.gw, cfg, d, nil
	}
	settings := map[string]string{}
	for k, v := range cfg.Settings {
		if settings[k], err = m.decrypt(v); err != nil {
			return nil, nil, nil, err
		}
	}
	gw, err := d.New(Config{GatewayID: id, Mode: cfg.Mode, Settings: settings, HTTPClient: m.client})
	if err != nil {
		return nil, nil, nil, err
	}
	m.mu.Lock()
	m.cache[id] = cachedGateway{stamp: cfg.UpdatedAt, gw: gw}
	m.mu.Unlock()
	return gw, cfg, d, nil
}

// ---- payments ---------------------------------------------------------------

// Payment returns one payment.
func (m *Manager) Payment(ctx context.Context, id string) (*Payment, error) {
	return m.store.Payment(ctx, id)
}

// Payments lists payments.
func (m *Manager) Payments(ctx context.Context, q PaymentQuery) ([]*Payment, int, error) {
	return m.store.Payments(ctx, q)
}

// Logs returns a payment's history, oldest first.
func (m *Manager) Logs(ctx context.Context, id string) ([]*Log, error) {
	return m.store.Logs(ctx, id)
}

// URL builds an absolute URL under the payment routes.
func (m *Manager) URL(path string, query ...url.Values) string {
	u := m.baseURL + m.prefix + "/" + strings.TrimLeft(path, "/")
	if len(query) > 0 && len(query[0]) > 0 {
		u += "?" + query[0].Encode()
	}
	return u
}

// PayURL is where to send the customer to (re)start a pending payment.
func (m *Manager) PayURL(p *Payment) string { return m.URL(p.ID + "/pay") }

// Checkout creates a payment and starts it at the gateway. Send the
// customer to PayURL(payment) (or use payment.Action directly).
func (m *Manager) Checkout(ctx context.Context, gatewayID string, req CheckoutRequest) (*Payment, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	gw, cfg, d, err := m.gateway(ctx, gatewayID, true)
	if err != nil {
		return nil, err
	}
	if !d.SupportsCurrency(req.Currency) {
		return nil, fmt.Errorf("payment: %s does not accept %s", d.Label, req.Currency)
	}
	now := time.Now()
	p := &Payment{
		ID: NewID("pay"), Reference: req.Reference, GatewayID: cfg.ID, Driver: d.Name,
		Status: StatusPending, Amount: req.Amount, Currency: req.Currency,
		Description: req.Description, CustomerName: req.Customer.Name(),
		CustomerEmail: req.Customer.Email, CustomerPhone: req.Customer.Phone,
		SuccessURL: req.SuccessURL, CancelURL: req.CancelURL,
		Metadata: cloneMap(req.Metadata), Data: map[string]string{}, Mode: cfg.Mode,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := m.store.CreatePayment(ctx, p); err != nil {
		return nil, err
	}
	_ = m.store.AddLog(ctx, &Log{ID: logID(), PaymentID: p.ID, Event: "created", To: StatusPending,
		Message: fmt.Sprintf("Checkout for %s via %s", p.Money(), cfg.Name), CreatedAt: now})

	ret := func(result string) string { return m.URL(p.ID+"/return", url.Values{"result": {result}}) }
	sess, err := gw.Checkout(ctx, &GatewayCheckout{
		CheckoutRequest: req, PaymentID: p.ID,
		ReturnURL: ret("success"), CancelReturnURL: ret("cancel"), FailureReturnURL: ret("failure"),
		NotifyURL: m.URL("webhooks/" + cfg.ID),
	})
	if err != nil {
		p.Status, p.FailureReason = StatusFailed, err.Error()
		p.UpdatedAt = time.Now()
		_ = m.store.UpdatePayment(ctx, p)
		_ = m.store.AddLog(ctx, &Log{ID: logID(), PaymentID: p.ID, Event: "checkout", From: StatusPending,
			To: StatusFailed, Message: err.Error(), CreatedAt: time.Now()})
		m.fire(ctx, StatusEvent{Payment: p, From: StatusPending, Source: "checkout"})
		return p, err
	}
	p.GatewayRef = sess.GatewayRef
	act := sess.Action
	p.Action = &act
	for k, v := range sess.Data {
		p.Data[k] = v
	}
	if sess.Status != "" && sess.Status != StatusPending {
		p.Status, p.FailureReason = sess.Status, sess.FailureReason
	}
	p.UpdatedAt = time.Now()
	if err := m.store.UpdatePayment(ctx, p); err != nil {
		return nil, err
	}
	_ = m.events.Dispatch(EventCreated, StatusEvent{Payment: p, Source: "checkout"})
	if p.Status != StatusPending {
		_ = m.store.AddLog(ctx, &Log{ID: logID(), PaymentID: p.ID, Event: "checkout", From: StatusPending,
			To: p.Status, Message: p.FailureReason, CreatedAt: time.Now()})
		m.fire(ctx, StatusEvent{Payment: p, From: StatusPending, Source: "checkout"})
	}
	return p, nil
}

// HandleWebhook verifies and applies a gateway notification.
func (m *Manager) HandleWebhook(ctx context.Context, gatewayID string, r *http.Request, body []byte) (*Payment, error) {
	gw, _, _, err := m.gateway(ctx, gatewayID, false)
	if err != nil {
		return nil, err
	}
	upd, err := gw.ParseWebhook(ctx, r, body)
	if err != nil {
		return nil, err
	}
	if upd == nil || upd.Ignore {
		return nil, nil
	}
	if upd.Method == nil && upd.Subscription == nil {
		p, err := m.find(ctx, gatewayID, upd)
		if err != nil {
			return nil, err
		}
		return m.apply(ctx, p.ID, upd, "webhook")
	}
	if err := m.applyUpdate(ctx, gatewayID, upd, "webhook"); err != nil {
		return nil, err
	}
	if upd.hasPayment() {
		p, _ := m.find(ctx, gatewayID, upd)
		return p, nil
	}
	return nil, nil
}

// HandleReturn runs when the customer comes back from the gateway. The
// result in the URL is only a hint: when the driver can look the payment
// up, the manager asks the gateway. It returns where to send the customer.
func (m *Manager) HandleReturn(ctx context.Context, paymentID, result string, r *http.Request) (*Payment, string, error) {
	p, err := m.store.Payment(ctx, paymentID)
	if err != nil {
		return nil, "", err
	}
	if p.Status == StatusPending || p.Status == StatusAuthorized {
		if gw, _, _, err := m.gateway(ctx, p.GatewayID, false); err == nil {
			if rh, ok := gw.(ReturnHandler); ok {
				if upd, err := rh.ParseReturn(ctx, p, r); err == nil && upd != nil && !upd.Ignore {
					if np, err := m.apply(ctx, p.ID, upd, "return"); err == nil && np != nil {
						p = np
					}
				}
			} else if f, ok := gw.(StatusFetcher); ok {
				if upd, err := f.FetchStatus(ctx, p); err == nil && upd != nil && !upd.Ignore {
					if np, err := m.apply(ctx, p.ID, upd, "return"); err == nil && np != nil {
						p = np
					}
				}
			}
		}
	}
	dest := p.CancelURL
	switch {
	case p.Status.Successful(), p.Status == StatusAuthorized:
		dest = p.SuccessURL
	case p.Status == StatusPending && result == "success":
		dest = p.SuccessURL // not confirmed yet: the app shows "processing"
	}
	if dest == "" {
		dest = p.SuccessURL
	}
	if dest == "" {
		dest = "/"
	}
	return p, withQuery(dest, "payment", p.ID), nil
}

// ReturnHandler verifies a customer's return visit from data the gateway
// signs into the return URL (PayHere and some others).
type ReturnHandler interface {
	ParseReturn(ctx context.Context, p *Payment, r *http.Request) (*Update, error)
}

// Sync asks the gateway for the payment's current status.
func (m *Manager) Sync(ctx context.Context, paymentID string) (*Payment, error) {
	p, err := m.store.Payment(ctx, paymentID)
	if err != nil {
		return nil, err
	}
	gw, _, _, err := m.gateway(ctx, p.GatewayID, false)
	if err != nil {
		return nil, err
	}
	f, ok := gw.(StatusFetcher)
	if !ok {
		return nil, ErrNotSupported
	}
	upd, err := f.FetchStatus(ctx, p)
	if err != nil {
		return nil, err
	}
	if upd == nil || upd.Ignore {
		return p, nil
	}
	return m.apply(ctx, p.ID, upd, "sync")
}

// Refund refunds amount (minor units; 0 means everything left).
func (m *Manager) Refund(ctx context.Context, paymentID string, amount int64, reason string) (*Payment, error) {
	unlock := m.lock(paymentID)
	defer unlock()
	p, err := m.store.Payment(ctx, paymentID)
	if err != nil {
		return nil, err
	}
	left := p.Refundable()
	if left <= 0 {
		return nil, fmt.Errorf("payment: %s has nothing left to refund", p.ID)
	}
	if amount <= 0 {
		amount = left
	}
	if amount > left {
		return nil, fmt.Errorf("payment: at most %s can be refunded", FormatMoney(left, p.Currency))
	}
	gw, _, d, err := m.gateway(ctx, p.GatewayID, false)
	if err != nil {
		return nil, err
	}
	rf, ok := gw.(Refunder)
	if !ok {
		return nil, ErrNotSupported
	}
	if amount < left && !d.Features.PartialRefunds {
		return nil, fmt.Errorf("payment: %s only refunds in full", d.Label)
	}
	res, err := rf.Refund(ctx, p, amount, reason)
	if err != nil {
		_ = m.store.AddLog(ctx, &Log{ID: logID(), PaymentID: p.ID, Event: "refund",
			Message: "Refund failed: " + err.Error(), CreatedAt: time.Now()})
		return nil, err
	}
	total := p.Refunded + amount
	if res != nil && res.Refunded > 0 {
		total = res.Refunded
	}
	upd := &Update{Status: StatusPartiallyRefunded, Refunded: total, Event: "refund"}
	if total >= p.Amount {
		upd.Status = StatusRefunded
	}
	msg := fmt.Sprintf("Refunded %s", FormatMoney(amount, p.Currency))
	if reason != "" {
		msg += ": " + reason
	}
	if res != nil && res.RefundRef != "" {
		msg += " (" + res.RefundRef + ")"
	}
	return m.applyLocked(ctx, p.ID, upd, "refund", msg)
}

// Capture captures an authorized payment (amount 0 = all of it).
func (m *Manager) Capture(ctx context.Context, paymentID string, amount int64) (*Payment, error) {
	unlock := m.lock(paymentID)
	defer unlock()
	p, err := m.store.Payment(ctx, paymentID)
	if err != nil {
		return nil, err
	}
	if p.Status != StatusAuthorized {
		return nil, fmt.Errorf("payment: only authorized payments can be captured (this one is %s)", p.Status)
	}
	gw, _, _, err := m.gateway(ctx, p.GatewayID, false)
	if err != nil {
		return nil, err
	}
	c, ok := gw.(Capturer)
	if !ok {
		return nil, ErrNotSupported
	}
	if amount <= 0 || amount > p.Amount {
		amount = p.Amount
	}
	upd, err := c.Capture(ctx, p, amount)
	if err != nil {
		_ = m.store.AddLog(ctx, &Log{ID: logID(), PaymentID: p.ID, Event: "capture",
			Message: "Capture failed: " + err.Error(), CreatedAt: time.Now()})
		return nil, err
	}
	if upd == nil {
		upd = &Update{Status: StatusPaid}
	}
	return m.applyLocked(ctx, p.ID, upd, "capture", "Captured "+FormatMoney(amount, p.Currency))
}

// Cancel voids an authorized payment, or abandons a pending one locally.
func (m *Manager) Cancel(ctx context.Context, paymentID string) (*Payment, error) {
	unlock := m.lock(paymentID)
	defer unlock()
	p, err := m.store.Payment(ctx, paymentID)
	if err != nil {
		return nil, err
	}
	switch p.Status {
	case StatusPending:
		return m.applyLocked(ctx, p.ID, &Update{Status: StatusCanceled}, "cancel", "Canceled")
	case StatusAuthorized:
	default:
		return nil, fmt.Errorf("payment: a %s payment cannot be canceled", p.Status)
	}
	gw, _, _, err := m.gateway(ctx, p.GatewayID, false)
	if err != nil {
		return nil, err
	}
	c, ok := gw.(Canceler)
	if !ok {
		return nil, ErrNotSupported
	}
	upd, err := c.Cancel(ctx, p)
	if err != nil {
		return nil, err
	}
	if upd == nil {
		upd = &Update{Status: StatusCanceled}
	}
	return m.applyLocked(ctx, p.ID, upd, "cancel", "Authorization voided")
}

func (m *Manager) find(ctx context.Context, gatewayID string, upd *Update) (*Payment, error) {
	if upd.PaymentID != "" {
		p, err := m.store.Payment(ctx, upd.PaymentID)
		if err == nil && p.GatewayID == gatewayID {
			return p, nil
		}
	}
	if upd.GatewayRef != "" {
		return m.store.PaymentByGatewayRef(ctx, gatewayID, upd.GatewayRef)
	}
	return nil, ErrNotFound
}

func (m *Manager) lock(id string) func() {
	m.mu.Lock()
	l, ok := m.locks[id]
	if !ok {
		l = &sync.Mutex{}
		m.locks[id] = l
	}
	m.mu.Unlock()
	l.Lock()
	return l.Unlock
}

func (m *Manager) apply(ctx context.Context, id string, upd *Update, source string) (*Payment, error) {
	unlock := m.lock(id)
	defer unlock()
	return m.applyLocked(ctx, id, upd, source, "")
}

// applyLocked moves a payment to the update's status if the state machine
// allows it, logs it and fires events. It retries on version conflicts
// with other processes.
func (m *Manager) applyLocked(ctx context.Context, id string, upd *Update, source, msg string) (*Payment, error) {
	for attempt := 0; ; attempt++ {
		p, err := m.store.Payment(ctx, id)
		if err != nil {
			return nil, err
		}
		if upd.EventID != "" {
			if seen, _ := m.store.SeenEvent(ctx, id, upd.EventID); seen {
				return p, nil
			}
		}
		from := p.Status
		changed := upd.Status != "" && CanTransition(from, upd.Status)
		if upd.Status == from && from != StatusPartiallyRefunded {
			changed = false
		}
		if upd.GatewayRef != "" && p.GatewayRef == "" {
			p.GatewayRef = upd.GatewayRef
		}
		for k, v := range upd.Data {
			if p.Data == nil {
				p.Data = map[string]string{}
			}
			p.Data[k] = v
		}
		if changed {
			p.Status = upd.Status
			if upd.FailureReason != "" {
				p.FailureReason = upd.FailureReason
			}
			if upd.Status == StatusPaid && p.PaidAt == nil {
				t := time.Now()
				p.PaidAt = &t
			}
		}
		if upd.Refunded > p.Refunded {
			p.Refunded = upd.Refunded
		}
		p.UpdatedAt = time.Now()
		if err := m.store.UpdatePayment(ctx, p); err != nil {
			if errors.Is(err, ErrVersionConflict) && attempt < 5 {
				continue
			}
			return nil, err
		}
		entry := &Log{ID: logID(), PaymentID: p.ID, Event: source, EventID: upd.EventID,
			Message: msg, CreatedAt: time.Now()}
		if entry.Message == "" {
			entry.Message = upd.Event
			if upd.FailureReason != "" {
				entry.Message = strings.TrimSpace(entry.Message + " " + upd.FailureReason)
			}
		}
		if changed {
			entry.From, entry.To = from, p.Status
		} else if upd.Status != "" && upd.Status != from {
			entry.Message = strings.TrimSpace(entry.Message + fmt.Sprintf(" (ignored: %s after %s)", upd.Status, from))
		}
		_ = m.store.AddLog(ctx, entry)
		if changed {
			m.fire(ctx, StatusEvent{Payment: p, From: from, Source: source})
			if p.Status == StatusAuthorized {
				m.autoCapture(ctx, p)
			}
		}
		return p, nil
	}
}

func (m *Manager) autoCapture(ctx context.Context, p *Payment) {
	cfg, err := m.store.Gateway(ctx, p.GatewayID)
	if err != nil || !cfg.AutoCapture {
		return
	}
	go func() {
		// a fresh context: the webhook request may finish first
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		if _, err := m.Capture(c, p.ID, 0); err != nil {
			m.log.Error("payment: auto-capture failed", "payment", p.ID, "error", err)
		}
	}()
}

func (m *Manager) fire(ctx context.Context, e StatusEvent) {
	m.mu.Lock()
	hooks := append([]func(context.Context, StatusEvent){}, m.hooks...)
	m.mu.Unlock()
	for _, h := range hooks {
		h(ctx, e)
	}
	_ = m.events.Dispatch(EventUpdated, e)
	_ = m.events.Dispatch("payment."+string(e.Payment.Status), e)
}

func withQuery(dest, key, value string) string {
	u, err := url.Parse(dest)
	if err != nil {
		return dest
	}
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()
	return u.String()
}

var logSeq atomic.Uint64

// logID sorts by creation, so logs read back in order from any store.
func logID() string {
	return fmt.Sprintf("log_%016x%06x", time.Now().UnixNano(), logSeq.Add(1)&0xffffff)
}
