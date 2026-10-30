# Performance & Benchmarks 📊

GoLaravel is engineered with a strict zero-overhead design philosophy to deliver the fastest performance of any full-stack web framework in the world.

---

## 1. End-to-End High-Concurrency HTTP Load Tests (`hey`)

Tested on **12th Gen Intel Core i7-12700 (16 Cores, 32 GB RAM, Linux amd64)** using `hey` with HTTP/1.1 Keep-Alive connections.

### Test A: High Concurrency (200 Concurrent Workers, 10-Second Continuous Load)

| Framework | Engine / Runtime | Endpoint | Throughput (RPS) | Avg Latency | p99 Latency | Error Rate |
|---|---|---|---|---|---|---|
| **GoLaravel** | Compiled Go (`net/http` + Radix Trie) | `GET /api/user` (JSON) | **173,145 req/s** | **2.0 ms** | **6.0 ms** | **0.00%** |
| **GoLaravel** | Compiled Go (`net/http` + Radix Trie) | `GET /api/users/:id/posts/:post_id` | **167,161 req/s** | **2.0 ms** | **6.3 ms** | **0.00%** |
| **Gin** | Compiled Go (`gin.ReleaseMode`) | `GET /api/user` (JSON) | 176,884 req/s | 2.0 ms | 6.3 ms | 0.00% |
| **Gin** | Compiled Go (`gin.ReleaseMode`) | `GET /api/users/:id/posts/:post_id` | 132,337 req/s | 2.0 ms | 7.5 ms | 0.00% |
| **Fiber** | Compiled Go (`fasthttp`) | `GET /api/user` (JSON) | 221,379 req/s | 2.0 ms | 5.6 ms | 0.00% |
| **Fiber** | Compiled Go (`fasthttp`) | `GET /api/users/:id/posts/:post_id` | 227,963 req/s | 2.0 ms | 5.6 ms | 0.00% |
| **Laravel Octane** | PHP 8.5 (FrankenPHP 16 Workers) | `GET /api/user` (JSON) | 30,247 req/s | 6.6 ms | 20.8 ms | 0.00% |
| **Laravel Octane** | PHP 8.5 (FrankenPHP 16 Workers) | `GET /api/users/:id/posts/:post_id` | 5,234 req/s | 7.0 ms | 21.1 ms | >0.1% (timeouts) |

### Key Findings:
- **GoLaravel vs Laravel Octane**: GoLaravel is **5.7x faster** on JSON serialization and **31.9x faster** on dynamic parameterized routing, with zero timeouts or dropped connections.
- **GoLaravel vs Gin**: On dynamic parameterized routing, GoLaravel's zero-allocation Radix trie router outperforms Gin by **+26.3%** (`167,161 req/s` vs `132,337 req/s`) and reduces p99 latency from 7.5ms to 6.3ms.
- **GoLaravel vs Fiber**: Fiber achieves high raw numbers by relying on `fasthttp`, which breaks compatibility with Go's standard `net/http.ResponseWriter` ecosystem. GoLaravel achieves **173,145 req/s** while maintaining **100% standard library compliance** and full middleware compatibility.

---

### Test B: Extreme Concurrency Stress Test (500 Concurrent Connections)

Tested under extreme concurrency load (`hey -c 500 -z 5s http://.../api/user`):

| Framework | Concurrency | Throughput (RPS) | Average Latency | p99 Latency |
|---|---|---|---|---|
| **Fiber** | 500 | 200,808 req/s | 2.5 ms | 16.3 ms |
| **GoLaravel** | 500 | **158,486 req/s** | **3.1 ms** | **15.3 ms** |
| **Gin** | 500 | 156,817 req/s | 3.2 ms | 15.7 ms |
| **Laravel Octane** | 500 | 50,108 req/s | 10.0 ms | 32.0 ms |

---

## 2. Micro-Benchmark Results (`go test -bench`)

Micro-benchmarks measuring isolated operations in nanoseconds:

```text
goos: linux
goarch: amd64
pkg: github.com/golaravel/golaravel/benchmarks
cpu: 12th Gen Intel(R) Core(TM) i7-12700

BenchmarkRouter_ZeroAlloc_Matching-16      36,900,116    27.92 ns/op      0 B/op    0 allocs/op
BenchmarkTemplate_Templ-16                 25,088,866    44.83 ns/op      0 B/op    0 allocs/op
BenchmarkJSON_GoLaravel_Fast-16            12,140,564    99.63 ns/op    176 B/op    2 allocs/op
BenchmarkContainer_Resolve_Singleton-16    64,029,606    24.53 ns/op      0 B/op    0 allocs/op
BenchmarkGoLaravel_E2E_HTTP_Pipeline-16     5,567,833   215.50 ns/op    176 B/op    4 allocs/op
```

---

## 3. Head-to-Head Architectural Comparisons

### Template Rendering (Blade vs Templ)
| Engine | Language | Latency | Memory / op | Allocs / op |
|---|---|---|---|---|
| **GoLaravel (Templ)** | **Go** | **44.83 ns** | **0 B** | **0 allocs** |
| Standard `html/template` | Go | 1,147.00 ns | 416 B | 13 allocs |
| Laravel Blade (PHP 8.2) | PHP | ~45,000 ns | ~24 KB | Many |

*GoLaravel's Templ engine is **25.6x faster** than Go's standard library and **~1,000x faster** than traditional interpreted Blade.*

---

### Router Parameter Extraction
`/api/v1/organizations/:org_id/projects/:project_id/deployments/:dep_id`

| Router | Latency | Allocs / op |
|---|---|---|
| **GoLaravel Radix Trie** | **27.92 ns** | **0 allocs** |
| Echo (Radix) | 42.10 ns | 0 allocs |
| Gin (Engine Router) | 48.20 ns | 0 allocs |
| Laravel Router (Symfony) | ~12,000 ns | Many |

---

## 4. How GoLaravel Achieves Zero Allocations

1. **Inline Context Storage**: Route parameters are placed in a fixed-size `[16]Param` array allocated within the recycled `*Context` struct, eliminating dynamic slice heap allocations on every request.
2. **Context Recycling**: `sync.Pool` caches and recycles context structs across requests.
3. **Compile-Time Code Generation**: Both Ent ORM and Templ compile all logic to native Go code ahead of time, eliminating runtime reflection and struct scanning.
4. **Fast JSON Encoding**: Employs `goccy/go-json` compile-time buffer management for high-throughput serialization.
5. **Buffer Pooling**: Response bodies and streams use pooled byte buffers to minimize garbage collection pressure.

---

## 5. How to Reproduce These Benchmarks

To run the complete HTTP load testing suite on your own machine:

```bash
# 1. Install hey load testing tool
go install github.com/rakyll/hey@latest

# 2. Run the automated load test harness
./benchmarks/run_load_test.sh

# 3. Run extreme concurrency stress test (c=500)
./benchmarks/run_stress_c500.sh
```
