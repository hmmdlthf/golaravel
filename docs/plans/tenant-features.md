# Plan: Per-Tenant Features & Settings (`pkg/features`)

Status: Phases 1–3 done 2026-11-27 and released in v1.19.0. Tests pass on SQLite, MySQL 8.4, Postgres 17, and with a Redis 7 cache. Phase 4 (GoFilament settings page, super-admin screen, visibility helpers) shipped in GoFilament v1.46.0. Phase 5b (the `Manager.Handler` endpoint, plus a `FeatureClient` in TypeScript and in the Flutter package `clients/flutter`) shipped in v1.20.0, together with the Dart offline sync client. Phase 5a, the plan layer, is still deferred.

Decisions so far: no plan layer in v1, no user layer in v1, explicit tenant
selection via `features.ForTenant(ctx, id)`, package name `features`.

Phase 1 differs from the sketch below in these ways:
- The column is `name`, not `key`, because `key` is reserved in MySQL. Rows have
  `id = "tenant_id|name"` so Ent can manage the table.
- `Int(...).Min().Max()` became `Int(...).Rules(features.Range(1, 50))`.
- `RecurringInvoices.Enabled(ctx)` became `features.Enabled(ctx, RecurringInvoices)`
  and `features.Is(ctx, TaxMode, "exclusive")`, because Go methods can't be
  specific to one type parameter.

## Goal

Let each tenant turn optional features on or off and choose among several
values for a setting (e.g. `tax.mode` = `inclusive | exclusive | none`).
Every feature has a **default in code**. A tenant only gets a database row
when it differs from the default:

```
value(tenant, key) = tenant row exists ? tenant row : definition default
```

GoFilament builds its tenant settings page, its navigation and its resource
visibility from the same registry.

## Design decisions

1. **One concept, typed values.** A feature on/off switch is a setting of
   type `bool`. A multi-choice setting is `enum` with a fixed list of options.
   `int`, `string` and `json` cover the rest. One registry, one table, one API.
2. **Definitions live in code, overrides live in the DB.** New features need
   no migration and no seeding of every tenant. Removing a row resets the
   tenant to the default, and changing a default reaches every tenant that
   has no override.
3. **Fails closed like `pkg/privacy`.** Reading or writing with no tenant in
   the context returns `privacy.ErrTenantMissing`, unless tenancy is bypassed
   (then you get the default).
4. **Validation on write, tolerance on read.** `Set` rejects values the
   definition doesn't allow. If a stored value later becomes invalid (say an
   enum option was removed), `Get` returns the default and logs a warning.
   It never returns the invalid value.
5. **SQL-first storage, optional Ent schema.** This follows the `pkg/offline`
   pattern: the store uses plain SQL with `Migrate()`, and an Ent schema is
   shipped for projects that manage migrations with Ent/Atlas.

## Storage

Table `tenant_settings`:

| column       | type          | notes                                  |
|--------------|---------------|----------------------------------------|
| `tenant_id`  | varchar(191)  | PK part                                |
| `key`        | varchar(191)  | PK part, e.g. `invoices.recurring`     |
| `value`      | JSON          | encoded typed value                    |
| `updated_by` | varchar(191)  | nullable, user id                      |
| `updated_at` | timestamp     |                                        |

Primary key `(tenant_id, key)`. A tenant's settings load in one indexed query.

## API sketch

```go
// Definitions (app code, e.g. app/features/features.go)
var (
    RecurringInvoices = features.Bool("invoices.recurring", false).
        Label("Recurring invoices").Group("Billing").
        ManagedBy(features.SuperAdmin)          // sold per plan, tenant can't toggle

    TaxMode = features.Enum("tax.mode", "inclusive", "inclusive", "exclusive", "none").
        Label("Tax mode").Group("Billing")      // tenant admin may change

    MaxBranches = features.Int("branches.max", 1).Min(1).Max(50).
        ManagedBy(features.SuperAdmin)
)

// Boot
mgr := features.New(db, features.Options{Cache: cacheStore})
mgr.Register(RecurringInvoices, TaxMode, MaxBranches)

// Read (tenant comes from privacy context)
on, err := RecurringInvoices.Get(ctx)    // bool
mode, err := TaxMode.Get(ctx)            // string, always one of the options
if RecurringInvoices.Enabled(ctx) { ... } // convenience; false on error

// Write
err = TaxMode.Set(ctx, "exclusive")      // validated
err = TaxMode.Reset(ctx)                 // delete row -> default

// Bulk, for settings pages / frontends
all, err := mgr.All(ctx)                 // []Resolved{Def, Value, IsOverridden}
```

Generics make `Bool` return `*Feature[bool]`, `Enum` return
`*Feature[string]`, and so on, so `Get` is typed at compile time.

## Resolution & caching

- `mgr.load(ctx, tenant)` reads every override for the tenant in one query,
  builds a `map[key]json.RawMessage`, and caches it in `pkg/cache` under the
  tag `features:tenant:<id>` (single-flight is already in `cache/flight.go`).
- `Set`/`Reset` write the row, flush that tag, and fire a
  `features.Changed{Tenant, Key, Old, New, By}` event through `pkg/events`.
- A request-scoped memo is kept in the context, so one request never
  reads twice.
- Resolver chain: `tenant override → definition default`. The chain is a slice
  of resolvers, so a **plan/edition layer** (`plan override`) can be added
  between them later without changing the API (see open questions).

Phase 2 differs from the sketch above in these ways:
- The cache uses one key per tenant plus a version number instead of tags.
  Each write bumps the version, so a value loaded just before a write can't
  be served after it.
- The request memo is opt-in through `features.WithMemo(ctx)`. The Phase 3
  middleware will install it.

Phase 3 differs from the sketch above in these ways:
- Inertia gained `Engine.ShareFunc` for props computed per request.
  Features plug in with `inertiaEngine.ShareFunc("features", mgr.InertiaProp())`.
- Artisan commands come from `features.NewCLI(mgr, out).Commands()`, so
  `pkg/features` doesn't import `pkg/artisan`. This follows `offline.CompactCLI`.
- `features.Fake` covers only the faked keys: unfaked keys and `All` still
  need a tenant. `features.NewInMemory` gives a manager with no database.
- The `GET /features` endpoint for offline and mobile clients moves to phase 5.

## Integration points

| Area            | What                                                                 |
|-----------------|----------------------------------------------------------------------|
| HTTP middleware | `features.Require(RecurringInvoices)` → 404 or 403 when off; `RequireValue(TaxMode, "exclusive")` |
| Inertia         | Share `mgr.Public(ctx)` (only defs marked `.Public()`) as page props |
| Offline / TS / Flutter clients | `GET /features` endpoint returns public resolved values |
| Validation      | Rule `features.Enabled(key)` for conditional form fields            |
| Artisan         | `features:list [--tenant=]`, `features:set <tenant> <key> <value>`, `features:reset`, `features:prune` (delete rows for unregistered keys) |
| Testing         | `features.Fake(ctx, map[string]any{...})` overrides without DB      |

## GoFilament

- **Tenant settings page**: generated from the registry, grouped by `Group()`.
  `bool` → toggle, `enum` → select/radio, `int` → number with min/max.
  An "overridden" badge plus a "Reset to default" action on each field.
- **Permissions**: `ManagedBy(TenantAdmin)` fields show on the tenant's own
  settings page. `ManagedBy(SuperAdmin)` fields show only in the central
  admin's "Tenant → Features" tab, so paid features can't be self-enabled.
- **Visibility**: `Resource.VisibleWhen(features.Enabled(RecurringInvoices))`,
  plus the same for nav items, actions, table columns and form fields.
  Hidden resources also reject direct URL access, via the same middleware.

## Phases

1. **Core**: `pkg/features` with registry, typed defs, SQL store, `Migrate`,
   Ent schema, Get/Set/Reset/All, validation, fail-closed tenancy, tests.
2. **Performance & events**: cache integration, request memo, `Changed` event.
3. **Framework glue**: middleware, Inertia shared props, artisan commands,
   `Fake`, docs page `docs/tenant-features-and-settings.md`.
4. **GoFilament**: auto settings page, super-admin tab, `VisibleWhen` helpers.
5. *(Optional)* Plan/edition layer and client endpoint for offline apps.

## Open questions

1. ~~**Plan layer now or later?**~~ **Decided: later.** v1 uses only
   `tenant override → code default`. The resolver chain stays a slice so a
   plan layer can be added in phase 5 without changing the API.
2. **User-level overrides?** Do some settings also need a per-user layer
   (`user → tenant → default`), e.g. UI preferences?
3. **Central (non-tenant) context**: should a super admin be able to read and
   write another tenant's values explicitly, as in
   `mgr.ForTenant(id).Set(...)`? (Recommended: yes.)
4. **Package name**: `features` (Pennant-like) or `settings`?
