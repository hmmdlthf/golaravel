# Changelog 🚀

All notable changes to the **GoLaravel** framework will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

### Added
- **CI (`.github/workflows/ci.yml`)**: every pull request and push to master runs `go vet` and `go test -race ./...`, plus an integration job against real MySQL 8.4, Postgres 17 and Redis 7.
- **Integration tests (`integration/`, a separate module)** run on whichever backends are configured (`GOLARAVEL_TEST_MYSQL_DSN`, `GOLARAVEL_TEST_POSTGRES_DSN`, `GOLARAVEL_TEST_REDIS_ADDR`); SQLite always runs. They cover:
  - the SQL and Redis session drivers;
  - the SQL and Redis cache stores, including concurrent increments, tags and locks shared between two instances;
  - validation `unique` / `exists`;
  - the Redis client;
  - the queue and rate limiter Redis code.

### Fixed
- **Container singletons (`pkg/container`)**: double-checked locking read `instance` without synchronization (a data race the new `-race` CI run found), and a singleton whose value was nil was rebuilt on every resolve. An atomic ready flag fixes both.
- **SQL cache locks on MySQL**: re-acquiring a lock without expiry by its own owner returned false, because MySQL counts an UPDATE that changes nothing as 0 affected rows. The owner is now checked directly.

## [v1.9.0] - 2026-11-16

### Added
- **Validation rule engine (`pkg/validation`)**: `validation.Make(data, validation.Rules{...})` with Laravel-style rule strings.
  - **Rules:** presence (`required*`, `sometimes`, `nullable`, `prohibited*`, `accepted`, ...), types and formats (`string`, `integer`, `numeric`, `decimal`, `boolean`, `array`, `json`, `email`, `url`, `uuid`, `ip`, `alpha*`, `regex`, dates, `timezone`), sizes that follow the field type, comparisons (`confirmed`, `same`, `gt`/`lt` against numbers or fields, `in`, `distinct`, `before`/`after`), files (`file`, `image` without SVG, `mimes` / `mimetypes` checked against the sniffed content, `extensions`) and database `unique` / `exists`.
  - **Fields:** nested and wildcard fields (`items.*.sku`), `bail`, custom `Rule` values / `RuleFunc`.
  - **Messages:** custom messages (`field.rule`), attribute names, and translation keys (`validation.<rule>`).
  - **Output:** `Validated()` returns only the fields listed in the rules.
  - **Database rules:** `SQLChecker(db, dialect)` and `SetDefaultDB` answer `unique` / `exists`, with table and column names checked.
  - **Form requests:** they can implement `Rules()`, `Messages()` and `Attributes()`, and `ValidateAndRespond` merges their errors with `Validate(v)`. `Validator.Check(field, value, rules)` uses rule strings inside `Validate`.
  - **Handlers:** `ValidateRequest(c, rules)`, `RequestData(c)` (JSON, urlencoded and multipart input) and `Validated(c)`.
  - **Scaffold:** the `lang/<locale>/validation.json` that artisan generates has per-type `min` / `max` messages.

### Changed
- `ValidateAndRespond` accepts an empty JSON body (rules decide what is required) and reads form input as well as JSON. The existing fluent methods are unchanged.

## [v1.8.0] - 2026-11-16

### Added
- **Persistent sessions (`pkg/session`)**:
  - `NewSQLDriver(db, dialect)` (SQLite, MySQL, Postgres; `Migrate` creates the table; idle sessions expire on read and are removed by `GC`) and `NewRedisDriver(client)` (TTL = session lifetime). Sessions survive restarts and are shared between instances.
  - Both drivers take the lifetime from the `Manager`.
  - `Manager.StartGC(ctx, interval)` runs the driver's GC. Nothing called it before, so memory and file sessions were never cleaned up.
- **Shared cache stores (`pkg/cache`)**:
  - `NewSQLStore(db, dialect)` (with `Migrate` and `Prune`) and `NewRedisStore(client)` implement the full `Store` API through a small `Backend` interface (`NewBackendStore`).
  - Values are stored as JSON. `Add`, `Increment` and locks are atomic in the backend.
  - Tag versions live in the backend, so a tag flush reaches every instance. Redis `Flush` only deletes keys under the store prefix.
- **Password reset tokens (`pkg/auth`)**:
  - `ResetTokenStore`, with `NewMemoryResetTokenStore` (the default) and `CacheResetTokenStore(cache.Store)`.
  - Configure the store with `NewPasswordResetBrokerWithStore` and `WorkflowConfig.ResetTokens`.
  - Only token hashes are stored, and emails are trimmed and lower-cased.
- **`pkg/redis`**: a small pooled RESP client (`New`, `Do`, `String`, `Int`, `Scan`) with per-command deadlines. Broken connections are dropped rather than reused. `pkg/redis/redistest` is an in-process fake for tests.
- **`database.Dialect`** (`SQLite`, `MySQL`, `Postgres`, `ParseDialect`): placeholder rebinding, upsert / insert-ignore statements and column types for SQL-backed stores.

### Notes
- The SQL stores are tested on SQLite only. The MySQL and Postgres statements follow each engine's syntax but have not been run against a server.
- `pkg/queue` and `pkg/ratelimit` still use their own copies of a Redis client. They can move to `pkg/redis` later.

## [v1.7.0] - 2026-11-16

### Security
- **Mail header injection (`pkg/mail`)**: the subject, From/To/Cc/Bcc/Reply-To, custom headers and attachment filenames were written into the message headers as-is. A line break in user input (for example a contact-form name or subject) could add headers such as `Bcc` and turn the app into a spam relay. `BuildMIME` now returns `ErrInvalidHeader` for line breaks, unparsable addresses and invalid header names.
- **Metrics and health report (`pkg/telemetry`)**: `MountOptions.Authorize` guards `/metrics`, `/health` and the report attached to a failing `/readyz`. `/livez` and the `/readyz` status stay public for probes.

### Added
- **Mail encoding**: non-ASCII display names and subjects are RFC 2047 encoded, attachment names are RFC 2231 encoded, and custom headers are written in sorted order.
- **`SecureHeaders(SecureHeadersOptions{...})`**: `ContentSecurityPolicy` (or `CSPReportOnly`), `PermissionsPolicy`, `FrameOptions` and `HSTSMaxAge`; `middleware.StrictCSP` is a starting policy. `SecureHeaders()` with no options behaves as before.
- **JWT**:
  - `JWTManager.Issuer` / `Audience` stamp `iss` / `aud` on issued tokens and require them on verified ones.
  - Tokens carry `nbf` and a random `jti`, and a future `nbf` is rejected.
  - `Leeway` tolerates clock skew, and `RotateFrom(oldSecrets...)` keeps accepting tokens signed with previous secrets.
  - Tokens without these claims still verify.

### Changed
- Attachment filenames without special characters are written unquoted (`filename=invoice.pdf`), as `mime.FormatMediaType` produces. This is valid MIME.

## [v1.6.0] - 2026-11-16

### Security
- **Sessions (`pkg/session`)**: `DefaultConfig` shipped a hard-coded `SecretKey`, so anyone could forge cookie-driver sessions (for example, signing in as any user). With no key, or with that old key, a random key is generated per process and a warning is logged. Set `Config.SecretKey` in production.
- **Sessions**: passing any `Config` turned `HttpOnly` and cookie encryption off, because unset bools are `false`. Both are now always on.
- **Session file driver**: the session id from the cookie was joined into a file path with only `filepath.Clean`, so `../` escaped the session directory. Ids must now be plain tokens.
- **CORS (`middleware.CORS`)**: any `Origin` was reflected whenever `AllowedOrigins` was not exactly `["*"]`, with credentials when `AllowCredentials` was set. Only listed origins (or ones `AllowOriginFunc` accepts) are reflected now. `"*"` never sends credentials, and `Vary: Origin` is always set.
- **Recovery**: the panic value (which can hold SQL, file paths or secrets) was sent to the client. It is now only logged.
- **Request bodies**: nothing capped them, so one large body could exhaust memory. `Engine` limits bodies to 32 MiB by default (`Engine.MaxBodyBytes(n)`; `n <= 0` removes the limit). `Run` caps request headers at 1 MiB.
- **Queue dashboard**: `MountDashboard` exposed job payloads and retry, delete and flush actions to anyone. The new `MountDashboardWith(r, DashboardOptions{Prefix, Authorize})` checks `Authorize` on every route, and the routes that change state refuse cross-site browser requests.
- **JWT (`pkg/auth`)**: an empty secret signed tokens with an empty HMAC key. `GenerateToken` / `VerifyToken` now return `ErrEmptySecret`. Tokens whose header is not `HS256` are rejected. The example app no longer falls back to a hard-coded JWT secret.
- **CSRF**: tokens are compared in constant time, and the cookie is `Secure` behind an HTTPS proxy.
- **`middleware.RateLimiter`**: buckets were keyed by the raw connection address, so every client behind a proxy shared one, and old buckets were never removed. It now uses `ClientIP()` and forgets idle clients.
- **`SecureHeaders`**: sends `X-XSS-Protection: 0` (current OWASP advice) and HSTS on HTTPS requests.

### Added
- `JWTManager.RevokedUsing(fn)` / `ErrTokenRevoked` to reject tokens after logout or a password change.
- `CORSOptions.AllowOriginFunc`; `session.Config.AllowScriptAccess` / `DisableEncryption`; `queue.DashboardOptions`; facade `Queue.MountDashboardWith`.

### Breaking
- `session.Config{HttpOnly: false}` / `Encrypt: false` no longer turn those off; use `AllowScriptAccess` / `DisableEncryption`.
- Apps that relied on the built-in session `SecretKey` get a random key per process, so sessions end on restart until a key is configured.
- `middleware.CORS` with a list of origins no longer allows unlisted origins.
- `queue.MountDashboard` (no `Authorize`) answers 403 on every route; switch to `MountDashboardWith`. If the routes already sit behind your own auth middleware, pass `Authorize: func(*context.Context) bool { return true }`.
- Request bodies over 32 MiB fail to read unless `Engine.MaxBodyBytes` is raised.
- `NewJWTManager("")` can no longer issue or verify tokens.

## [v1.5.0] - 2026-11-16

### Security
- **`Context.ClientIP()` (`pkg/context`)**: it trusted the first `X-Forwarded-For` value from any peer, so clients could spoof their IP (rate limits, logs). Forwarding headers are now only believed when the direct peer is a trusted proxy, and `X-Forwarded-For` is read from the right, skipping trusted hops. The default trusted networks are loopback and private ranges (a reverse proxy on the same host or private network). Configure others with `context.SetTrustedProxies(cidrs...)`; call it with no arguments to ignore forwarding headers. IPv6 `RemoteAddr` values are now returned without brackets.

### Breaking
- Apps behind a proxy or CDN that connects from **public** addresses (for example Cloudflare) now see the proxy's IP until its ranges are passed to `context.SetTrustedProxies`.

## [v1.4.1] - 2026-11-11

### Fixed
- **Router (`pkg/router`)**: a static route registered after a sibling parameter route (for example `/items/reorder` after `/items/:id/...`) was never matched; the parameter route answered instead. Static children now always take precedence regardless of registration order.
- **Database sessions (`pkg/database`)**: raw statements through Ent (`client.ExecContext` / `client.QueryContext`) bypassed the request session and therefore its transaction and row level security settings. `SessionDriver` now routes them into the session.

## [v1.4.0] - 2026-11-10

### Added
- **Request-Scoped Database Sessions (`pkg/database`)**:
  - `Session`: a transaction that starts lazily on the first statement and runs an optional `SessionInit` hook, e.g. to set a role or tenant variable for PostgreSQL row level security. Requests that never touch the database never open one.
  - `SessionDriver` / `NewSessionDriver`: an Ent SQL driver that routes statements into the `Session` carried by the statement's context; nested `client.Tx(ctx)` calls are backed by savepoints.
  - `SessionMiddleware`: gives each request a session, committing when the handler chain succeeds and rolling back on error or panic.
  - `WithSession`, `SessionFromContext` and `WithoutSession` context helpers.

## [v1.1.2] - 2026-11-01

### Fixed
- **Cross-Platform Telemetry (`pkg/telemetry`)**:
  - Replaced Unix-only `syscall.Statfs` in `DiskCheck` with OS-specific implementations (`disk_unix.go` and `disk_windows.go` via Win32 `GetDiskFreeSpaceExW`).
  - Enables clean cross-compilation for Windows (`GOOS=windows GOARCH=amd64`).

---

## [v1.1.1] - 2026-11-01

### Added
- **HTTP Interoperability**:
  - `WrapHandler(http.Handler)` converts standard `http.Handler` into GoLaravel `context.HandlerFunc`.
  - `WrapHandlerFunc(http.HandlerFunc)` converts standard `http.HandlerFunc` into GoLaravel `context.HandlerFunc`.

---

## [v1.1.0] - 2026-10-31

### Added
- **Full Localization & Translation Subsystem (`pkg/translation`)**:
  - Laravel-style JSON dictionaries for grouped files (`lang/{locale}/{group}.json`) and flat root files (`lang/{locale}.json`).
  - Parameter interpolation with automatic casing matching (`:name`, `:Name`, `:NAME`).
  - Pluralization / choice engine (`SelectPlural` / `TransChoice`) supporting singular/plural (`apple|apples`), exact numbers (`{0}`), and numeric ranges (`[1,5]`).
  - Embedded `fs.FS` (`//go:embed`) and filesystem directory (`LoadDir`) loaders.
  - Automated HTTP `translation.Middleware` with prioritized locale resolution: URL query (`?lang=`), route parameters (`:lang`), session keys, cookies, and weighted `Accept-Language` HTTP headers.
  - Request context helpers (`c.Trans()`, `c.TransChoice()`, `c.GetLocale()`, `c.SetLocale()`).
  - First-class Right-to-Left (RTL) language detection (`ar`, `fa`, `ur`, `he`, etc.), regional normalization (`ar-SA`, `fa-IR`), custom RTL registry (`RegisterRTL`), and direction helpers (`c.IsRTL()`, `c.Direction()`).
  - Artisan CLI generator command: `artisan make:lang <locale>`.
  - Top-level framework facade exports in `golaravel.go` (`Trans`, `TransChoice`, `SetLocale`, `GetLocale`, `IsRTL`, `Direction`, `Localization`).

---

## [v1.0.3] - 2026-10-31

### Fixed
- **Radix Router Catch-All Wildcard Integration (`pkg/router/tree.go`)**:
  - Fixed catch-all wildcard (`/*filepath`) registration when coexisting with existing static sub-branches under the same prefix (e.g., `/admin/assets/*`, `/admin/api/*`, and `/admin/*filepath`).
  - Added support for root/prefix exact match fallback to catch-all route handlers with empty string parameter (e.g. `/admin/` gracefully serving SPA index HTML).
  - Prevented invalid slice index and false conflict panics during radix child node insertion.

---

## [v1.0.2] - 2026-10-31

### Fixed
- **Zero-Allocation Radix Router Multi-Child Branching (`pkg/router/tree.go`)**:
  - Fixed route lookup on parameterized paths with multiple distinct static suffixes (e.g. `/:id/edit` and `/:id/delete`).
  - Search matching child prefix when transitioning past parameter delimiters instead of unconditionally falling back to first child.

---

## [v1.0.1] - 2026-10-31

### Fixed
- **Zero-Allocation Radix Router (`pkg/router`)**:
  - Prioritize static route matching over wildcard parameter children on shared path prefixes (e.g. `/users/create`, `/users/bulk`, `/users/export` alongside `/users/:id`).
  - Support seamless coexistence of static sibling routes and wildcard parameter nodes within radix tree nodes without route conflicts or panics.

---

## [v1.0.0] - 2026-10-30

### Initial Production Release 🎉

GoLaravel is an ultra-high-performance full-stack web framework that brings Laravel's unmatched developer ergonomics, expressive syntax, and robust architecture to the Go ecosystem — powered by compiled type-safety, zero-reflection execution, and multiplatform delivery.

---

### ⚡ Core Architecture & HTTP Foundation
- **Zero-Allocation Radix Router** (`pkg/router`):
  - Ultra-fast routing benchmarked at 150k+ req/sec with zero allocations per request.
  - Expressive route registration (`router.Get`, `router.Post`, `router.Group`, prefixes, nested groups).
  - Explicit and dynamic route parameters (`:id`, `{slug}`).
  - Built-in HEAD request fallback to matching GET routes.
  - Compile-time type-safe **Route Model Binding** (`router.BindModel[T]`, `router.BindPredicate[T]`) with automatic 404 aborts.
- **Context & Pool Management** (`pkg/context`):
  - Recycled HTTP contexts via `sync.Pool` eliminating GC pauses under extreme concurrency.
  - Rich helper methods: `.JSON()`, `.String()`, `.Status()`, `.Header()`, `.Cookie()`, `.Redirect()`, `.Query()`, `.Param()`, `.PostForm()`, `.File()`, `.SaveUploadedFile()`.
- **Top-Level Framework Ergonomics** (`golaravel.go`):
  - Unified facade exporting aliases and constructors (`golaravel.New()`, `golaravel.Default()`, `golaravel.Http`, `golaravel.Test()`, `golaravel.NewFaker()`).

---

### 🗄️ Database & Ent ORM Integration
- **Declarative Schema-as-Code** (`app/schema/`):
  - Schema definition using Facebook's Ent ORM code generation.
  - Seamless graph edge traversals, eager loading, and type-safe query builders.
- **Atlas Versioned Migrations & Seeding Engine** (`pkg/database`):
  - Zero-downtime, declarative migration diffing via Ariga Atlas (`atlas.hcl`).
  - Hierarchical database seeders with deterministic execution chains (`pkg/database/seeder.go`).
- **Ent Model Factories & Zero-Dependency Faker** (`pkg/database/factory`):
  - Generic model factories (`Factory[T]`) with immutable chaining, `.Count()`, `.State()`, `.Sequence()`, and hooks (`AfterMaking`, `AfterCreating`).
  - Ent database creators (`.Create`, `.CreateOne`) and in-memory stubs (`.Make`, `.MakeOne`).
  - Built-in Faker engine covering Person, Internet, Phone, Text, Business, Location, Numbers, and deterministic seed generation (`faker.Seed(12345)`).
- **Privacy Scopes & Soft Deletes** (`pkg/privacy`):
  - Ent soft delete mixin with automatic query filtering, `withTrashed()`, `onlyTrashed()`, and `forceDelete()`.
  - Multi-tenant privacy interceptors with tenant isolation across databases.

---

### 🛡️ Authentication, Authorization & Security
- **Multi-Guard Authentication Engine** (`pkg/auth`):
  - Session and JWT token-based authentication guards.
  - Argon2id password hashing with constant-time cryptographic verification (`pkg/hash`).
  - Scaffolding for TOTP Two-Factor Authentication (2FA) with RFC 6238 time-step windows and QR code generator.
- **Socialite / OAuth2 Multi-Provider Authentication** (`pkg/auth/oauth`):
  - Turnkey drivers for GitHub, Google, GitLab, Bitbucket, Discord, and Slack.
  - OpenID Connect (OIDC) support, PKCE verification, and cryptographically secure state cookies.
- **Fine-Grained Policy Authorization** (`pkg/auth`):
  - Resource gate policies (`Gate.Define`, `Gate.Allows`, `Gate.Denies`).
  - Policy scaffolding via `artisan make:policy`.

---

### 🍪 Sessions, Rate Limiting & Middleware
- **Encrypted Stateful Sessions & Flash Messages** (`pkg/session`):
  - Cookie and distributed Redis session stores.
  - AES-256-GCM encryption with HMAC tamper-proofing.
  - Flash message subsystem (`session.Flash()`, `session.GetFlash()`) with automatic next-request expiration.
  - Strict CSRF protection middleware with double-submit cookie validation and timing-attack-safe comparison.
- **Sliding-Window Rate Limiting & Tiered Throttling** (`pkg/ratelimit`):
  - High-precision sliding-window log algorithm over Redis and in-memory storage.
  - Named limiters with dynamic client keying (`ByIP`, `ByUser`, `ByHeader`, `BySubdomain`).
  - Standard RFC 6585 and draft IETF headers (`X-RateLimit-Limit`, `X-RateLimit-Remaining`, `Retry-After`).
  - Tiered burst multipliers and tiered quota policies.

---

### ⏱️ Queues, Background Workers & Task Scheduling
- **Redis Queue Driver & Priority Workers** (`pkg/queue`):
  - Robust worker pool supporting multiple named priority queues (`high`, `default`, `low`).
  - Exponential backoff retry policies with configurable retry counts.
  - Full-featured Dead-Letter Queue (DLQ) with CLI inspection, retry, and flush tooling.
  - Horizon-inspired real-time HTML Worker Dashboard with live throughput graphs, active workers, queue latencies, and DLQ management.
- **Advanced In-Process Cron Scheduler & Distributed Task Runner** (`pkg/schedule`):
  - Standard 5-field cron parsing (`* * * * *`, `@hourly`, `@daily`, `@weekly`).
  - Human-friendly fluent intervals (`EveryMinute()`, `EveryFiveMinutes()`, `HourlyAt(15)`, `DailyAt("02:30")`, `Weekdays()`).
  - Distributed mutual exclusion locks (`OneServer()`) preventing concurrent execution on clustered instances.
  - Overlap prevention (`WithoutOverlapping(timeout)`), background async dispatch (`RunInBackground()`), and lifecycle hooks (`Before()`, `After()`, `OnSuccess()`, `OnFailure()`).

---

### 📧 Mail, Notifications & Realtime Broadcasting
- **Advanced Mailables & Templ View Studio** (`pkg/mail`):
  - Fluent `Mailable` interface supporting HTML/plain-text bodies, attachments, embedded inline assets (CID), CC, BCC, and Reply-To.
  - SMTP transport driver and memory driver for testing.
  - Interactive **Mail Preview Studio** web interface with responsive viewport switching (Desktop, Tablet, Mobile) and source inspector.
- **Multi-Channel Notifications Engine** (`pkg/notification`):
  - Single notification dispatchable across multiple channels simultaneously: Mail, Database, Realtime Broadcast, Slack, and SMS (Twilio).
  - On-demand custom notification routing (`notification.Route()`).
- **Realtime WebSockets & SSE Broadcasting** (`pkg/broadcast`):
  - Public, private, and presence channels with client authorization guards.
  - WebSocket hub and Server-Sent Events (SSE) streaming engine.
  - Automatic broadcast event dispatching on Ent model lifecycle mutations.

---

### ☁️ Cloud Filesystem & Tagged Cache
- **Multi-Disk Cloud Filesystem** (`pkg/storage`):
  - Pluggable driver architecture supporting Local filesystem, Amazon S3, and MinIO.
  - AWS Signature Version 4 (SigV4) presigned PUT and GET URL generation for direct client uploads.
  - Direct file streaming, atomic writes, and metadata queries.
- **Tagged Cache Engine & Atomic Distributed Locks** (`pkg/cache`):
  - Tagged cache invalidation (`cache.Tags("users", "billing").Flush()`).
  - Atomic distributed locks with automatic expiration and ownership token verification.

---

### 🧪 Full-Stack HTTP Testing & Client Toolkit
- **HTTP Testing Harness & Assertion Toolkit** (`pkg/testing` / `golaravel.Test`):
  - Zero-overhead in-memory HTTP requests directly against framework handlers.
  - Fluent assertions: `.AssertStatus()`, `.AssertOk()`, `.AssertNotFound()`, `.AssertHeader()`, `.AssertJSON()`, `.AssertJSONExact()`, `.AssertJSONStructure()`, `.AssertViewIs()`, `.AssertViewHas()`.
- **Fluent HTTP Client & Mocking Engine** (`pkg/http` / `golaravel.Http`):
  - Fluent request builder (`WithToken()`, `WithHeaders()`, `WithTimeout()`, `Retry()`).
  - Deterministic HTTP mocking engine (`Http.Fake()`) with sequence responses and url-pattern matchers.

---

### 📊 Observability & Health Checks
- **Prometheus Metrics & OpenTelemetry Tracing** (`pkg/telemetry`):
  - Zero-allocation Prometheus metrics exporter (`/metrics`) tracking HTTP durations, throughput, active goroutines, and memory allocs.
  - W3C Trace Context propagation (`traceparent`, `tracestate`) and OpenTelemetry span collectors.
  - Subsystem Health Check Engine (`/health`) with disk space, memory, database, and Redis checks.

---

### 📱 Multiplatform Presentation Stacks
- **Templ SSR Components** (`pkg/view`):
  - Compile-time type-checked HTML templating with zero-reflection execution.
- **Inertia.js Protocol Adapter** (`pkg/inertia`):
  - First-class Inertia.js protocol adapter supporting React and Vue.js frontends without building an API layer.
- **Flutter Multiplatform Engine** (`pkg/flutter`):
  - Native embedding and serving of Flutter Web clients with client-side SPA routing fallbacks.
  - Showcase application: Real-World SaaS Invoicing application built with Flutter Web and Ent ORM.

---

### 🛠️ Artisan CLI Tooling
- **Project Wizard**: `artisan new <name>`
- **Full-Stack Scaffolder**: `artisan make:crud <Name> [--api | --web]`
- **Subsystem Generators**:
  - `artisan make:schema <Name>`
  - `artisan make:controller <Name>`
  - `artisan make:view <name>`
  - `artisan make:factory <Name>`
  - `artisan make:seeder <Name>`
  - `artisan make:migration <Name>`
  - `artisan make:job <Name>`
  - `artisan make:notification <Name>`
  - `artisan make:mail <Name> [--view]`
  - `artisan make:auth`
  - `artisan make:oauth <Provider>`
  - `artisan make:policy <Name>`
  - `artisan make:scope <Name>`
  - `artisan make:middleware <Name>`
  - `artisan make:limiter <Name>`
- **Runtime Commands**:
  - `artisan serve`
  - `artisan generate`
  - `artisan flutter:build`
  - `artisan migrate` / `migrate:diff` / `migrate:status` / `migrate:rollback`
  - `artisan db:seed`
  - `artisan queue:work` / `queue:failed` / `queue:retry` / `queue:flush`
  - `artisan schedule:list` / `schedule:run` / `schedule:work`
  - `artisan health`
  - `artisan version` / `--version` / `-v`
