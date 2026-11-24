package payment

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	// ErrNotFound: no such payment or gateway.
	ErrNotFound = errors.New("payment: not found")
	// ErrVersionConflict: someone else updated the payment first.
	ErrVersionConflict = errors.New("payment: the payment changed meanwhile")
	// ErrInvalidSignature: a webhook failed verification.
	ErrInvalidSignature = errors.New("payment: webhook signature is invalid")
	// ErrNotSupported: the driver cannot do that.
	ErrNotSupported = errors.New("payment: the gateway does not support this")
	// ErrGatewayDisabled: the gateway is switched off.
	ErrGatewayDisabled = errors.New("payment: the gateway is disabled")
)

// GatewayConfig is one configured gateway: a driver plus its settings. An
// application can configure the same driver twice (say, Stripe US and
// Stripe EU) under different ids.
type GatewayConfig struct {
	ID      string `json:"id"` // slug used in webhook URLs, "stripe"
	Driver  string `json:"driver"`
	Name    string `json:"name"` // shown to customers, "Card (Stripe)"
	Enabled bool   `json:"enabled"`
	Mode    Mode   `json:"mode"`
	// AutoCapture captures authorized payments straight away.
	AutoCapture bool `json:"auto_capture"`
	// Settings hold the driver's fields. Secret fields are encrypted by the
	// manager before they reach the store.
	Settings  map[string]string `json:"settings"`
	Sort      int               `json:"sort"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

func (g *GatewayConfig) clone() *GatewayConfig {
	c := *g
	c.Settings = cloneMap(g.Settings)
	return &c
}

// PaymentQuery filters ListPayments.
type PaymentQuery struct {
	Search    string // id, reference, gateway ref, customer email or name
	Status    Status
	GatewayID string
	Reference string
	Mode      Mode
	// SubscriptionID lists a subscription's renewals.
	SubscriptionID string
	Sort           string // created_at (default), amount, status
	Desc           bool
	Page           int // from 1
	PerPage        int
}

// Store persists gateways, payments and their history.
type Store interface {
	SaveGateway(ctx context.Context, g *GatewayConfig) error
	DeleteGateway(ctx context.Context, id string) error
	Gateway(ctx context.Context, id string) (*GatewayConfig, error)
	Gateways(ctx context.Context) ([]*GatewayConfig, error)

	CreatePayment(ctx context.Context, p *Payment) error
	// UpdatePayment saves p if its Version still matches the stored one and
	// then increments it; otherwise it returns ErrVersionConflict.
	UpdatePayment(ctx context.Context, p *Payment) error
	Payment(ctx context.Context, id string) (*Payment, error)
	PaymentByGatewayRef(ctx context.Context, gatewayID, ref string) (*Payment, error)
	Payments(ctx context.Context, q PaymentQuery) ([]*Payment, int, error)

	AddLog(ctx context.Context, l *Log) error
	Logs(ctx context.Context, paymentID string) ([]*Log, error)
	// SeenEvent reports whether a gateway event id was already processed
	// for the payment (or method, or subscription: logs are keyed by any id).
	SeenEvent(ctx context.Context, paymentID, eventID string) (bool, error)

	CreateMethod(ctx context.Context, m *PaymentMethod) error
	// UpdateMethod saves m if its Version matches, then increments it.
	UpdateMethod(ctx context.Context, m *PaymentMethod) error
	Method(ctx context.Context, id string) (*PaymentMethod, error)
	Methods(ctx context.Context, q MethodQuery) ([]*PaymentMethod, int, error)

	CreateSubscription(ctx context.Context, s *Subscription) error
	// UpdateSubscription saves s if its Version matches, then increments it.
	UpdateSubscription(ctx context.Context, s *Subscription) error
	Subscription(ctx context.Context, id string) (*Subscription, error)
	SubscriptionByGatewayRef(ctx context.Context, gatewayID, ref string) (*Subscription, error)
	Subscriptions(ctx context.Context, q SubscriptionQuery) ([]*Subscription, int, error)
	// DueSubscriptions lists manager-billed subscriptions (active or past
	// due) whose next billing time has come.
	DueSubscriptions(ctx context.Context, now time.Time, limit int) ([]*Subscription, error)
}

// MemoryStore keeps everything in memory: tests, demos, single-process apps
// that do not need payments to survive a restart.
type MemoryStore struct {
	mu       sync.RWMutex
	gateways map[string]*GatewayConfig
	payments map[string]*Payment
	logs     map[string][]*Log
	methods  map[string]*PaymentMethod
	subs     map[string]*Subscription
}

// NewMemoryStore creates an empty store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{gateways: map[string]*GatewayConfig{}, payments: map[string]*Payment{}, logs: map[string][]*Log{},
		methods: map[string]*PaymentMethod{}, subs: map[string]*Subscription{}}
}

func (s *MemoryStore) SaveGateway(_ context.Context, g *GatewayConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gateways[g.ID] = g.clone()
	return nil
}

func (s *MemoryStore) DeleteGateway(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.gateways[id]; !ok {
		return ErrNotFound
	}
	delete(s.gateways, id)
	return nil
}

func (s *MemoryStore) Gateway(_ context.Context, id string) (*GatewayConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	g, ok := s.gateways[id]
	if !ok {
		return nil, ErrNotFound
	}
	return g.clone(), nil
}

func (s *MemoryStore) Gateways(_ context.Context) ([]*GatewayConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*GatewayConfig, 0, len(s.gateways))
	for _, g := range s.gateways {
		out = append(out, g.clone())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sort != out[j].Sort {
			return out[i].Sort < out[j].Sort
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (s *MemoryStore) CreatePayment(_ context.Context, p *Payment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.payments[p.ID]; ok {
		return errors.New("payment: duplicate id")
	}
	s.payments[p.ID] = clonePayment(p)
	return nil
}

func (s *MemoryStore) UpdatePayment(_ context.Context, p *Payment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.payments[p.ID]
	if !ok {
		return ErrNotFound
	}
	if cur.Version != p.Version {
		return ErrVersionConflict
	}
	p.Version++
	s.payments[p.ID] = clonePayment(p)
	return nil
}

func (s *MemoryStore) Payment(_ context.Context, id string) (*Payment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.payments[id]
	if !ok {
		return nil, ErrNotFound
	}
	return clonePayment(p), nil
}

func (s *MemoryStore) PaymentByGatewayRef(_ context.Context, gatewayID, ref string) (*Payment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.payments {
		if p.GatewayID == gatewayID && p.GatewayRef == ref && ref != "" {
			return clonePayment(p), nil
		}
	}
	return nil, ErrNotFound
}

func (s *MemoryStore) Payments(_ context.Context, q PaymentQuery) ([]*Payment, int, error) {
	s.mu.RLock()
	var all []*Payment
	search := strings.ToLower(strings.TrimSpace(q.Search))
	for _, p := range s.payments {
		if q.Status != "" && p.Status != q.Status ||
			q.GatewayID != "" && p.GatewayID != q.GatewayID ||
			q.Reference != "" && p.Reference != q.Reference ||
			q.SubscriptionID != "" && p.SubscriptionID != q.SubscriptionID ||
			q.Mode != "" && p.Mode != q.Mode {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(strings.Join([]string{
			p.ID, p.Reference, p.GatewayRef, p.CustomerEmail, p.CustomerName}, " ")), search) {
			continue
		}
		all = append(all, clonePayment(p))
	}
	s.mu.RUnlock()
	desc := q.Desc || q.Sort == "" // newest first by default
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if desc {
			a, b = b, a
		}
		switch q.Sort {
		case "amount":
			return a.Amount < b.Amount
		case "status":
			return a.Status < b.Status
		}
		if a.CreatedAt.Equal(b.CreatedAt) {
			return a.ID < b.ID
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
	return paginate(all, q.Page, q.PerPage), len(all), nil
}

func paginate[T any](rows []T, page, perPage int) []T {
	if perPage <= 0 {
		return rows
	}
	if page < 1 {
		page = 1
	}
	start := (page - 1) * perPage
	if start >= len(rows) {
		return nil
	}
	end := start + perPage
	if end > len(rows) {
		end = len(rows)
	}
	return rows[start:end]
}

func (s *MemoryStore) AddLog(_ context.Context, l *Log) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := *l
	s.logs[l.PaymentID] = append(s.logs[l.PaymentID], &c)
	return nil
}

func (s *MemoryStore) Logs(_ context.Context, paymentID string) ([]*Log, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Log, 0, len(s.logs[paymentID]))
	for _, l := range s.logs[paymentID] {
		c := *l
		out = append(out, &c)
	}
	return out, nil
}

func (s *MemoryStore) SeenEvent(_ context.Context, paymentID, eventID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, l := range s.logs[paymentID] {
		if l.EventID == eventID {
			return true, nil
		}
	}
	return false, nil
}

func clonePayment(p *Payment) *Payment {
	c := *p
	c.Metadata = cloneMap(p.Metadata)
	c.Data = cloneMap(p.Data)
	if p.Action != nil {
		a := *p.Action
		a.Fields = cloneMap(p.Action.Fields)
		c.Action = &a
	}
	if p.PaidAt != nil {
		t := *p.PaidAt
		c.PaidAt = &t
	}
	return &c
}

func cloneMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	c := make(map[string]string, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}
