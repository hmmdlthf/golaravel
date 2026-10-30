# GoLaravel ⚡️

The fastest full-stack web framework in the world — combining the developer ergonomics and architectural elegance of **Laravel** with the compiled performance, zero-reflection execution, and concurrency of **Go**.

---

## 🚀 Key Highlights & Architectural Pillars

| Component | Architecture | Performance Benefit |
|---|---|---|
| **View Engine** | **Templ** (`.templ`) | **25.6x faster** than standard `html/template` (44.8 ns/op, **0 B/op, 0 allocs/op**) |
| **HTTP Router** | Zero-alloc Radix Trie | **8.6 ns** static matching, **27.9 ns** deep param matching (**0 allocs/op**) |
| **ORM Layer** | **Ent** (entgo.io/ent) | 100% type-safe graph traversal with **zero runtime reflection** |
| **IoC Container** | Generic-based DI | Type-safe resolution (`Resolve[T]`) at **24.5 ns/op, 0 allocs/op** |
| **JSON Engine** | SIMD/Fast JSON (`goccy/go-json`) | **2.2x faster** serialization with buffer streaming |
| **CLI Tool** | **Artisan for Go** | Scaffolds schemas, views, controllers, runs code generation |
| **Binary Footprint** | Single standalone binary | **27 MB** (including SQLite, Ent ORM, and Templ views) |

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

* Sustained throughput: **Over 4.6 million requests/second** on a single thread.
* **Zero allocations** on routing and template rendering.

---

## 🛠 Project Layout

```text
golaravel/
├── cmd/
│   └── artisan/            # The Artisan CLI tool for GoLaravel
├── pkg/
│   ├── golaravel/          # Application kernel & HTTP engine (sync.Pool context)
│   ├── container/          # Generic type-safe IoC container (Resolve[T])
│   ├── router/             # Zero-allocation Radix trie router
│   ├── context/            # Pooled request context (JSON, Templ, Forms)
│   ├── view/               # Templ helpers (Blade-like @csrf, @method)
│   ├── middleware/         # Recovery, Logger, CORS, CSRF, SecureHeaders, RateLimiter
│   ├── database/           # High-concurrency connection pool & transaction manager
│   └── artisan/            # Artisan command registry & generators
├── example/                # Full reference app (Ent ORM + Templ + Controllers)
│   ├── app/
│   │   ├── schema/         # Ent schemas (user.go)
│   │   ├── handlers/       # Controllers (user_controller.go)
│   │   └── views/          # Templ components (layout, home, users)
│   └── main.go             # Application entrypoint
└── benchmarks/             # Micro and end-to-end benchmark suite
```

---

## ⚡ Quickstart

### 1. Installation & Generation
```bash
# Generate Ent ORM models & Templ views
artisan generate
```

### 2. Scaffold New Components with Artisan
```bash
# Create an Ent database schema
artisan make:schema Post

# Create a Templ component view
artisan make:view posts

# Create a controller
artisan make:controller PostController

# Create custom middleware
artisan make:middleware AuthToken
```

### 3. Running the Example Server
```bash
go run ./example/main.go
```

Access the app at `http://localhost:8080`.
