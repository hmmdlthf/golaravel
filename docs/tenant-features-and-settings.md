# Per-Tenant Features & Settings 🎛️ (`pkg/features`)

In a multi-tenant app, tenants rarely want the same things. One tenant pays for recurring invoices and another doesn't. One prices tax-inclusive, another tax-exclusive. `pkg/features` handles both kinds of difference:

- **Features** are on or off per tenant (`bool`).
- **Settings** hold a value per tenant: one of a fixed set of options (`enum`), a number, text, or any JSON value.

Every feature has a **default in code**. A tenant gets a database row only when its value differs from the default:

```
value(tenant, key) = tenant row exists ? tenant row : code default
```

So adding a feature needs no migration and no seeding of every tenant. Changing a default reaches every tenant that hasn't overridden it, and resetting a tenant just deletes its row.

---

## 1. Declaring features

Declare features once, as package-level variables:

```go
// app/features/features.go
package features

import "github.com/golaravel/golaravel/pkg/features"

var (
	RecurringInvoices = features.Bool("invoices.recurring", false).
		Label("Recurring invoices").Group("Billing").
		ManagedBy(features.SuperAdmin). // sold per plan: tenants can't enable it themselves
		Public()                        // safe to send to the frontend

	TaxMode = features.Enum("tax.mode", "inclusive", "inclusive", "exclusive", "none").
		Label("Tax mode").Group("Billing") // ManagedBy(TenantAdmin) is the default

	MaxBranches = features.Int("branches.max", 1).
		Rules(features.Range(1, 50)).
		ManagedBy(features.SuperAdmin)

	InvoiceFooter = features.String("invoice.footer", "").Rules(features.MaxLen(200))

	Theme = features.JSON("ui.theme", ThemeConfig{Color: "blue"}).Public()
)
```

| Constructor | Go type | Notes |
|---|---|---|
| `features.Bool(key, def)` | `bool` | on/off |
| `features.Enum(key, def, options...)` | `string` | value must be one of `options` |
| `features.Int(key, def)` | `int` | add `Rules(features.Range(min, max))` |
| `features.String(key, def)` | `string` | add `Rules(features.MaxLen(n))` |
| `features.JSON[T](key, def)` | any `T` | any JSON-encodable value |

Keys use lowercase letters, digits, `.`, `_` and `-`. Use `features.NewRule(func(v T) error)` for your own checks.

`Label`, `Description` and `Group` are metadata for settings pages. `ManagedBy` records who may change a feature (`TenantAdmin` or `SuperAdmin`); your admin panel enforces it. `Public` marks a feature as safe to send to browsers and apps.

---

## 2. Booting the manager

```go
mgr, err := features.New(features.Options{
	DB:      db,               // *sql.DB, *sql.Tx, or an Ent client with sql/execquery
	Dialect: database.Postgres, // SQLite (default), MySQL, Postgres
	Cache:   cache.DefaultStore(), // optional, see §5
	Actor:   currentUserID, // optional func(ctx) string, stored as updated_by
})
if err := mgr.Migrate(ctx); err != nil { ... } // creates tenant_settings if missing

if err := mgr.Register(
	appfeatures.RecurringInvoices, appfeatures.TaxMode, appfeatures.MaxBranches,
	appfeatures.InvoiceFooter, appfeatures.Theme,
); err != nil { ... }
```

`Register` refuses an invalid key, a default that fails its own rules, and a key registered twice.

If Ent or Atlas manages your migrations, embed the schema instead of calling `Migrate`:

```go
// ent/schema/tenant_setting.go
type TenantSetting struct{ schema.TenantSetting } // github.com/golaravel/golaravel/pkg/features/schema
```

---

## 3. Reading and writing

The tenant comes from the context, set by `privacy.TenantMiddleware` (see [Multi-Tenancy](./multi-tenancy-and-soft-deletes.md)), exactly as for tenant-scoped queries.

```go
mode, err := appfeatures.TaxMode.Get(ctx) // typed: string, always one of the options
n := appfeatures.MaxBranches.Value(ctx)   // the default on any error (logged)

if features.Enabled(ctx, appfeatures.RecurringInvoices) { ... } // false on any error
if features.Is(ctx, appfeatures.TaxMode, "exclusive") { ... }

err = appfeatures.TaxMode.Set(ctx, "exclusive") // validated: ErrInvalidValue if not allowed
err = appfeatures.TaxMode.Reset(ctx)            // back to the default
over, err := appfeatures.TaxMode.Overridden(ctx)
```

For settings pages, forms and tools there are untyped versions keyed by name:

```go
all, err := mgr.All(ctx)        // []Resolved: Meta + Value + Overridden, one query
pub, err := mgr.Public(ctx)     // map[key]value of Public features only
v, err := mgr.Get(ctx, "tax.mode")
err = mgr.Set(ctx, "branches.max", "7") // strings are parsed: "true", "7", `{"color":"red"}`
err = mgr.Reset(ctx, "branches.max")
```

### Guarantees

- **Fails closed.** With no tenant in the context, reads and writes return `privacy.ErrTenantMissing`. `Enabled` and `Is` return `false`, so a missing tenant never turns a feature on.
- **Tenancy bypassed** (`privacy.WithBypassTenancy`): reads return the defaults, and writes are refused until you pick a tenant.
- **Super admins pick the tenant explicitly**: `features.ForTenant(ctx, "acme")`.
- **Invalid stored values fall back to the default.** If a stored value stops being valid (say you removed an enum option), reads return the default and log a warning. They never return the invalid value.

---

## 4. HTTP and Inertia

```go
app.Use(tenantMiddleware, features.Middleware()) // one load per request, however many checks

// 404 unless the feature is on for the request's tenant:
app.GET("/invoices/recurring", h, features.Require(appfeatures.RecurringInvoices))

// Only for some values:
app.POST("/invoices/:id/tax", h, features.RequireValue(appfeatures.TaxMode, []string{"exclusive"}))

// Say it exists but isn't available, or send an upgrade page:
features.Require(f, features.DeniedStatus(http.StatusForbidden))
features.Require(f, features.OnDenied(func(c *context.Context) error {
	return c.Redirect(http.StatusFound, "/billing/upgrade")
}))
```

`Require` fails closed: a request with no tenant gets `403`, and a lookup error gets `500` (logged).

To share the tenant's `Public` features with every Inertia page:

```go
inertiaEngine.ShareFunc("features", mgr.InertiaProp())
```

```js
// In your page component
if (usePage().props.features["invoices.recurring"]) { ... }
```

### Offline and mobile clients

`mgr.Handler()` serves the tenant's `Public` features as JSON, with an ETag, so clients can poll for changes cheaply:

```go
api.GET("/features", auth.BearerGuard(jwt), privacy.TenantMiddleware(privacy.JWTClaimResolver()), mgr.Handler())
// 200 {"features": {"invoices.recurring": true, "ui.theme": {"color": "blue"}}}
// 304 when If-None-Match matches; 403 without a tenant
```

In browsers and Tauri apps, `FeatureClient` from `@golaravel/offline` keeps the last known values across restarts and offline periods:

```ts
import { FeatureClient } from "@golaravel/offline";

const features = await FeatureClient.open({
  url: "https://pos.example.com/api/features",
  headers: async () => ({ Authorization: `Bearer ${await token()}` }),
  storageKey: `features:${tenantId}`, // one cache per tenant
});

if (features.enabled("invoices.recurring")) showRecurring(); // synchronous; never waits for the network
const theme = features.value("ui.theme", { color: "blue" });
features.on("change", rerender);
```

In Flutter (Android, iOS, desktop and web), the `golaravel` Dart package in `clients/flutter` has the same client:

```dart
final features = await FeatureClient.open(
  url: Uri.parse('https://pos.example.com/api/features'),
  headers: () async => {'Authorization': 'Bearer ${await token()}'},
  storageKey: 'features:$tenantId',
);
if (features.enabled('invoices.recurring')) showRecurring();
// FeatureClient is a ChangeNotifier: ListenableBuilder(listenable: features, ...)
```

In both clients, a feature never seen is off. Hiding something in the client is only for display: the server must still check with `Require` or `Get`.

---

## 5. Caching and events

With `Options.Cache` set, each tenant's values are stored as one cache entry. App instances share it when the cache is SQL or Redis. Every write bumps a per-tenant version in the cache key, so a request that loaded values just before a write can never serve them afterwards. If the cache is down, reads go to the database. `CacheTTL` (default one hour) limits how long old values can survive if an invalidation is lost.

`features.Middleware()` (or `features.WithMemo(ctx)` outside HTTP) loads the tenant's values once per request.

Every `Set` and `Reset` dispatches `features.EventChanged` through `pkg/events` (or `Options.Events`):

```go
events.Listen(features.EventChanged, func(p any) error {
	ch := p.(features.Changed) // Tenant, Key, Old, New, WasOverridden, Overridden, By, At
	audit.Log(ch.Tenant, ch.By, "changed "+ch.Key)
	return nil
})
```

A failing listener is logged and doesn't undo the change.

---

## 6. Artisan commands

```go
cli := features.NewCLI(mgr, os.Stdout)
for _, c := range cli.Commands() {
	registry.Register(&artisan.Command{Name: c.Name, Description: c.Description, Run: c.Run})
}
```

| Command | What it does |
|---|---|
| `artisan features:list` | every feature: type, default, who manages it, options |
| `artisan features:list --tenant=acme` | the tenant's values; `*` marks an override |
| `artisan features:set acme tax.mode exclusive` | set a tenant's value (validated) |
| `artisan features:reset acme tax.mode` | back to the default |
| `artisan features:prune [--dry-run]` | delete stored values of features no longer in code |

---

## 7. Testing

Force values for a test without touching the database:

```go
ctx := features.Fake(ctx, map[string]any{
	"invoices.recurring": true,
	"tax.mode":           "none",
})
```

Faked keys need no tenant, and other keys still resolve normally. Fakes nest, with inner values winning. They are validated like `Set`, so a typo or an invalid value fails the read instead of passing silently.

For tests that set values, use an in-memory manager:

```go
mgr := features.NewInMemory(features.Options{})
mgr.Register(appfeatures.TaxMode)
```
