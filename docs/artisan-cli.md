# Artisan CLI Reference 🛠️

The Artisan CLI is the command-line companion for GoLaravel applications, providing code generators, project scaffolding, and lifecycle management.

---

## 1. Project Management

### `artisan new <name>`
Starts the interactive project creation wizard. Prompts for:
- Presentation stack: Templ (Blade SSR), Inertia.js (React/Vue), Flutter, or Hybrid (Flutter + Templ).
- Flutter target platforms (Web, Windows, macOS, Linux, iOS, Android).
- Database driver (SQLite, PostgreSQL, MySQL).

```bash
artisan new my-billing-system
```

---

## 2. Generator Commands

### `artisan make:crud <Name> [--api | --web] [--force]` (PRD §8.2)

Scaffolds a complete full-stack CRUD resource end-to-end in seconds:
- **Ent Database Schema**: `app/schema/<name>.go`
- **Form Requests with Validation**: `app/requests/<name>_request.go`
- **RESTful & Web Controller**: `app/handlers/<name>_controller.go` (Index, Show, Create, Store, Edit, Update, Destroy)
- **Templ UI Views**: `app/views/<name>.templ` (Index Table, Show Detail, and Create/Edit Form with CSRF & method spoofing)
- **Database Seeder**: `database/seeders/<name>_seeder.go`
- **Route Mounting Helper**: `ctrl.RegisterRoutes(router, "/<names>")`

```bash
# Scaffold full-stack CRUD with Templ views
artisan make:crud Product

# Scaffold API-only CRUD (skip Templ views, JSON responses)
artisan make:crud Order --api

# Overwrite existing files
artisan make:crud Customer --force
```

---

### Individual Component Generators

All generators place files in deterministic directories:

| Command | Output Path | Description |
|---|---|---|
| `artisan make:crud <Name>` | Full Stack | Scaffolds Schema, Requests, Controller, Views, and Seeder |
| `artisan make:schema <Name>` | `app/schema/<name>.go` | Creates an Ent ORM schema definition |
| `artisan make:controller <Name>` | `app/handlers/<name>_controller.go` | Creates an HTTP controller |
| `artisan make:view <name>` | `app/views/<name>.templ` | Creates a Templ component view |
| `artisan make:job <Name>` | `app/jobs/<name>_job.go` | Creates an async background queue job |
| `artisan make:notification <Name>` | `app/notifications/<name>_notification.go` | Creates a multi-channel notification class |
| `artisan make:auth` | `app/handlers/auth_workflow_controller.go` | Scaffolds turnkey auth workflows & TOTP 2FA controller |
| `artisan make:mail <Name> [--view]` | `app/mail/<name>_mail.go` | Creates a Mailable struct with optional companion Templ view |
| `artisan make:policy <Name>` | `app/policies/<name>_policy.go` | Creates an authorization policy |
| `artisan make:scope <Name>` | `app/scopes/<name>_scope.go` | Creates a global Ent query scope |
| `artisan make:middleware <Name>`| `app/middleware/<name>.go` | Creates custom HTTP middleware |
| `artisan make:limiter <Name>`   | `app/limiters/<name>_limiter.go` | Creates a named sliding-window rate limiter |
| `artisan make:migration <Name>`| `database/migrations/<timestamp>_<name>.sql` | Creates a new Atlas versioned migration |
| `artisan make:factory <Name>`  | `database/factories/<name>_factory.go` | Creates an Ent model factory |
| `artisan make:seeder <Name>`   | `database/seeders/<name>_seeder.go` | Creates a new database seeder class |

---

## 3. Database Migration & Seeding Commands

| Command | Description |
|---|---|
| `artisan migrate` | Executes pending database migrations via Atlas |
| `artisan migrate:diff <name>` | Computes schema diff between Ent schemas and DB and creates a versioned SQL migration |
| `artisan migrate:status` | Inspects migration status and pending files |
| `artisan migrate:rollback` | Reverts the last migration batch |
| `artisan db:seed` | Seeds database records via `database/seeders` |

---

## 4. Build & Generation Commands

### `artisan generate`
Runs both `templ generate` and `go generate ./...` to compile:
- Ent ORM query builders, mutations, predicates, and edge traversals.
- Templ view components into pure Go functions.

```bash
artisan generate
```

### `artisan flutter:build`
Compiles the Flutter client application into `client/build/web` for direct hosting by GoLaravel:

```bash
artisan flutter:build
```

---

## 5. Server, Worker & Scheduler Commands

### `artisan serve`
Starts the HTTP development server on port 8080 (or custom port):

```bash
artisan serve
artisan serve 3000
```

### `artisan queue:work`
Launches concurrent background worker goroutines to process enqueued jobs:

```bash
artisan queue:work
```

### `artisan schedule:list`
Displays a formatted ASCII table of all registered scheduled tasks, cron expressions, next due times, timezones, and concurrency locks:

```bash
artisan schedule:list
```

### `artisan schedule:run`
Executes all tasks due at the current minute. Ideal for invoking from your server crontab (`* * * * *`):

```bash
artisan schedule:run
```

### `artisan schedule:work`
Runs an in-process schedule daemon that executes `schedule:run` every minute without needing external crond:

```bash
artisan schedule:work
```
