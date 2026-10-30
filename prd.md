# Product Requirements Document (PRD): Go Full-Stack Framework ("GoLaravel") with Ent ORM Integration

## Executive Summary & Vision

"GoLaravel" is an opinionated, batteries-included web framework for Go that pairs the developer velocity and architectural elegance of Laravel with the compiled performance, low resource consumption, and concurrent runtime of Go.

Rather than relying on runtime reflection for database operations, this revision integrates **Ent** as the core ORM layer. By adopting Ent’s schema-as-code model and compile-time code generation, the framework provides:

* 100% type-safe graph and relation traversal.
* Predictable, machine-readable schemas that optimize AI-driven code generation.
* Zero-reflection, high-throughput execution suitable for data-heavy administrative and enterprise CRUD tools.

---

## 1. Core Architecture & Foundations

### 1.1 Dependency Injection & Service Container

* **IoC Container**: Type-safe service container supporting Singletons, Transient bindings, and Scoped dependencies per HTTP request.
* **Ent Client Injection**: The generated `*ent.Client` is registered into the container as a thread-safe singleton, injected into handlers, middleware, and background jobs.
* **Service Providers**: Uniform lifecycle hooks (`Register()`, `Boot()`) enabling plug-and-play modular packages.
* **Zero-Reflection Fast Path**: Uses Go generics (`Container.Resolve[T]()`) for type-safe dependency retrieval with compile-time checks.

### 1.2 Configuration Management

* **Hierarchical Config**: Multi-environment configuration support (`.env`, YAML, or struct-based) with automatic type-casting.
* **Runtime Reloading**: Hot-reloading of non-critical configs in development; immutable compilation in production.

---

## 2. Database & Data Layer (Powered by Ent)

### 2.1 Schema-as-Code Engine

* **Declarative Go Schemas**: All data models are authored directly in pure Go under `app/schema/`:
```go
package schema

import (
    "entgo.io/ent"
    "entgo.io/ent/schema/field"
    "entgo.io/ent/schema/edge"
)

type User struct {
    ent.Schema
}

func (User) Fields() []ent.Field {
    return []ent.Field{
        field.String("email").Unique().NotEmpty(),
        field.String("password").Sensitive(),
    }
}

func (User) Edges() []ent.Edge {
    return []ent.Edge{
        edge.To("posts", Post.Type),
    }
}

```


* **Code Generation Pipeline**:
* Integrated `go generate ./...` runner via framework CLI (`artisan generate`).
* Generates type-safe builders for Querying, Creation, Mutation, and Deletion.
* Generates full predicate engines (e.g., `user.EmailEQ("test@example.com")`).



### 2.2 Relationship Traversal (Graph-Based)

* **Edges Over Foreign Keys**: Explicit declaration of relations (`O2O`, `O2M`, `M2O`, `M2M`).
* **Fluent Graph Traversal**: Direct traversal across boundaries without manual joins:
```go
// Equivalent to Laravel: User::with('posts.comments')->get()
users, err := client.User.
    Query().
    WithPosts(func(q *ent.PostQuery) {
        q.WithComments()
    }).
    All(ctx)

```



### 2.3 Migration, Hooks & Privacy Scopes

* **Atlas Integration**: Uses Ent’s default Atlas-backed migration engine for declarative database diffing, versioned migrations, and automated schema synchronization.
* **Schema Hooks (Lifecycle Events)**: Equivalent to Laravel model observers (`BeforeCreate`, `AfterUpdate`) implemented as Ent Mutators:
```go
func (User) Hooks() []ent.Hook {
    return []ent.Hook{
        hook.On(HashPasswordHook(), ent.OpCreate|ent.OpUpdateOne),
    }
}

```


* **Privacy Layer (Global Scopes / Soft Deletes)**: Native Ent privacy policies that enforce tenant isolation or soft deletes at the query engine level.

---

## 3. HTTP Routing, Controllers & Form Requests

### 3.1 Router & Dispatcher

* **High-Performance Multiplexer**: Radix-tree router with zero-allocation path matching.
* **Route Groups & Namespaces**: Prefixing, subdomain routing, middleware stacking, and named routes (`route.Name("users.show").URL(id)`).
* **Route Model Binding**: Automatic model resolution via route parameters:
* Route: `/users/{user}`
* Resolves target entity via `client.User.Get(ctx, id)` and injects the typed entity into the handler context.



### 3.2 Form Requests & Validation

* **Form Request Abstraction**: Decoupled request structs with automatic binding (JSON, multipart forms, query strings).
* **Ent-Aware Validation**: Validation rules integrated with Ent predicates (e.g., `exists:users,id`, `unique:users,email`).
* **Automatic Error Bags**: Structured JSON `422 Unprocessable Entity` responses matching Laravel's validation error formatting.

### 3.3 Middleware Pipeline

* **Standardized Pipeline**: Pre/post execution wrappers (`func(ctx Context) error`).
* **Included Middleware**:
* CSRF verification with encrypted cookie exchange.
* CORS configuration.
* Stateful session management (Cookie, Redis, Database).
* Rate limiting / Throttling (Token Bucket via Redis or In-Memory).



---

## 4. Authentication, Authorization & Security

### 4.1 Authentication Scaffolding

* **Multi-Guard Auth**: Pluggable guards supporting Session-based auth, Bearer Tokens (JWT), and API keys.
* **Password Security**: Argon2id and Bcrypt hashing with automated work-factor upgrades.
* **Pre-Packaged Flows**: Out-of-the-box endpoints for login, registration, password resets, email verification, and TOTP two-factor authentication.

### 4.2 Authorization (Gates & Policies)

* **Gates**: Closure-based permission checks (`gate.Allows("edit-settings", user)`).
* **Ent Policies**: Model-level authorization implemented directly via Ent privacy rules or handler-level Policy structs (`View`, `Create`, `Update`, `Delete`).

---

## 5. Async Processing, Events & Scheduling

### 5.1 Queues & Jobs

* **Unified Queue Interface**: Multi-driver support for Redis, Database (PostgreSQL/MySQL), SQS, and In-Memory channels.
* **Ent-Safe Serialization**: Automatic serialization of entity IDs rather than full structs, with automatic re-querying inside worker goroutines.
* **Worker Pool**: Scalable goroutine worker pool with retry backoffs, DLQ routing, and graceful shutdown handling.

### 5.2 Task Scheduler (Cron Engine)

* **Fluent Go Scheduler**: In-process scheduler replacing external crontab files:
```go
schedule.Job(&PruneExpiredTokensJob{}).DailyAt("00:00")
schedule.Command("ent:migrate:check").Hourly()

```



### 5.3 Events & Broadcasting

* **Event Dispatcher**: Synchronous and asynchronous event/listener pipelines.
* **Real-time Broadcasting**: WebSocket push over Centrifugo or Pusher-compatible protocols for Ent model mutation hooks.

---

## 6. Template Engine & Presentation

### 6.1 Server-Side Rendering ("GoBlade")

* **Blade-Inspired Engine**: Compiled templates with template inheritance (`@extends`), components (`<x-table>`), and directives.
* **Hot-Reloading in Dev**: Template change re-rendering without recompiling the Go binary.

### 6.2 Inertia.js Adapter

* **Modern Monolith Protocol**: Native Inertia adapter enabling React, Vue, or Svelte frontends without maintaining a REST/GraphQL boilerplate layer.
* **Ent Data Serialization**: Seamless JSON marshalling of generated Ent entities into Inertia view props.

---

## 7. Storage, Mail, Notifications & Caching

| Component | Laravel Equivalent | Go Implementation Specification |
| --- | --- | --- |
| **Filesystem** | Flysystem / Storage | Multi-driver interface supporting Local Disk, Amazon S3, MinIO, and Google Cloud Storage. |
| **Mail** | Mailable / Mailgun | Fluent mail builder supporting SMTP, SendGrid, SES, and Markdown/HTML email templates. |
| **Notifications** | Notification Channels | Multi-channel delivery system (Email, SMS via Twilio, Slack webhooks, Database in-app alerts). |
| **Cache** | Cache Facade | Unified driver interface for In-Memory (Ristretto/BigCache), Redis, and Memcached with tag-based invalidation. |

---

## 8. CLI Tooling & AI-First Ergonomics ("Artisan for Go")

### 8.1 Command Line Interface

* **Generator Commands**:
* `artisan make:schema <Name>`: Creates a new Ent schema definition.
* `artisan make:controller <Name>`: Generates an Ent-aware CRUD controller.
* `artisan make:job <Name>`: Creates a background job with serialization helpers.
* `artisan make:policy <Name>`: Generates authorization logic.


* **Lifecycle Commands**:
* `artisan generate`: Runs `ent generate` to rebuild all type-safe entities and builders.
* `artisan migrate`: Runs Atlas schema diffing and applies database updates.
* `artisan queue:work`: Launches background worker goroutines.



### 8.2 AI-Optimized Code Contracts

* **Exhaustive Go Interfaces & Generated Graph API**: Because Ent generates explicit Go files for every method, field, and predicate, LLMs have full code-completion context and do not hallucinate dynamic method names (unlike traditional dynamic ORMs).
* **Deterministic Directory Structure**: Strict module paths (`app/schema`, `app/handlers`, `app/jobs`, `app/policies`) ensure AI tools target exact locations when generating features.

---

## 9. Non-Functional Requirements & Performance Targets

* **Binary Footprint**: Standalone compiled single binary under 40 MB (including generated Ent assets).
* **Cold Start Time**: Under 50ms from start to listening on socket.
* **Base Memory Idle Overhead**: Under 30 MB RAM.
* **Throughput Target**: Sustained $\ge 40,000$ requests/sec on basic I/O and JSON ping on a standard 2-core / 2 GB RAM node. Zero runtime reflection in data queries
