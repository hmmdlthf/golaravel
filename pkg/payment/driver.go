package payment

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// Gateway is a configured, ready-to-use connection to one payment provider.
// Drivers build it from a Config. Only Checkout and ParseWebhook are
// required; the optional interfaces below add capabilities.
type Gateway interface {
	// Checkout starts a payment at the gateway.
	Checkout(ctx context.Context, req *GatewayCheckout) (*Session, error)
	// ParseWebhook authenticates a server-to-server notification and says
	// what it means. It must reject anything it cannot verify with
	// ErrInvalidSignature: the manager trusts what it returns.
	ParseWebhook(ctx context.Context, r *http.Request, body []byte) (*Update, error)
}

// StatusFetcher looks a payment up at the gateway. The manager uses it to
// confirm return visits (which anyone can forge) and to resync payments.
type StatusFetcher interface {
	FetchStatus(ctx context.Context, p *Payment) (*Update, error)
}

// Refunder refunds captured payments, in full or in part.
type Refunder interface {
	Refund(ctx context.Context, p *Payment, amount int64, reason string) (*RefundResult, error)
}

// Capturer captures authorized payments (buy-now-pay-later, manual capture).
type Capturer interface {
	Capture(ctx context.Context, p *Payment, amount int64) (*Update, error)
}

// Canceler voids an authorized payment before capture.
type Canceler interface {
	Cancel(ctx context.Context, p *Payment) (*Update, error)
}

// ConnectionTester checks the credentials, for the admin's "Test" button.
type ConnectionTester interface {
	TestConnection(ctx context.Context) error
}

// FieldType is how a driver setting is entered.
type FieldType string

const (
	FieldText     FieldType = "text"
	FieldSecret   FieldType = "secret" // stored encrypted, never shown back
	FieldSelect   FieldType = "select"
	FieldToggle   FieldType = "toggle"
	FieldTextarea FieldType = "textarea"
)

// Field describes one driver setting, so an admin panel can render the
// form without knowing the driver.
type Field struct {
	Name        string            `json:"name"`
	Label       string            `json:"label"`
	Type        FieldType         `json:"type"`
	Required    bool              `json:"required,omitempty"`
	Default     string            `json:"default,omitempty"`
	Help        string            `json:"help,omitempty"`
	Placeholder string            `json:"placeholder,omitempty"`
	Options     map[string]string `json:"options,omitempty"` // FieldSelect: value => label
}

// Secret reports whether the value is stored encrypted and hidden.
func (f Field) Secret() bool { return f.Type == FieldSecret }

// Features is what a driver can do, for admin screens and for the manager.
type Features struct {
	Refunds        bool `json:"refunds"`
	PartialRefunds bool `json:"partial_refunds"`
	Capture        bool `json:"capture"` // payments arrive authorized and need capturing
	Cancel         bool `json:"cancel"`
	StatusLookup   bool `json:"status_lookup"`
	Installments   bool `json:"installments"` // buy now, pay later
}

// Config is what a driver gets to build a Gateway.
type Config struct {
	GatewayID string
	Mode      Mode
	Settings  map[string]string
	// HTTPClient is the client drivers must use for outbound calls, so
	// tests and proxies can stand in for the gateway.
	HTTPClient *http.Client
}

// Get returns a setting, or its default from the driver's schema.
func (c Config) Get(name string) string { return strings.TrimSpace(c.Settings[name]) }

// Test reports test (sandbox) mode.
func (c Config) Test() bool { return c.Mode != ModeLive }

// Client returns the HTTP client, never nil.
func (c Config) Client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

// Driver is a payment gateway plugin. Register it with RegisterDriver (or
// Manager.RegisterDriver) and it can be configured, as many times as
// needed, in the admin panel.
type Driver struct {
	Name        string   // unique key, "stripe"
	Label       string   // "Stripe"
	Description string   // one line for the admin's driver picker
	Website     string   // the provider's site
	Logo        string   // URL or data: URI, optional
	Countries   []string // ISO codes where it works; empty means anywhere
	Currencies  []string // currencies it accepts; empty means any
	Features    Features
	Fields      []Field
	// New builds a gateway from saved settings. It should check required
	// settings and fail early with a clear message.
	New func(cfg Config) (Gateway, error)
}

// Field returns the schema of one setting.
func (d *Driver) Field(name string) (Field, bool) {
	for _, f := range d.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// SupportsCurrency reports whether the driver accepts a currency.
func (d *Driver) SupportsCurrency(currency string) bool {
	if len(d.Currencies) == 0 {
		return true
	}
	for _, c := range d.Currencies {
		if strings.EqualFold(c, currency) {
			return true
		}
	}
	return false
}

// Validate checks settings against the schema and fills defaults.
func (d *Driver) Validate(settings map[string]string) error {
	var missing []string
	for _, f := range d.Fields {
		v := strings.TrimSpace(settings[f.Name])
		if v == "" && f.Default != "" {
			settings[f.Name] = f.Default
			v = f.Default
		}
		if f.Required && v == "" {
			missing = append(missing, f.Label)
		}
		if v != "" && f.Type == FieldSelect && len(f.Options) > 0 {
			if _, ok := f.Options[v]; !ok {
				return fmt.Errorf("payment: %s: %q is not an option", f.Label, v)
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("payment: %s needs %s", d.Label, strings.Join(missing, ", "))
	}
	return nil
}

// Registry holds the installed drivers.
type Registry struct {
	mu      sync.RWMutex
	drivers map[string]*Driver
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry { return &Registry{drivers: map[string]*Driver{}} }

var defaultRegistry = NewRegistry()

// DefaultRegistry is the process-wide registry RegisterDriver adds to.
func DefaultRegistry() *Registry { return defaultRegistry }

// RegisterDriver installs a driver in the default registry. Driver packages
// may call it from init, so a blank import is enough to install them.
func RegisterDriver(d *Driver) { defaultRegistry.Register(d) }

// Register installs or replaces a driver.
func (r *Registry) Register(d *Driver) {
	if d == nil || d.Name == "" || d.New == nil {
		panic("payment: a driver needs a Name and a New func")
	}
	if d.Label == "" {
		d.Label = d.Name
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.drivers[d.Name] = d
}

// Driver looks a driver up by name.
func (r *Registry) Driver(name string) (*Driver, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.drivers[name]
	return d, ok
}

// Drivers lists the installed drivers by label.
func (r *Registry) Drivers() []*Driver {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Driver, 0, len(r.drivers))
	for _, d := range r.drivers {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}
