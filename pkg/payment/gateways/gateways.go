// Package gateways bundles the built-in payment drivers and a service
// provider that wires a payment.Manager into a GoLaravel app.
//
//	mgr := payment.NewManager(payment.Options{BaseURL: "https://shop.example.com", Store: store, Encrypter: enc})
//	app.RegisterProvider(gateways.Provider(mgr))
//
// The provider installs Stripe, PayPal, PayHere, OnePay, Tabby, Tamara and the test
// gateway, puts the manager in the container and mounts the payment routes.
// Third-party drivers are registered the same way: mgr.RegisterDriver(d).
package gateways

import (
	"github.com/golaravel/golaravel/pkg/container"
	"github.com/golaravel/golaravel/pkg/golaravel"
	"github.com/golaravel/golaravel/pkg/payment"
	"github.com/golaravel/golaravel/pkg/payment/onepay"
	"github.com/golaravel/golaravel/pkg/payment/payhere"
	"github.com/golaravel/golaravel/pkg/payment/paypal"
	"github.com/golaravel/golaravel/pkg/payment/stripe"
	"github.com/golaravel/golaravel/pkg/payment/tabby"
	"github.com/golaravel/golaravel/pkg/payment/tamara"
)

// Defaults returns the built-in drivers.
func Defaults() []*payment.Driver {
	return []*payment.Driver{
		stripe.Driver(), paypal.Driver(), payhere.Driver(), onepay.Driver(), tabby.Driver(), tamara.Driver(), payment.TestDriver(),
	}
}

// Register installs the built-in drivers in a registry.
func Register(r *payment.Registry) {
	for _, d := range Defaults() {
		r.Register(d)
	}
}

// PaymentProvider is the service provider.
type PaymentProvider struct {
	Manager *payment.Manager
	// NoDefaults skips installing the built-in drivers.
	NoDefaults bool
	// NoRoutes skips mounting the routes (mount Manager.Routes yourself,
	// for example inside a group with your own middleware).
	NoRoutes bool
}

// Provider creates the service provider for a manager.
func Provider(m *payment.Manager) *PaymentProvider { return &PaymentProvider{Manager: m} }

// Register installs the drivers and binds the manager.
func (p *PaymentProvider) Register(c *container.Container) {
	if !p.NoDefaults {
		Register(p.Manager.Registry())
	}
	container.Instance(c, p.Manager)
}

// Boot mounts the payment routes.
func (p *PaymentProvider) Boot(app *golaravel.Engine) error {
	if !p.NoRoutes {
		p.Manager.Routes(app.Router)
	}
	return nil
}
