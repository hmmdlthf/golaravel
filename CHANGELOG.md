# Changelog 🚀

All notable changes to the **GoLaravel** framework will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
