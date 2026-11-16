# Tagged Cache Engine & Atomic Locks ⚡️ (PRD §7)

GoLaravel includes an ultra-fast, high-concurrency caching engine with **Laravel-grade Tagged Cache Invalidation** and **Atomic Concurrency Locks**, backed by a unified `Store` interface.

---

## 1. Basic Cache Operations

The global `Cache` facade provides fluent methods for storing and retrieving values:

```go
import (
	"time"
	"github.com/golaravel/golaravel"
)

// 1. Storing items:
_ = golaravel.Cache.Put("user_count", 1500, 30*time.Minute)

// Add only if missing:
added, _ := golaravel.Cache.Add("lock_key", "value", 10*time.Minute)

// 2. Reading items:
count := golaravel.Cache.GetInt("user_count") // 1500
name := golaravel.Cache.GetString("site_name", "Default Name")

// 3. Atomic Increment & Decrement:
newTotal, _ := golaravel.Cache.Increment("visitors")      // increments by 1
newScore, _ := golaravel.Cache.Increment("score", 10)     // increments by 10
_ = golaravel.Cache.Decrement("stock", 1)                 // decrements by 1

// 4. Remember pattern (Retrieve or execute callback and cache):
val, err := golaravel.Cache.Remember("monthly_revenue", 1*time.Hour, func() (any, error) {
	return calculateMonthlyRevenueFromDatabase()
})

// Indefinite caching:
res, err := golaravel.Cache.RememberForever("system_settings", func() (any, error) {
	return loadSettings()
})

// 5. Pull (Retrieve and delete):
code := golaravel.Cache.Pull("verification_code")

// 6. Forget or Flush:
_ = golaravel.Cache.Forget("user_count")
_ = golaravel.Cache.Flush() // clears entire store
```

---

## 2. Tagged Cache Invalidation (`Cache.Tags`)

Tagged caching enables grouping related cached items under one or more tags. Flushing a tag invalidates **all** items associated with that tag in $O(1)$ constant time using versioned namespaces.

### Tagging and Storing Items:
```go
// Tagging an item with multiple tags:
_ = golaravel.Cache.Tags("invoices", "tenant_42").Put("inv_1001", invoiceData, 2*time.Hour)

// Storing reports under different tags:
_ = golaravel.Cache.Tags("reports", "tenant_42").Put("q1_summary", reportData, 24*time.Hour)
```

### Retrieving Tagged Items:
Tag order does not matter:
```go
val, found := golaravel.Cache.Tags("invoices", "tenant_42").Get("inv_1001")
// Or in reverse tag order:
val, found := golaravel.Cache.Tags("tenant_42", "invoices").Get("inv_1001")
```

### Invalidating by Tag:
Flushing a single tag purges all items associated with it, leaving unrelated tags intact:

```go
// 1. Invalidate all invoices for all tenants:
_ = golaravel.Cache.Tags("invoices").Flush()
// 'inv_1001' is immediately invalidated!
// 'q1_summary' under 'reports' remains fully cached!

// 2. Invalidate all cached data for tenant 42:
_ = golaravel.Cache.Tags("tenant_42").Flush()
// All invoices and reports for tenant 42 are invalidated in O(1)!
```

### Tagged Remember:
```go
data, err := golaravel.Cache.Tags("analytics").Remember("active_users", 15*time.Minute, func() (any, error) {
	return computeActiveUsers()
})
```

---

## 3. Atomic Concurrency Locks (`Cache.Lock`)

Atomic locks prevent race conditions across parallel requests, background queue workers, and scheduled cron jobs (e.g. preventing duplicate invoice generation or double charges).

### 1. Execute Callback with Auto-Release (`Lock.Get`):
Acquires the lock, executes the callback, and automatically releases the lock upon completion (even if an error or panic occurs):

```go
lock := golaravel.Cache.Lock("process-invoice-1001", 10*time.Second)

err := lock.Get(func() error {
	// Critical section: only 1 worker can execute this at a time
	return processInvoicePayment(1001)
})
if err == golaravel.ErrLockNotAcquired {
	// Another worker is already processing this invoice
}
```

### 2. Blocking Until Available (`Lock.Block`):
Waits up to a specified timeout for the lock to become available, retrying with exponential backoff:

```go
lock := golaravel.Cache.Lock("daily-inventory-sync", 30*time.Second)

// Wait up to 5 seconds to acquire the lock:
err := lock.Block(5*time.Second, func() error {
	return syncInventory()
})
if err == golaravel.ErrLockTimeout {
	// Lock could not be acquired within 5 seconds
}
```

### 3. Manual Acquire and Release:
```go
lock := golaravel.Cache.Lock("resource-key", 10*time.Second)

acquired, err := lock.Acquire()
if err == nil && acquired {
	defer lock.Release()
	// Process resource...
}
```

### Concurrency Safety Guarantees:
- **Owner Token Tracking**: Each lock instance is assigned a unique cryptographic owner token (`lock.Owner()`). A process can **never** release a lock that expired and was subsequently acquired by another worker (`ErrLockNotOwned`).
- **TTL Auto-Expiration**: If a worker crashes while holding a lock, the lock automatically expires after the configured duration, preventing permanent deadlocks.

## Shared cache stores (SQL and Redis)

`cache.NewMemoryStore` is per process. The SQL and Redis stores implement the same `cache.Store`
API, so every instance sees the same values, tag flushes and locks.

```go
// Redis: keys live under "golaravel:cache:" (Flush only deletes those).
rc, _ := redis.New(redis.Config{Addr: "127.0.0.1:6379"})
cache.SetDefaultStore(cache.NewRedisStore(rc))

// SQL (SQLite, MySQL, Postgres): tables "cache" and "cache_locks".
store, backend, err := cache.NewSQLStore(db, database.MySQL)
_ = backend.Migrate(ctx)
cache.SetDefaultStore(store)
// Expired rows are ignored on read; delete them now and then:
_ = backend.Prune(ctx)
```

Things to know:

- **Values are stored as JSON.** `Get` returns strings, bools, `int64` (whole numbers), `float64`,
  `map[string]any` and `[]any`. Use the typed getters (`GetInt`, `GetString`, ...) or decode into
  your own types.
- **Tags:** tag versions are kept in the store, so `Tags(...).Flush()` reaches every instance.
- **Locks:** taken atomically in Redis (`SET NX`) and in SQL (unique row). A lock expires after
  its duration.
