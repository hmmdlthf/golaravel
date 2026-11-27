# GoLaravel Documentation 📖

Welcome to the official documentation for **GoLaravel** — the ultra-high-performance full-stack web framework that combines Laravel's developer ergonomics and architectural elegance with Go's compiled speed, zero-reflection execution, and multiplatform power.

---

## 📚 Table of Contents

1. **[Getting Started & Installation](./installation.md)**
   - System Prerequisites (Go, Templ, Flutter)
   - Installing the Artisan CLI
   - Creating Your First Project (`artisan new`)
   - Directory Structure Overview

2. **[Routing & Controllers](./routing-and-controllers.md)**
   - Zero-Allocation Radix Router
   - Route Groups & Prefixes
   - Route Parameters (`:id` and `{id}`)
   - Context Pooling (`sync.Pool`)
   - Middleware Pipeline

3. **[Database & Ent ORM](./database-and-ent.md)**
   - Declarative Schema-as-Code (`app/schema/`)
   - Running `artisan generate`
   - Type-Safe Graph Edge Traversal (`WithItems()`)
   - Atomic Transactions (`WithTx` / `client.Tx`)
   - High-Concurrency Connection Pooling

4. **[Database Migrations & Seeding (via Atlas)](./migrations-and-seeding.md)**
   - Declarative Atlas Migration Workflow (`atlas.hcl`)
   - Versioned Migrations (`artisan migrate`, `artisan migrate:diff`)
   - Migration Status & Rollbacks (`migrate:status`, `migrate:rollback`)
   - Hierarchical Database Seeding Engine (`artisan db:seed`, `make:seeder`)

5. **[Ent Model Factories & Faker Data Seeder (PRD §2.3)](./model-factories-and-faker.md)**
   - Compile-Time Generic Model Factories (`factory.New[TDef]()`)
   - High-Performance Zero-Dependency Faker Engine (`faker.Faker`)
   - Deterministic Seeded Generation for Reproducible Tests (`faker.Seed(12345)`)
   - Factory States (`.State`), Sequences (`.Sequence`), and Counts (`.Count`)
   - Direct Ent ORM Database Integration (`.Create`, `.CreateOne`) and In-Memory Stubs (`.Make`)
   - Scaffolder Command (`artisan make:factory <Name>`)

6. **[Multi-Tenancy Privacy Scopes & Soft Deletes (PRD §2.3)](./multi-tenancy-and-soft-deletes.md)**
   - Soft Delete Mixin & Interceptor (`withTrashed`, `onlyTrashed`, `forceDelete`)
   - Multi-Tenant Privacy Scopes & Automatic Injection
   - Modular HTTP Multi-Tenancy Middleware (Subdomain, Header, Param, JWT Claims)
   - Custom Global Query Scopes (`artisan make:scope`)

7. **[Templating with Templ (Blade Equivalent)](./templ-views.md)**
   - Why Templ Replaces Traditional Blade
   - Layout Components & Children
   - Blade-like Helpers (`@csrf`, `@method`)
   - Zero-Reflection Direct Stream Rendering

8. **[Flutter Multiplatform & Hybrid Serving](./flutter-multiplatform.md)**
   - Multiplatform Support (Web, Windows, macOS, Linux, iOS, Android)
   - Serving Flutter Web from GoLaravel (`flutter.ServeWeb`)
   - **The Hybrid Micro-SSR Pattern**: Instant zero-JS public invoice/receipt pages vs Flutter SPA

9. **[Inertia.js Protocol Adapter](./inertia.md)**
   - Modern Monoliths without REST/GraphQL boilerplate
   - `c.Inertia("Component", props)`
   - Asset Versioning & Conflict Resolution

10. **[Validation & Form Requests](./validation-and-form-requests.md)**
    - FormRequest Interface
    - Validation Rules (`Required`, `Email`, `MinLength`, etc.)
    - Laravel-Compatible **422 Unprocessable Entity** Error Bags

11. **[Authentication & Security](./authentication-and-security.md)**
    - JWT Bearer Tokens for Flutter & APIs
    - Password Hashing (Argon2id & Bcrypt)
    - Authorization Gates & Policies
    - Built-in Middlewares (CSRF, CORS, RateLimiter, SecureHeaders)

12. **[Socialite & OAuth2 Multi-Provider Authentication (PRD §4.2)](./socialite-oauth.md)**
    - Multi-Provider Drivers (GitHub, Google, Discord, Generic OAuth2/OIDC)
    - Normalized User Profile Interface (`oauth.User` implements `auth.Authenticatable`)
    - Anti-CSRF State Validation (Encrypted Session or Secure Cookie)
    - Mobile & SPA PKCE (RFC 7636) and Stateless Mode for Flutter Clients
    - Route Mounting Helper (`Socialite.Mount`) & Generator (`artisan make:oauth <Provider>`)

13. **[Stateful Sessions & Flash Messages (PRD §3.3)](./stateful-sessions-and-flash.md)**
    - AES-256-GCM Encrypted Cookie Sessions
    - Storage Drivers (Cookie, Memory, File)
    - One-Request Flash Messages (`session.Flash`)
    - Session-Backed CSRF Verification (`VerifyCSRFToken`)

14. **[Sliding-Window Rate Limiting & Tiered Throttling (PRD §3.3)](./rate-limiting-and-throttling.md)**
    - Mathematically Exact Sliding-Window Log Counter (Nanosecond Precision)
    - Named Limiter Registry (`ratelimit.For("api", resolver)`)
    - Dynamic Tiered Throttling (Free, Pro, Enterprise, IP Partitioning)
    - Multi-Rule & Compound Limits (Per-Second Burst + Per-Minute Sustained)
    - Standard IETF/GitHub Rate Limit Headers (`X-RateLimit-*`, `Retry-After`)
    - Dual Storage Backends: High-Speed `MemoryStore` & Distributed `RedisStore` (ZSET)
    - Scaffolder Command (`artisan make:limiter <Name>`)

15. **[Redis Queue Driver, Priority Workers & Horizon Dashboard (PRD §6)](./queues-and-worker-dashboard.md)**
    - Pluggable Drivers (`MemoryDriver`, `RedisDriver` with pure Go RESP client)
    - Multi-Queue Priority Polling (`high` -> `default` -> `low`)
    - Job Extensions: `Queueable`, `Delayable`, `Retryable`, `Timeoutable`
    - Dead-Letter Queue (DLQ) & `FailedJobStore` (Memory & Redis Hashes)
    - Horizon-Equivalent Worker Dashboard Web UI & JSON APIs (`/queue/dashboard`)
    - Artisan CLI Queue Operations (`queue:work`, `queue:failed`, `queue:retry`, `queue:flush`)

16. **[Advanced In-Process Cron Scheduler & Distributed Task Runner (PRD §5.2)](./task-scheduling.md)**
    - Standard 5-Part Cron Parser with Lists, Ranges, and Steps (`ParseCron`, `Matches`, `Next`)
    - Fluent Timing Directives (`EveryMinute`, `HourlyAt`, `DailyAt`, `WeeklyOn`, `Between`, `Timezone`)
    - Concurrency & Multi-Server Controls (`WithoutOverlapping`, `OnOneServer`, `RunInBackground`)
    - Multi-Target Handlers (Closures, OS Commands `Exec`, Queued Jobs `Job`)
    - Lifecycle Callbacks & Conditionals (`When`, `Skip`, `Before`, `After`, `OnSuccess`, `OnFailure`)
    - Artisan Scheduler Suite (`schedule:list`, `schedule:run`, `schedule:work`)

17. **[Realtime WebSockets & SSE Broadcasting (PRD §5.3)](./realtime-broadcasting.md)**
    - Realtime Broadcasting Architecture (WebSockets vs SSE)
    - Live Ent Model Mutation Hooks (`EntMutationHook`)
    - Flutter and Browser Native Client Subscriptions

18. **[Advanced Mailables, Templ View Templates & Preview Studio (PRD §7)](./mail-and-preview-studio.md)**
    - Fluent Mailable Architecture (`mail.Mailable`, `mail.PendingMail`)
    - Type-Safe Templ Email Templates (`msg.View(ctx, component)`)
    - Asynchronous Queued & Delayed Email Delivery (`mail.Queue`, `mail.Later`)
    - RFC 2045/2046 MIME Compiler with File & In-Memory Attachments
    - Interactive Mail Preview Studio Web UI (`/mail/preview`, responsive simulator, isolated iframe)
    - Test Assertions & Mocking (`mail.Fake()`, `mail.AssertSent`, `mail.AssertSentCount`)
    - Artisan Mailable Generator (`artisan make:mail <Name> [--view]`)

19. **[Multi-Channel Notifications Engine (PRD §7)](./notifications.md)**
    - Unified Notification Delivery (Email, In-App Database, Broadcast, Slack, SMS)
    - Notifiable Recipients & On-Demand Routing
    - Asynchronous Queued Notifications (`ShouldQueue`)
    - Generator Command (`artisan make:notification`)

20. **[Payment Gateways & Gateway Plugins (pkg/payment)](./payment-gateways.md)**
    - One API for any gateway: checkout, verified webhooks, refunds, captures, payment history
    - Built-in Stripe, PayHere, OnePay, Tabby, Tamara and test gateway drivers
    - Encrypted per-gateway settings with a schema admin panels can render
    - Writing your own gateway plugin (`payment.Driver`)

21. **[Tagged Cache Engine & Atomic Locks (PRD §7)](./tagged-cache-and-locks.md)**
    - O(1) Tagged Cache Invalidation (`Cache.Tags(...)`)
    - Distributed & In-Process Atomic Locks (`Cache.Lock(...)`)
    - Automatic Lock Expiration & Auto-Release (`Lock.Get`, `Lock.Block`)

22. **[Cloud Filesystem & Presigned URLs (PRD §7)](./cloud-storage-and-s3.md)**
    - Multi-Disk Registry (`local`, `public`, `s3`, `minio`)
    - Zero-External-SDK AWS Signature Version 4 (SigV4) Engine
    - Time-Limited Presigned URLs (`TemporaryURL`, `PresignedPutURL`)
    - Direct Client Uploads & Multipart Form Helpers (`c.FormFile`, `c.SaveUploadedFile`)

23. **[HTTP Testing Harness & Assertion Toolkit (PRD §5)](./testing.md)**
    - Zero-Network Direct In-Memory Pipeline Execution (`app.Test(t)`)
    - Fluent Request Builder (`WithToken`, `WithJSON`, `WithForm`, `WithFile`)
    - Deep JSON Path, Predicate, and Error Bag Assertions
    - HTML & View Assertions (`AssertSee`, `AssertDontSee`, `AssertSeeInOrder`)

24. **[Fluent HTTP Client & Mocking Engine (PRD §7)](./fluent-http-client.md)**
    - Expressive Request Chaining (`WithToken`, `WithHeaders`, `Timeout`, `Retry`)
    - Automatic JSON, Form, and Multipart File (`Attach`) Uploads
    - Comprehensive Mocking Engine (`Http.Fake()`, Sequences, Wildcards)
    - Test Assertions (`AssertSent`, `AssertSentCount`, `AssertNothingSent`)

25. **[Prometheus, OpenTelemetry & Health Check Engine (PRD §9)](./telemetry-and-health-checks.md)**
    - Prometheus Metrics Exposition (`/metrics`) with Go Runtime Statistics
    - OpenTelemetry Distributed Tracing & W3C Trace Context Propagation (`traceparent`)
    - Multi-Subsystem Health Probes (`/health`, `/livez`, `/readyz`)
    - HTTP Observability Middlewares (`MetricsMiddleware`, `TraceMiddleware`)
    - Terminal Health Dashboard (`artisan health`)

26. **[Localization & Translation Guide (pkg/translation)](./localization-and-translation.md)**
    - JSON Dictionaries (`lang/{locale}/{group}.json` and `lang/{locale}.json`)
    - Laravel-Style Parameter Interpolation (`:name`, `:Name`, `:NAME`)
    - Pluralization / Choice (`apple|apples`, `{0}`, `[1,5]`)
    - Automated HTTP Localization Middleware (`Accept-Language`, Query, Cookie, Session)
    - Request Context Helpers (`c.Trans()`, `c.TransChoice()`, `c.GetLocale()`)
    - Scaffolder Command (`artisan make:lang <locale>`)

27. **[Artisan CLI Reference](./artisan-cli.md)**
    - Project Creation (`artisan new`)
    - Full-Stack CRUD Scaffolder (`artisan make:crud <Name>`) (PRD §8.2)
    - Database Migrations & Seeding (`migrate`, `migrate:diff`, `db:seed`, etc.)
    - Generator Commands (`make:crud`, `make:schema`, `make:controller`, `make:job`, `make:factory`, `make:seeder`, `make:lang`, `make:notification`, `make:limiter`, `make:scope`, etc.)
    - Build & Lifecycle Commands (`generate`, `flutter:build`, `serve`, `health`)

28. **[Performance & Benchmarks](./benchmarks.md)**
    - Zero-Allocation Architecture Methodology
    - Micro-benchmark Results vs Gin, Fiber, and Laravel Octane

29. **[Per-Tenant Features & Settings](./tenant-features-and-settings.md)**
   - Typed Features & Settings with Code Defaults (`features.Bool`, `Enum`, `Int`, `JSON`)
   - Per-Tenant Overrides, Validation & Fail-Closed Tenancy
   - `Require` Middleware & Inertia Shared Props
   - Caching, Request Memo & `Changed` Events
   - `features:*` Artisan Commands & `features.Fake` for Tests
