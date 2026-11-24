# Payment Gateways (pkg/payment)

`pkg/payment` is one payment API for any gateway. The core runs the parts every gateway shares:

- creating a payment
- sending the customer to the gateway
- verifying the result
- refunds and captures
- the status history

Each gateway is a **driver plugin** that only translates to and from its provider's API.

Built-in drivers:

| Driver | Region | Checkout | Webhook verification | Refunds | Capture |
|---|---|---|---|---|---|
| `stripe` | Global | Stripe Checkout (redirect) | `Stripe-Signature` HMAC-SHA256, 5 min tolerance | Full and partial | Manual-capture mode |
| `payhere` | Sri Lanka | Hosted form (auto-posted) | `md5sig` | Full and partial, needs a business app | — |
| `onepay` | Sri Lanka | Payment link (redirect) | Confirmed against the status API | — | — |
| `tabby` | UAE, KSA, Kuwait | Hosted checkout (redirect) | Shared header, then the payment is read back from the API | Full and partial | Yes |
| `tamara` | GCC | Hosted checkout (redirect) | `tamaraToken` HS256 JWT, then the order is read back | Full and partial | Yes, and the order is authorised automatically |
| `test` | — | Local "Pay / Decline / Cancel" page | HMAC | Yes | Optional |

> **OnePay:** OnePay doesn't publish its API reference. The driver follows the v3 integration that OnePay gives merchants, and both endpoint paths are settings. Check them against a sandbox account before going live. Callbacks are never trusted on their own: the driver reads the transaction back from OnePay's status endpoint and refuses the callback if it can't.

---

## Setup

```go
import (
    "database/sql"

    "github.com/golaravel/golaravel/pkg/database"
    "github.com/golaravel/golaravel/pkg/middleware"
    "github.com/golaravel/golaravel/pkg/payment"
    "github.com/golaravel/golaravel/pkg/payment/gateways"
    "github.com/golaravel/golaravel/pkg/session"
)

store, _ := payment.NewSQLStore(db, database.SQLite) // or payment.NewMemoryStore()
_ = store.Migrate(ctx)

mgr := payment.NewManager(payment.Options{
    BaseURL:   "https://shop.example.com",               // public URL: gateways call back here
    Store:     store,
    Encrypter: session.NewEncrypter(os.Getenv("APP_KEY")), // secret settings are encrypted at rest
})

app.Use(middleware.CSRF("/payments/webhooks/*")) // gateways post webhooks without a CSRF token
app.RegisterProvider(gateways.Provider(mgr))     // installs the drivers and mounts the routes
```

The provider mounts these routes:

| Route | Purpose |
|---|---|
| `GET /payments/{payment}/pay` | Sends the customer to the gateway (redirect or auto-posted form) |
| `GET\|POST /payments/{payment}/return` | The customer comes back. The result is confirmed with the gateway before it counts. |
| `POST /payments/webhooks/{gateway}` | Gateway notifications. Register this URL in the gateway's dashboard. |
| `GET /payments/test-gateway` | The `test` driver's checkout page |

## Configuring gateways

A configured gateway is a driver plus its settings. You can configure the same driver more than once, for example `stripe-us` and `stripe-eu`. Admin panels such as gofilament render the settings form from the driver's `Fields`.

```go
err := mgr.SaveGateway(ctx, &payment.GatewayConfig{
    ID:      "tabby",           // used in the webhook URL: /payments/webhooks/tabby
    Driver:  "tabby",
    Name:    "Pay in 4 with Tabby",
    Enabled: true,
    Mode:    payment.ModeTest,  // or payment.ModeLive
    AutoCapture: true,          // capture as soon as the payment is authorized
    Settings: map[string]string{
        "secret_key": "sk_test_…", "merchant_code": "sa", "region": "sa", "webhook_secret": "…",
    },
})
```

- Settings are checked against the driver's schema: required fields, select options and the mode/key match.
- Secret fields are encrypted, and they're shown back as `payment.MaskedValue`. Saving the mask, or a blank secret field, keeps the stored value.
- `mgr.TestGateway(ctx, id)` checks the credentials when the driver supports it.
- `mgr.Options(ctx, "SAR")` lists the enabled gateways that accept a currency, for your checkout page's payment method picker.

## Taking a payment

```go
p, err := mgr.Checkout(ctx, "stripe", payment.CheckoutRequest{
    Reference:  order.Number,       // your order or invoice number
    Amount:     order.TotalCents,   // minor units: cents, fils, …
    Currency:   "USD",
    Customer:   payment.Customer{FirstName: "Sam", LastName: "Lee", Email: "sam@example.com", Phone: "+15550001"},
    Items:      []payment.Item{{SKU: "MUG", Name: "Mug", Quantity: 2, UnitAmount: 1250}},
    SuccessURL: "https://shop.example.com/orders/" + order.Number,
    CancelURL:  "https://shop.example.com/cart",
})
if err != nil { /* p may still exist, with status failed and the reason */ }
return c.Redirect(http.StatusSeeOther, mgr.PayURL(p))
```

The customer comes back to `SuccessURL?payment=<id>` or `CancelURL?payment=<id>`. On the success page, read the payment and show "paid" or "processing". Don't treat the visit itself as proof of payment, because some gateways confirm a few seconds later by webhook.

### Lifecycle

```
pending ──► authorized ──► paid ──► partially_refunded ──► refunded
   │             │
   └► failed / canceled / expired   (a late success can still move these to paid)
```

The state machine ignores stale or out-of-order notifications, so a late `pending` never undoes `paid`. Repeated webhook deliveries are recognised by event id and applied once.

### Fulfilling orders

```go
mgr.OnStatusChange(func(ctx context.Context, e payment.StatusEvent) {
    if e.Payment.Status == payment.StatusPaid {
        orders.MarkPaid(ctx, e.Payment.Reference)
    }
})
// or the dispatcher: mgr.Events().Listen("payment.paid", func(v any) error { … })
```

### Refunds, captures and voids

```go
mgr.Refund(ctx, paymentID, 2500, "damaged item") // 0 refunds whatever is left
mgr.Capture(ctx, paymentID, 0)                   // authorized → paid
mgr.Cancel(ctx, paymentID)                       // void an authorization, or abandon a pending payment
mgr.Sync(ctx, paymentID)                         // ask the gateway for the current status
```

## Writing a gateway plugin

A driver is a `*payment.Driver` with a settings schema and a constructor. The gateway it builds needs only two methods. Optional interfaces add more:

- `StatusFetcher`
- `Refunder`
- `Capturer`
- `Canceler`
- `ReturnHandler`
- `ConnectionTester`

```go
package mypay

func Driver() *payment.Driver {
    return &payment.Driver{
        Name: "mypay", Label: "MyPay", Description: "Cards in Atlantis.",
        Currencies: []string{"ATL"},
        Features:   payment.Features{Refunds: true},
        Fields: []payment.Field{
            {Name: "api_key", Label: "API key", Type: payment.FieldSecret, Required: true},
        },
        New: func(cfg payment.Config) (payment.Gateway, error) {
            return &gateway{key: cfg.Get("api_key"), http: cfg.Client(), sandbox: cfg.Test()}, nil
        },
    }
}

type gateway struct{ /* … */ }

func (g *gateway) Checkout(ctx context.Context, req *payment.GatewayCheckout) (*payment.Session, error) {
    // create the payment at the provider; req.ReturnURL / CancelReturnURL / NotifyURL are ready to pass on
    return &payment.Session{GatewayRef: id, Action: payment.Action{Type: payment.ActionRedirect, URL: hostedURL}}, nil
}

func (g *gateway) ParseWebhook(ctx context.Context, r *http.Request, body []byte) (*payment.Update, error) {
    // verify first: return payment.ErrInvalidSignature for anything you cannot authenticate
    return &payment.Update{GatewayRef: id, Status: payment.StatusPaid, EventID: eventID}, nil
}
```

Install it with `mgr.RegisterDriver(mypay.Driver())`. You can also call `payment.RegisterDriver` from the package's `init`, so that a blank import installs it.

`payment.Call` is a small JSON/form client with error decoding. Use `cfg.Client()` for outbound calls, so tests and proxies can stand in for the provider. Point the driver at an `httptest` server through a base-URL setting. The built-in drivers' tests in `pkg/payment/gateways` work this way.

## Admin panel

gofilament's `payments` package gives you, on top of this manager:

- a **Payment Gateways** resource: pick a driver, fill in its fields, enable it, switch between test and live, and test the connection
- a **Payments** resource: filters, the status history, refund, capture, void and sync actions
