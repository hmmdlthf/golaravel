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

All generators place files in deterministic directories:

| Command | Output Path | Description |
|---|---|---|
| `artisan make:schema <Name>` | `app/schema/<name>.go` | Creates an Ent ORM schema definition |
| `artisan make:controller <Name>` | `app/handlers/<name>_controller.go` | Creates an HTTP controller |
| `artisan make:view <name>` | `app/views/<name>.templ` | Creates a Templ component view |
| `artisan make:job <Name>` | `app/jobs/<name>_job.go` | Creates an async background queue job |
| `artisan make:mail <Name>` | `app/mail/<name>.go` | Creates a fluent Mailable email class |
| `artisan make:policy <Name>` | `app/policies/<name>_policy.go` | Creates an authorization policy |
| `artisan make:middleware <Name>`| `app/middleware/<name>.go` | Creates custom HTTP middleware |

---

## 3. Build & Generation Commands

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

## 4. Server & Worker Commands

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
