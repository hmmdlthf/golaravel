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

5. **[Multi-Tenancy Privacy Scopes & Soft Deletes (PRD §2.3)](./multi-tenancy-and-soft-deletes.md)**
   - Soft Delete Mixin & Interceptor (`withTrashed`, `onlyTrashed`, `forceDelete`)
   - Multi-Tenant Privacy Scopes & Automatic Injection
   - Modular HTTP Multi-Tenancy Middleware (Subdomain, Header, Param, JWT Claims)
   - Custom Global Query Scopes (`artisan make:scope`)

6. **[Templating with Templ (Blade Equivalent)](./templ-views.md)**
   - Why Templ Replaces Traditional Blade
   - Layout Components & Children
   - Blade-like Helpers (`@csrf`, `@method`)
   - Zero-Reflection Direct Stream Rendering

6. **[Flutter Multiplatform & Hybrid Serving](./flutter-multiplatform.md)**
   - Multiplatform Support (Web, Windows, macOS, Linux, iOS, Android)
   - Serving Flutter Web from GoLaravel (`flutter.ServeWeb`)
   - **The Hybrid Micro-SSR Pattern**: Instant zero-JS public invoice/receipt pages vs Flutter SPA

7. **[Inertia.js Protocol Adapter](./inertia.md)**
   - Modern Monoliths without REST/GraphQL boilerplate
   - `c.Inertia("Component", props)`
   - Asset Versioning & Conflict Resolution

8. **[Validation & Form Requests](./validation-and-form-requests.md)**
   - FormRequest Interface
   - Validation Rules (`Required`, `Email`, `MinLength`, etc.)
   - Laravel-Compatible **422 Unprocessable Entity** Error Bags

9. **[Authentication & Security](./authentication-and-security.md)**
   - JWT Bearer Tokens for Flutter & APIs
   - Password Hashing (Argon2id & Bcrypt)
   - Authorization Gates & Policies
   - Built-in Middlewares (CSRF, CORS, RateLimiter, SecureHeaders)

10. **[Stateful Sessions & Flash Messages (PRD §3.3)](./stateful-sessions-and-flash.md)**
    - AES-256-GCM Encrypted Cookie Sessions
    - Storage Drivers (Cookie, Memory, File)
    - One-Request Flash Messages (`session.Flash`)
    - Session-Backed CSRF Verification (`VerifyCSRFToken`)

11. **[Queues & Task Scheduling](./queues-and-scheduling.md)**
    - Async Background Jobs (`queue.Push`)
    - Scalable Worker Pools (`artisan queue:work`)
    - In-Process Fluent Cron Scheduler (`schedule.DailyAt`, `Hourly`)

11. **[Realtime WebSockets & SSE Broadcasting (PRD §5.3)](./realtime-broadcasting.md)**
    - Realtime Broadcasting Architecture (WebSockets vs SSE)
    - Live Ent Model Mutation Hooks (`EntMutationHook`)
    - Flutter and Browser Native Client Subscriptions

12. **[Multi-Channel Notifications Engine (PRD §7)](./notifications.md)**
    - Unified Notification Delivery (Email, In-App Database, Broadcast, Slack, SMS)
    - Notifiable Recipients & On-Demand Routing
    - Asynchronous Queued Notifications (`ShouldQueue`)
    - Generator Command (`artisan make:notification`)

13. **[Artisan CLI Reference](./artisan-cli.md)**
    - Project Creation (`artisan new`)
    - Database Migrations & Seeding (`migrate`, `migrate:diff`, `db:seed`, etc.)
    - Generator Commands (`make:schema`, `make:controller`, `make:job`, `make:notification`, etc.)
    - Build & Lifecycle Commands (`generate`, `flutter:build`, `serve`)

14. **[Performance & Benchmarks](./benchmarks.md)**
    - Zero-Allocation Architecture Methodology
    - Micro-benchmark Results vs Gin, Fiber, and Laravel Octane
