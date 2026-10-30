# Getting Started & Installation 🚀

GoLaravel requires Go 1.22+ and optionally Templ and Flutter depending on your chosen presentation stack.

---

## 1. Prerequisites

### Go
Verify your Go installation:
```bash
go version # Should be >= 1.22
```

### Templ CLI (Compiled Blade Engine)
Install the `templ` compiler binary:
```bash
go install github.com/a-h/templ/cmd/templ@latest
```

### Flutter (Optional for Multiplatform Clients)
If you intend to target Mobile (iOS/Android), Desktop (Windows/macOS/Linux), or Web with Flutter:
```bash
flutter --version
```

---

## 2. Installing the Artisan CLI

The Artisan CLI provides code generation, project scaffolding, and lifecycle tools.

Install globally to your `$GOPATH/bin`:
```bash
go install github.com/golaravel/golaravel/cmd/artisan@latest
```
Ensure your Go bin directory is in your system `PATH`:
```bash
export PATH=$PATH:$(go env GOPATH)/bin
```

---

## 3. Creating Your First Project

Run the interactive creation wizard:
```bash
artisan new my-saas-app
```

The interactive CLI will prompt you:
1. **Frontend / Presentation Stack**:
   - `[1] Templ (Blade-like SSR)` - High-speed compiled HTML for standard web apps.
   - `[2] Inertia.js (Modern Monolith)` - React / Vue without API boilerplate.
   - `[3] Flutter Multiplatform` - Full cross-platform client (Web, Mobile, Desktop).
   - `[4] Hybrid: Flutter + Templ Fast-SSR (Recommended)` - Rich Flutter app for dashboard/desktop/mobile + Templ for zero-JS customer invoices and public token links.
2. **Flutter Targets** (if Flutter or Hybrid is selected):
   - All Platforms (Web, Android, iOS, Windows, macOS, Linux).
3. **Database Driver**:
   - SQLite, PostgreSQL, or MySQL.

---

## 4. Directory Structure

A GoLaravel application follows a deterministic, AI-friendly structure:

```text
my-saas-app/
├── app/
│   ├── schema/           # Ent ORM database schemas (declarative Go)
│   ├── handlers/         # HTTP Controllers
│   ├── views/            # Templ component views (.templ)
│   ├── middleware/       # Custom application middlewares
│   ├── jobs/             # Async background queue jobs
│   ├── policies/         # Authorization policies
│   └── generate.go       # Ent code generation directive
├── client/               # Multiplatform Flutter project (if selected)
│   ├── lib/              # Flutter Dart code (Material 3 UI, API client)
│   └── pubspec.yaml
├── main.go               # Application entrypoint & route registration
├── go.mod
└── .env                  # Environment configuration
```

---

## 5. Running Your Application

```bash
# 1. Enter project directory
cd my-saas-app

# 2. Run code generators for Ent models and Templ views
artisan generate

# 3. Build Flutter Web assets (if using Flutter or Hybrid)
artisan flutter:build

# 4. Start the server
go run main.go
# Or with Artisan:
artisan serve
```

Your server will be running on `http://localhost:8080`.
