# Changelog 🚀

All notable changes to the **GoLaravel** framework will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

### Added
- **Offline sync engine, server core (`pkg/offline`)**: push/pull sync for offline-first clients such as POS terminals.
  - **Push is idempotent.** A retried mutation id comes back as a duplicate with its original outcome and is not applied again. Idempotency is per tenant.
  - **Each mutation is atomic.** It is applied in its own transaction together with its row in an append-only ledger. Mutations sharing a `group` are applied all-or-nothing.
  - **Transient failures keep order.** One stops the batch, and that mutation and everything after it come back as `retry`. Permanent ones come back as `rejected`, with the server's current record so the client can undo its local change.
  - **Pull cannot skip a change.** It returns each changed entity once, in its current state or as a tombstone, after a cursor. A per-tenant sequence lock makes ledger rows commit in order, so a change that commits late is never missed.
  - **Snapshots and resyncs.** `Snapshot` pages through an entity for first-time loading, and a cursor older than the retained history gets `410 resync_required`.
  - **Conflicts are settled with hybrid logical clocks.** Strategies: `FieldLWW` (default), `LWW`, `ServerWins`, `ClientWins`, or a custom `Resolver`. Counter fields take `inc` deltas that add up across devices. Device clocks too far ahead are replaced with server time.
  - **Admin edits reach devices.** `offline.ChangeHook` records writes made outside sync (admin panel, jobs) and stamps them with the server clock, so devices pull them and older offline edits don't overwrite them.
  - **HTTP endpoints and storage.** Devices are registered per tenant and can be revoked. The endpoints are `POST /devices`, `POST /push`, `GET /pull` and `GET /snapshot`. Tables are created by `Engine.Migrate` (SQLite, MySQL, Postgres) or by the Ent schemas in `pkg/offline/schema`, which also provides `SyncableMixin`. Ent apps enable the `sql/execquery` feature so resources write through their own `*ent.Tx`.

### Fixed
- **Security: `privacy.TenantSQLInterceptor` did not filter Ent queries**: it looked for a `WhereP` method, which generated `*XQuery` types don't have (only mutations do), so it skipped the tenant filter without an error and a query in one tenant's context returned other tenants' rows. It now adds `WHERE tenant_id = ?` through the query's generated `Where(...predicate.X)` method, and a query type it cannot filter fails with the new `privacy.ErrTenantScopeUnsupported` instead of running unfiltered. Apps using `TenantSQLInterceptor` should upgrade; `TenantScope` was not affected.
- **`privacy.TenantMutationHook` skipped tenant enforcement without an error**: an update or delete on a mutation without `WhereP` ran without the `tenant_id` filter, and a failed `SetField` on create was ignored, so the row could be saved without the tenant. Both now fail with `ErrTenantScopeUnsupported` before the mutation runs. Generated Ent mutations with `TenantMixin` were not affected.

## [v1.18.0] - 2026-11-24

### Added
- **Payment gateways (`pkg/payment`)**: one payment API with gateway drivers as plugins. The manager creates payments and sends customers to the gateway (redirect, auto-posted form or client secret). It confirms return visits and verifies webhooks through the driver, moves payments through a state machine that ignores stale and duplicate notifications, and handles refunds, captures, voids and status sync. Status changes fire `payment.*` events. Gateway settings follow each driver's schema, with secret fields encrypted at rest. `MemoryStore` and `SQLStore` (SQLite, MySQL, Postgres) are included.
- **Built-in gateway drivers**: Stripe (`pkg/payment/stripe`), PayPal (`paypal`), PayHere (`payhere`), OnePay (`onepay`), Tabby (`tabby`), Tamara (`tamara`) and a no-account test gateway. `gateways.Provider(mgr)` installs them and mounts the `/payments` routes.
- **Saved cards, charging and subscriptions**: `SavePaymentMethod` (preapproval) and `Charge` (merchant-initiated charges on a saved card). `Subscribe` runs subscriptions either through the gateway (PayHere Recurring, Stripe Billing, PayPal Billing) or billed by the manager on a saved card with `RunDueSubscriptions`, which retries declines. Also `CancelSubscription`, `RetrySubscription`, `SyncSubscription`, renewals recorded as payments, `subscription.*` and `payment_method.*` events, and SQL tables for both.
- **`middleware.CSRF(except...)`**: paths to skip, exact or with a trailing `*` for a prefix, for third-party POSTs such as gateway webhooks.

## [v1.17.2] - 2026-11-17

### Fixed
- **TOTP clock-drift tolerance was off by default**: `NewTOTPManager(TOTPOptions{...})` with `SkewWindow` left out (as `WorkflowController` and gofilament do) took the zero value as "no tolerance", so only the current 30-second code worked and a phone clock a few seconds off, or a code typed just as it changed, was refused. `SkewWindow: 0` now keeps the default of one step either side; use the new `auth.NoSkew` to accept only the current step.

## [v1.17.1] - 2026-11-17

### Fixed
- **`session.StartSession(mgr)` built the default manager**: it resolved `DefaultManager()` before looking at the manager passed in, so apps with their own manager still built the unused default and logged its "no SecretKey" warning. The default is now resolved on the first request, and only when no manager is given.
- **Session CSRF check timing (`session.VerifyCSRFToken`)**: the token was compared with `!=`; it now uses a constant-time comparison (like `middleware.CSRF`) and refuses an empty session token.

## [v1.17.0] - 2026-11-17

### Added
- **Session login (`pkg/auth`)**: `auth.Login`, `auth.Logout`, `auth.LogoutOtherDevices`, `auth.SessionGuard` / `SessionGuardFunc` and `auth.UserProviderFunc`, so server-rendered sites can log in with the session cookie instead of a JWT. Login regenerates the session id and deletes the old one, and renews the CSRF token. Logout destroys the session and issues a new CSRF token.
- **Sessions linked to users (`pkg/session`)**: the SQL driver has a nullable, indexed `user_id` column (`Migrate` adds it to existing tables). `Manager.DestroyUserSessions(userID, except...)` ends a user's sessions on the memory, file, SQL and Redis drivers. The cookie driver returns `ErrUserSessionsUnsupported`. New `Manager.Regenerate`, `Manager.Invalidate`, `session.ManagerFrom(c)` and the `UserSessionDriver` interface.
- **`auth.SQLResetTokenStore`**: password reset tokens in a `password_reset_tokens` table (SQLite, including `modernc.org/sqlite`, MySQL and Postgres), with `Migrate` and `PruneExpired`.

### Changed
- **No warnings at import**: the default session manager and disk manager are built on first use instead of at package load. The default session manager takes its key from `APP_KEY`. The "no SecretKey" warning is logged only when a manager is built without a key, and the storage "no signing secret" warning only when a URL is first signed with the random key.

## [v1.16.0] - 2026-11-16

### Security
- **Support ticket IDs (`pkg/support`)**: `GenerateTicketID` produced only 32 bits of randomness (`TKT-` plus 8 hex characters), which can be brute-forced. For visitors who aren't signed in, the ticket ID is their only access to the ticket, so IDs now carry 128 bits (`TKT-` plus 32 hex characters).

## [v1.15.0] - 2026-11-16

### Security
- **2FA bypass (`pkg/auth`)**: the `two_factor_token` returned after a correct password (before the second factor) was signed with the same secret as access tokens, so `BearerGuard` / `CookieGuard` accepted it as a full login. Two-factor authentication could be skipped entirely. Challenge tokens now use a derived key and their own audience, and the guards refuse them.
- **Auth workflows (`WorkflowController`)**:
  - **Throttling:** logins and 2FA codes are throttled through the `ratelimit` store (`LoginAttempts` / `LoginWindow`, `TwoFactorAttempts` / `TwoFactorWindow`). A 6-digit code can no longer be brute-forced.
  - **Account enumeration:** unknown emails take as long to reject as wrong passwords.
  - **Codes:** TOTP codes are single-use (`TOTPManager.VerifyCodeStep`). Recovery codes are refused when `UpdateRecoveryCodes` is not configured (they would never be used up), and no token is issued without a verified factor.
  - **Password resets:** only existing accounts get reset mail, the reply is the same either way, mails are throttled per address, and the reset link is URL-encoded.
  - **Disabling 2FA** requires the current password or a 2FA code.
  - **Errors:** callback errors are no longer sent to the client. Passwords over 72 bytes (the bcrypt limit) get a validation error instead of a 500.
- **OAuth**: the `state` and PKCE cookies are `Secure` on HTTPS.

### Fixed
- **Panics** in scheduled tasks (including `RunInBackground`), async event listeners and queue jobs crashed the process. They are now recovered: tasks report an error, and jobs are retried or sent to the dead-letter queue.
- **`schedule`**: `Exec` keeps only the last 64 KiB of command output. `OnOneServer` logs a warning when the default cache is the in-memory store, since it then only prevents duplicates within one process.

### Breaking
- **`POST /auth/two-factor/disable`** needs `password` or `code` in the body.
- **Recovery codes** are refused unless `WorkflowCallbacks.UpdateRecoveryCodes` is set.
- **Forgot-password** sends no mail when `FindByEmail` finds no user (the response is unchanged).
- **Existing 2FA challenge tokens** stop working (they expire within 5 minutes anyway).

## [v1.14.0] - 2026-11-16

### Security
- **Broadcast channels (`pkg/broadcast`)**:
  - **Anyone could listen on any channel.** The SSE handler took `?channel=`, and the WebSocket handler subscribed to whatever channel the client named. `broadcast.Channel("orders.{id}", fn)` now registers authorization callbacks, like Laravel's channel routes. `private-` and `presence-` channels without a rule are refused, and other channels stay public. `Options.Authorize` can decide every subscription instead.
  - **Cross-site WebSocket hijacking.** WebSockets accepted every `Origin`, so any site could open one with the user's cookies. Now only the same origin, clients that send no `Origin`, and `Options.AllowedOrigins` are accepted.
  - **Smaller fixes:** the SSE `connected` event escapes the channel name (it was written raw, allowing JSON and SSE frame injection), event names are stripped of newlines, channel names with control characters are refused, and a WebSocket may hold at most `MaxSubscriptions` (100) channels.
  - New: `SSEHandlerWith` / `WebSocketHandlerWith(Options)`.
- **Signed storage URLs (`pkg/storage`)**: `NewLocalDriver` signed temporary download and upload URLs with a hard-coded fallback key when `APP_KEY` / `STORAGE_SECRET` was unset, so anyone could forge them. It now uses a random per-process key (with a warning), and refuses the old public key.
- **Tenancy (`pkg/privacy`)**: `TenantMiddleware` trusted whatever tenant the client named (`X-Tenant-ID`, query, route parameter or subdomain). New `TenantMiddlewareOptions.Authorize` checks membership and answers 403 when it fails. Without it, a one-time warning is logged.
- **SMTP (`pkg/mail`)**: `Encryption: "starttls"` silently fell back to plain text when the server didn't offer STARTTLS (an attacker can strip the offer); it is now required. Connections and the SMTP conversation have timeouts (`SMTPConfig.Timeout`, default 30s).
- **HTTP client (`pkg/http`)**: response bodies are capped at 64 MiB (`MaxResponseSize`, `ErrResponseTooLarge`) instead of being read fully into memory.
- **Metrics (`pkg/telemetry`)**:
  - Path labels are capped (`MaxPaths`, default 500; the rest count as `other`), so random URLs can't grow the metrics without bound.
  - `NormalizePaths` now works: ID-like segments become `:id`.
  - `traceparent` IDs must be lowercase hex and non-zero.
- **Flutter (`pkg/flutter`)**: `Version` and `BuildID` are escaped in the injected HTML.

### Breaking
- **Cross-origin WebSockets:** browser WebSockets from another origin, such as a Flutter web build on a different host, need `broadcast.WebSocketHandlerWith(broadcast.Options{AllowedOrigins: [...]})`.
- **Private channels:** subscriptions to `private-` / `presence-` channels need a matching `broadcast.Channel` rule.
- **Signed storage URLs:** they stop working across restarts until `APP_KEY` or `STORAGE_SECRET` is set.
- **SMTP:** `Encryption: "starttls"` fails against servers that don't offer STARTTLS.

## [v1.13.0] - 2026-11-16

### Fixed
- **Form method spoofing didn't work.** `view.Method("PUT")` emitted a `_method` field, but nothing read it, so HTML forms could never reach PUT, PATCH or DELETE routes. The engine now routes a POST with `_method` (or an `X-HTTP-Method-Override` header) of PUT, PATCH or DELETE as that method, before routing. GET is never overridden. Turn it off with `Engine.MethodOverride(false)`.
- **`.env` parsing (`pkg/config`)**:
  - `export KEY=value` lines are read.
  - Unquoted values drop a trailing ` # comment`.
  - Double-quoted values support `\n`, `\"` and `\\`, and their closing quote skips escaped quotes. Single-quoted values are taken literally.
  - Read errors are reported instead of being ignored.

### Added
- Tests for `pkg/config` and `pkg/view`, which had none.

## [v1.12.0] - 2026-11-16

### Added
- **Request body read timeout**: `Engine.BodyReadTimeout` (default 2 minutes) and `middleware.BodyReadTimeout(d)` per route. Upgrade requests and requests without a body are not affected, and the deadline is cleared once the body has been read, so slow handlers and SSE streams keep working.
- **Cache stampede protection**: `Remember` runs the function once for concurrent misses (per process, and across instances on the SQL / Redis stores through a backend lock; `RememberLockTimeout`).
- **Atomic `Pull`** on the SQL and Redis stores (`Puller`; Redis `GETDEL`).
- **Validation**: `validation.Password()` (min / max / letters / mixed case / numbers / symbols / not-in list, one message per unmet requirement; `MultiRule`), `exclude`, `exclude_if`, `exclude_unless`, `exclude_with`, `exclude_without`, `required_array_keys`, and `dimensions` (PNG, JPEG, GIF).

### Changed
- **`artisan make:oauth`** now generates a working controller: a `FindOrCreate` hook, with a warning against linking accounts by email alone, issues the app's JWT instead of returning the provider profile and its access token. A test checks the generated code compiles.

### Fixed
- Routes with more than 127 handlers overflowed the `int8` chain index.

## [v1.11.0] - 2026-11-16

### Added
- **JWT with public/private keys (`pkg/auth`)**: `NewJWTManagerWithKey(privateKey, kid, ttl)` signs RS256 (RSA ≥ 2048), ES256 (P-256) or EdDSA (Ed25519) tokens, so other services verify them with only the public key.
  - **Verifying elsewhere:** `NewJWTVerifier(publicKey, kid)` and `NewJWTVerifierFromJWKS(json)` create verify-only managers, and `JWKS()` publishes the public keys.
  - **Rotation:** `AcceptKey(publicKey, kid)` keeps accepting old keys; tokens carry `kid` and are checked only against that key.
  - **Key files:** `ParsePrivateKeyPEM` and `ParsePublicKeyPEM` read PEM keys.
  - **Algorithm checks:** the algorithm always comes from the manager, never from the token. Key-based managers refuse HS256 (no confusion with a public key used as an HMAC secret), and secret managers refuse key-signed tokens.
  - **Interoperability:** the ES256 format is checked against the example in RFC 7515, appendix A.3.

### Fixed
- **`ratelimit.ThrottleMax` routes shared one counter.** The counter was keyed only by client IP, so traffic on a route with a loose limit used up a strict one elsewhere (for example a feed route locking users out of login). The limit is now part of the key.

### Deprecated
- **`middleware.RateLimiter`** now delegates to `ratelimit.ThrottleMax(n, time.Minute)` instead of keeping its own in-memory token buckets. It therefore uses the configured `ratelimit` store (shared between instances with the Redis store) and sends the standard `X-RateLimit-*` headers. Use `ratelimit.ThrottleMax` or `ratelimit.Throttle` directly.

## [v1.10.0] - 2026-11-16

### Added
- **CI (`.github/workflows/ci.yml`)**: every pull request and push to master runs `go vet` and `go test -race ./...`, plus an integration job against real MySQL 8.4, Postgres 17 and Redis 7.
- **Integration tests (`integration/`, a separate module)** run on whichever backends are configured (`GOLARAVEL_TEST_MYSQL_DSN`, `GOLARAVEL_TEST_POSTGRES_DSN`, `GOLARAVEL_TEST_REDIS_ADDR`); SQLite always runs. They cover:
  - the SQL and Redis session drivers;
  - the SQL and Redis cache stores, including concurrent increments, tags and locks shared between two instances;
  - validation `unique` / `exists`;
  - the Redis client;
  - the queue and rate limiter Redis code.

### Changed
- **One Redis client.** `pkg/queue` and `pkg/ratelimit` now use the shared `pkg/redis` client instead of their own copies. The queue's copy put a connection back into the pool after a network error, so later commands could read a half-finished reply. Blocking pops use the new `Client.DoTimeout`. `redis.Open` creates a client without contacting the server.
- `ratelimit.RedisStore.Clear` uses `SCAN` instead of `KEYS`, so it doesn't block a large Redis. New `RedisStore.Close`.

### Fixed
- **Redis rate limiter could be exceeded.** `Hit` counted and then added in separate commands, so concurrent requests (for example a burst of login attempts across instances) could all pass the limit. The trim, count and add now run in one Lua script; 50 concurrent hits with a limit of 10 admit exactly 10.
- **Delayed jobs on custom queues never ran.** The Redis queue only moved due jobs for `default`, `high` and `low`; now it moves them for every queue `Later` was called with. Only the worker whose `ZREM` removed a job pushes it, so two workers can't run the same delayed job twice.
- **Container singletons (`pkg/container`)**: double-checked locking read `instance` without synchronization (a data race the new `-race` CI run found), and a singleton whose value was nil was rebuilt on every resolve. An atomic ready flag fixes both.
- **SQL cache `Increment` on Postgres** (found by the new integration run): two concurrent first increments of a new key could both read 0, losing an update, because `SELECT ... FOR UPDATE` locks nothing when the row is missing. The row is now inserted before it is locked, and increments retry on deadlocks and serialization failures.
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
