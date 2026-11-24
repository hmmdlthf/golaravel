# Payment Gateways (pkg/payment)

`pkg/payment` is one payment API for any gateway, covering one-off payments, saved cards and subscriptions. The core runs the parts every gateway shares:

- creating a payment
- sending the customer to the gateway
- verifying the result
- refunds and captures
- the status history

Each gateway is a **driver plugin** that only translates to and from its provider's API.

Built-in drivers:

| Driver | Region | Checkout | Webhook verification | Refunds | Capture | Saved cards + charging | Subscriptions |
|---|---|---|---|---|---|---|---|
| `stripe` | Global | Stripe Checkout (redirect) | `Stripe-Signature` HMAC-SHA256, 5 min tolerance | Full and partial | Manual-capture mode | ✅ SetupIntent, off-session charges | ✅ Stripe Billing |
| `paypal` | Global | PayPal Checkout (Orders v2), captured on return | PayPal's verify-webhook-signature API | Full and partial | Automatic | — | ✅ PayPal Billing |
| `payhere` | Sri Lanka | Hosted form (auto-posted) | `md5sig` | Full and partial, needs a business app | — | ✅ Preapproval + Charging API | ✅ Recurring + Subscription Manager |
| `onepay` | Sri Lanka | Payment link (redirect) | Confirmed against the status API | — | — | — | — |
| `tabby` | UAE, KSA, Kuwait | Hosted checkout (redirect) | Shared header, then the payment is read back | Full and partial | Yes | — | — |
| `tamara` | GCC | Hosted checkout (redirect) | `tamaraToken` HS256 JWT, then the order is read back | Full and partial | Yes, and the order is authorised automatically | — | — |
| `test` | — | Local "Pay / Decline / Cancel" page | HMAC | Yes | Optional | ✅ Charges ending in .13 decline | ✅ |

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

## Saved cards, charging and subscriptions

### Saving a card (preapproval) and charging it later

```go
pm, _ := mgr.SavePaymentMethod(ctx, "payhere", payment.PreapprovalRequest{
    CustomerRef: user.ID, Customer: payment.Customer{FirstName: "Nimal", Email: "nimal@example.com"},
    Currency: "LKR", SuccessURL: "https://shop.example.com/account/cards",
})
return c.Redirect(http.StatusSeeOther, mgr.PayURL(&payment.Payment{ID: pm.ID})) // the gateway's save-card page

// later, without the customer: a merchant-initiated charge
p, err := mgr.Charge(ctx, pm.ID, payment.ChargeRequest{Reference: "INV-1042", Amount: 250000})
// p.Status is paid or failed; a decline is a failed payment, not an error
```

- The gateway's token for the card is encrypted at rest. `PaymentMethod` and `PaymentMethods` never return it.
- `mgr.PaymentMethods(ctx, payment.MethodQuery{CustomerRef: user.ID})` lists a customer's cards.
- `mgr.RevokePaymentMethod(ctx, id)` removes a card, at the gateway too where the driver supports it.

### Subscriptions

There are two ways to run a subscription:

| | Who charges each period | Works with |
|---|---|---|
| **Gateway-billed** (`BillingGateway`) | The gateway (PayHere Recurring, Stripe Billing, PayPal Billing), which reports each renewal by webhook | Drivers with `Subscriptions` |
| **Manager-billed** (`BillingManager`) | The manager charges a saved card on schedule, with retries | Any driver with `Charging` |

```go
// gateway-billed: the customer signs up on the gateway's page
sub, _ := mgr.Subscribe(ctx, "stripe", payment.SubscriptionRequest{
    Reference: "PLAN-PRO-" + user.ID, Amount: 1500, Currency: "USD",
    Interval: payment.Month, Cycles: 12, // 0 = until canceled
    Customer: payment.Customer{Email: user.Email},
    SuccessURL: "https://app.example.com/welcome", CancelURL: "https://app.example.com/plans",
})
return c.Redirect(http.StatusSeeOther, mgr.PayURL(&payment.Payment{ID: sub.ID}))

// manager-billed: a saved card is charged now, then every period
sub, _ := mgr.Subscribe(ctx, "", payment.SubscriptionRequest{
    Reference: "PLAN-PRO-" + user.ID, Amount: 150000, Interval: payment.Month, PaymentMethodID: pm.ID,
})

// run renewals from the scheduler (safe to run often and from several instances)
schedule.Call(func(ctx context.Context) error {
    _, err := mgr.RunDueSubscriptions(ctx)
    return err
}).EveryFifteenMinutes()
```

Subscription statuses: `pending → active ⇄ past_due → canceled / completed / failed`.

- **Renewals**: every paid period is recorded as an ordinary payment with `SubscriptionID` set, so refunds and history work on it. `mgr.SubscriptionPayments(ctx, id, page, perPage)` lists them.
- **Retries for manager-billed subscriptions**: a declined renewal makes the subscription `past_due`. It's retried after 1, 3 and 5 days (`payment.DefaultRetryPolicy`), and then it's `failed`.
- **Retries for gateway-billed subscriptions**: the gateway runs its own retries.
- **Finishing**: when the last of `Cycles` is paid, the subscription is `completed` and the manager cancels it at the gateway. This covers gateways that bill until canceled, such as Stripe.
- `mgr.CancelSubscription`, `mgr.RetrySubscription` (a failed renewal, now) and `mgr.SyncSubscription` (read it from the gateway).
- **Events**: `subscription.renewed` for every paid period, `subscription.updated` and `subscription.<status>` for every change, and `payment_method.updated` and `payment_method.<status>` for saved cards.

Gateway notes:

- **PayHere**: preapproval posts to `/pay/preapprove`, with the checkout hash computed on a zero amount. Recurring checkout sends `recurrence` and `duration`, and PayHere bills only weekly, monthly or yearly. Charging and the Subscription Manager need the business app ID and secret. Check the preapproval hash against a sandbox merchant before going live.
- **Stripe**: saved cards use a Checkout Session in setup mode, which creates a Stripe customer. Charges are off-session PaymentIntents, and a card that needs authentication fails the charge. Subscribe to `invoice.paid`, `invoice.payment_failed` and `customer.subscription.*` as well as the checkout events.
- **PayPal**: each subscription creates its own product and plan for the price. Saving cards in PayPal's vault isn't supported yet.

## Writing a gateway plugin

A driver is a `*payment.Driver` with a settings schema and a constructor. The gateway it builds needs only two methods. Optional interfaces add more:

- `StatusFetcher`
- `Refunder`
- `Capturer`
- `Canceler`
- `ReturnHandler`
- `ConnectionTester`
- for saved cards and charging: `Preapprover`, `Charger`, `MethodRevoker`
- for subscriptions: `Subscriber`, `SubscriptionCanceler`, `SubscriptionRetrier`, `SubscriptionFetcher`
- for saved-card and subscription return visits: `RecordReturnHandler`

A webhook `Update` can carry a `Method` or `Subscription` part, and a subscription part can carry an `Installment` (one period's payment). Record ids tell drivers what a gateway reference points at: `pay_…`, `pm_…` and `sub_…` (`payment.Kind(id)`).

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
