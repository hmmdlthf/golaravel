# GoLaravel ⚡️

The fastest full-stack web framework in the world — pairing the developer ergonomics, elegance, and batteries-included architecture of **Laravel** with the compiled performance, zero-reflection execution, and multiplatform power of **Go** and **Flutter**.

---

## 🚀 Key Highlights & Architectural Pillars

| Component | Architecture | Performance & Capability |
|---|---|---|
| **View Engine** | **Templ** (`.templ`) | **25.6x faster** than standard `html/template` (44.8 ns/op, **0 B/op, 0 allocs/op**) |
| **HTTP Router** | Zero-alloc Radix Trie | **8.6 ns** static matching, **27.9 ns** deep param matching (**0 allocs/op**) |
| **ORM Layer** | **Ent** (entgo.io/ent) | 100% type-safe graph traversal with **zero runtime reflection** |
| **Presentation Flexibility** | **Hybrid Engine** | **Flutter** (Web/Mobile/Desktop) + **Templ** micro-SSR (Invoices, Receipts) + **Inertia.js** |
| **Multiplatform Client** | **Flutter** | Native Android, iOS, Windows, macOS, Linux, and Web |
| **IoC Container** | Generic-based DI | Type-safe resolution (`Resolve[T]`) at **24.5 ns/op, 0 allocs/op** |
| **JSON Engine** | SIMD/Fast JSON (`goccy/go-json`) | **2.2x faster** serialization with buffer streaming |
| **Auth & Security** | Multi-Guard Auth | JWT Bearer tokens, Session cookies, Argon2id & Bcrypt, Gates & Policies |
| **Validation** | Form Requests | Struct-level binding with Laravel-compatible **422 Unprocessable Entity** error bags |
| **Async Processing** | Queues & Workers | In-memory & Redis worker pools with automatic retries and dead-letter handling |
| **Task Scheduling** | In-Process Cron | Fluent Go scheduler (`DailyAt`, `Hourly`, `EveryMinute`) |
| **Filesystem & Cache** | Flysystem & Cache Facades | Local disk, S3, Memory cache with automatic TTL eviction |
| **CLI Tool** | **Artisan for Go** | Project scaffolder (`artisan new`), generator suite, and lifecycle runners |

---

## 📊 Benchmark Results

Benchmarked on 12th Gen Intel Core i7-12700 (Linux amd64):

```text
BenchmarkRouter_ZeroAlloc_Matching-16      36,900,116    27.92 ns/op      0 B/op    0 allocs/op
BenchmarkTemplate_Templ-16                 25,088,866    44.83 ns/op      0 B/op    0 allocs/op
BenchmarkJSON_GoLaravel_Fast-16            12,140,564    99.63 ns/op    176 B/op    2 allocs/op
BenchmarkContainer_Resolve_Singleton-16    64,029,606    24.53 ns/op      0 B/op    0 allocs/op
BenchmarkGoLaravel_E2E_HTTP_Pipeline-16     5,567,833   215.50 ns/op    176 B/op    4 allocs/op
```

* **Zero Allocations**: Radix routing and `templ` rendering execute with **0 heap allocations**.
* **Sustained Throughput**: Over **4.6 million requests/second** on a single thread.

---

## 📦 Framework Packages

```text
golaravel/
├── pkg/
│   ├── golaravel/          # Application kernel & HTTP engine (sync.Pool context)
│   ├── container/          # Generic type-safe IoC container (Resolve[T])
│   ├── router/             # Zero-allocation Radix trie router (route groups, named routes)
│   ├── context/            # Pooled request context (c.JSON, c.Templ, c.Inertia, c.Param)
│   ├── view/               # Templ helpers (Blade-like @csrf, @method)
│   ├── flutter/            # Flutter Web static hosting with SPA fallback
│   ├── inertia/            # Full Inertia.js protocol adapter (React / Vue)
│   ├── auth/               # Multi-guard auth (JWT Bearer, Sessions, Gates & Policies)
│   ├── hash/               # Password hashing (Argon2id, Bcrypt with work-factor upgrades)
│   ├── validation/         # Form requests with 422 JSON error bags
│   ├── queue/              # Concurrent background job queues & worker pools
│   ├── schedule/           # In-process fluent cron scheduler
│   ├── events/             # Synchronous & asynchronous event dispatcher
│   ├── cache/              # Tagged cache engine, atomic locks, Memory & Redis with TTL
│   ├── storage/            # Multi-disk cloud filesystem (S3, MinIO, Local, SigV4 Presigned URLs)
│   ├── mail/               # Fluent email builder (LogDriver, SMTP)
│   ├── broadcast/          # Realtime WebSockets & SSE broadcasting with Ent hooks
│   ├── notification/       # Multi-channel notifications (Mail, DB, Broadcast, Slack, SMS)
│   ├── privacy/            # Multi-tenancy isolation scopes, soft deletes, and mixins
│   ├── session/            # Encrypted stateful sessions, flash messages, and CSRF tokens
│   ├── config/             # Multi-environment config loader (.env, typed getters)
│   ├── database/           # High-concurrency connection pools, Atlas migrations & Seeders
│   ├── http/               # Fluent outbound HTTP client & mocking engine (Http::fake)
│   ├── testing/            # Full-stack HTTP testing harness & assertion toolkit
│   └── artisan/            # Artisan command registry & generators
├── example/                # Full reference app (Ent ORM + Templ + Flutter Hybrid)
└── benchmarks/             # Micro and end-to-end benchmark suite
```

---

## ⚡ Quickstart

### 1. Interactive Project Creation
```bash
artisan new my-project
```
The interactive wizard prompts for:
- Presentation stack: Templ (Blade SSR), Inertia.js (React/Vue), Flutter, or **Hybrid (Flutter + Templ)**
- Target platforms (Web, Android, iOS, Windows, macOS, Linux)
- Database driver (SQLite, PostgreSQL, MySQL)

---

## 📖 Comprehensive Documentation

Explore the in-depth documentation guides in [`docs/`](./docs/):

- 🚀 **[Getting Started & Installation](./docs/installation.md)**
- ⚡ **[Routing & Controllers](./docs/routing-and-controllers.md)**
- 🗄️ **[Database & Ent ORM](./docs/database-and-ent.md)**
- 🔄 **[Database Migrations & Seeding (via Atlas)](./docs/migrations-and-seeding.md)**
- 🛡️ **[Multi-Tenancy Privacy Scopes & Soft Deletes](./docs/multi-tenancy-and-soft-deletes.md)**
- 🎨 **[Templating with Templ (Blade Equivalent)](./docs/templ-views.md)**
- 📱 **[Flutter Multiplatform & Hybrid Serving](./docs/flutter-multiplatform.md)**
- ⚛️ **[Inertia.js Protocol Adapter](./docs/inertia.md)**
- 🛡️ **[Validation & Form Requests](./docs/validation-and-form-requests.md)**
- 🔒 **[Authentication & Security](./docs/authentication-and-security.md)**
- 🍪 **[Stateful Sessions & Flash Messages](./docs/stateful-sessions-and-flash.md)**
- ⏱️ **[Queues & Task Scheduling](./docs/queues-and-scheduling.md)**
- 📡 **[Realtime WebSockets & SSE Broadcasting](./docs/realtime-broadcasting.md)**
- 🔔 **[Multi-Channel Notifications Engine](./docs/notifications.md)**
- ⚡ **[Tagged Cache Engine & Atomic Locks](./docs/tagged-cache-and-locks.md)**
- 🛠️ **[Artisan CLI Reference](./docs/artisan-cli.md)**
- 📊 **[Performance Benchmarks](./docs/benchmarks.md)**

---

## 🛠 Artisan Commands
```bash
# Code generation
artisan generate            # Rebuild Ent ORM models & Templ views
artisan flutter:build       # Compile Flutter Web into client/build/web

# Scaffolding
artisan make:schema Post    # Create Ent database schema
artisan make:controller Post # Create controller
artisan make:auth            # Scaffold turnkey auth & TOTP 2FA controller
artisan make:job SendMail   # Create async background job
artisan make:notification InvoicePaid # Create multi-channel notification
artisan make:mail Welcome   # Create mailable email
artisan make:policy Post    # Create authorization policy
artisan make:scope Active   # Create global Ent query scope
artisan make:view posts     # Create Templ view
artisan make:middleware Auth# Create middleware

# Server & Workers
artisan serve               # Start HTTP development server
artisan queue:work          # Launch background queue workers
```
