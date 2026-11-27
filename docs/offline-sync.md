# Offline-First Sync 📴 (`pkg/offline`)

Tills, field apps and kiosks have to keep working when the internet doesn't. `pkg/offline` is a sync engine for them. Devices read and write a local database and never wait for the network. When the server can be reached, they push their changes and pull everyone else's.

- **Server:** `pkg/offline`, with your Ent models.
- **Clients:** `@golaravel/offline` for browsers and Tauri ([`clients/offline`](../clients/offline/README.md)), and the Dart package `golaravel` for Flutter ([`clients/flutter`](../clients/flutter/README.md)). Both speak the same protocol.

```
 device                                          server
 ──────                                          ──────
 UI ─► local DB ─► outbox (append-only) ── push ─► apply each mutation in a transaction
                                                    + append it to the sync ledger
 UI ◄─ local DB ◄──────────────────────── pull ── entities changed since my cursor
```

What it guarantees:

- **Writes never wait.** A sale made offline is in the local database at once and in the outbox until the server acknowledges it.
- **Exactly once.** Every mutation has a client-generated ULID. The server records each one in an append-only ledger, so a batch re-sent after a dropped connection is acknowledged and not applied a second time.
- **Atomic groups.** A sale, its lines and its stock change can be sent as one group. The server applies all of them or none.
- **No lost updates.** Concurrent edits are settled field by field with hybrid logical clocks. Counters such as stock take deltas that add up across devices.
- **No missed changes.** Pull cursors are per-tenant sequence numbers that commit in order, so a change that commits late is never skipped.
- **Tenant isolation.** Ledger, cursors, devices and idempotency are all per tenant, on top of your `privacy` scopes.
- **Optional end-to-end payload encryption**, with a key per device.

---

## 1. Quick start

### 1.1 Scaffold a synced entity

```bash
artisan make:syncable Product name:string sku:string price:int64 stock:counter
artisan generate
```

This creates three things:

- **`app/schema/product.go`:** the Ent schema, with `offline/schema.SyncableMixin` (a client-generated string `id`, `sync_version`, `sync_hlc`, per-field clocks), `privacy.TenantMixin` and `privacy.SoftDeleteMixin` (deletes leave tombstones that devices can pull).
- **`app/synced/product.go`:** an `offline.Resource` that loads, snapshots and applies products through your Ent client.
- **`--feature sql/execquery`** added to `app/generate.go`. The engine needs it to write its ledger in the same transaction as your entity.

Field types: `string`, `text`, `int`, `int64`, `float`, `bool`, `time` and `counter`. A `counter` is a float that clients change with deltas. The generated files are a starting point: add validation, rules or another resolver.

### 1.2 Add the sync tables

Either let Ent manage them, by embedding the schemas so Atlas migrations include them:

```go
// app/schema/sync.go
package schema

import offline "github.com/golaravel/golaravel/pkg/offline/schema"

type (
	SyncLedger struct{ offline.SyncLedger }
	SyncDevice struct{ offline.SyncDevice }
	SyncTenant struct{ offline.SyncTenant }
)
```

or call `eng.Migrate(ctx)` (SQLite, MySQL, Postgres), which creates them with `CREATE TABLE IF NOT EXISTS`. `Options.Prefix` only applies to `Migrate`.

### 1.3 Create the engine and mount the routes

```go
eng, err := offline.New(offline.Options[*app.Tx]{
	Begin:   func(ctx context.Context) (*app.Tx, error) { return client.Tx(ctx) },
	Dialect: database.Postgres,
	Keys:    []offline.ServerKey{syncKey}, // optional: payload encryption (§6)
})

eng.Register(synced.Product())
client.Product.Intercept(privacy.TenantScope(func(q *app.ProductQuery, t string) { q.Where(product.TenantID(t)) }))
client.Product.Use(privacy.TenantMutationHook(), offline.ChangeHook(eng, "product"))

api := app.Group("/api/sync",
	auth.BearerGuard(jwt),
	privacy.TenantMiddleware(privacy.JWTClaimResolver("tenant_id")),
)
eng.Routes(api)

schedule.Call(eng.CompactTask(offline.CompactOptions{})).Daily() // §8
```

### 1.4 Use it from a client

```ts
import { SyncClient } from "@golaravel/offline";

const sync = await SyncClient.open({
  url: "/api/sync",
  entities: ["product", "sale", "sale_line"],
  headers: () => ({ Authorization: `Bearer ${token}` }),
  encrypt: true,
});

await sync.batch((b) => {
  const sale = b.create("sale", { total: 900, sold_at: new Date().toISOString() });
  b.create("sale_line", { sale_id: sale, product_id: teaId, qty: 2, price: 450 });
  b.increment("product", teaId, "stock", -2);
});
```

---

## 2. Resources

A `Resource` tells the engine how to read and write one entity type. `T` is your transaction type: `*app.Tx` with Ent, or `offline.SQLTx` with plain `database/sql` (`offline.BeginSQL(db)`).

| Field | Required | What it does |
|---|---|---|
| `Name` | yes | The entity name clients use (`"product"`). |
| `Load(ctx, tx, id)` | yes | The current row as a `Record`, or `nil` if it doesn't exist. Soft-deleted rows come back as tombstones (`Deleted: true`): load them with `privacy.WithTrashed(ctx)`. |
| `Apply(ctx, tx, w)` | yes | Writes a resolved change. `w.Op` is `OpCreate` (also used to restore a tombstone, when `w.Current` is set), `OpUpdate` or `OpDelete`. Store `w.Version`, `w.HLC` and `w.FieldClocks` on the row. |
| `Snapshot(ctx, tx, after, limit)` | no | Live rows ordered by id, for a new device's first load and for resyncs. |
| `LoadMany` | no | A batch form of `Load` for pulls. Without it, pulls call `Load` once per row. |
| `Fields` | — | The fields clients may set. Leave it empty only on purpose: empty means any field. `id`, `tenant_id`, `deleted_at` and `sync_*` are always refused. |
| `Counters` | — | The fields clients may change with `inc` deltas. |
| `Resolver` | — | Conflict strategy (§5). Defaults to `FieldLWW`. |
| `Authorize(ctx, p, m, current)` | — | Return an error to refuse a mutation. It comes back to the client as `rejected` (`forbidden: …` unless you return `offline.Reject`). |

Errors from `Apply`:

- `offline.Reject(code, err)` / `offline.Rejectf(code, …)`: a **permanent** failure, such as validation. The client drops the change and keeps it as a dead letter.
- **Transient** errors (lost connection, deadlock, `database is locked`, serialization failure, lock timeout) make the client retry later, in order. `Options.Classify` decides which is which.
- Anything else counts as permanent. The client sees only `apply_failed`, and the real error is logged, so SQL never reaches devices.

### Rules on top of generated resources

The example's POS (`example/app/pos`) wraps the generated resources rather than editing them:

```go
sales := synced.Sale()
sales.Authorize = func(_ context.Context, _ offline.Principal, m *offline.Mutation, cur *offline.Record) error {
	if m.Op != offline.OpCreate || (cur != nil && !cur.Deleted) {
		return offline.Rejectf("immutable", "sales cannot be changed once made")
	}
	return nil
}
eng.Register(sales)
```

### Changes made outside sync

Changes from the admin panel, background jobs or other APIs must reach devices too. `offline.ChangeHook(eng, "product")`:

- records every write to the entity in the ledger, inside the same Ent transaction when there is one;
- stamps the row with the server clock, so an older offline edit can't overwrite it;
- skips the engine's own writes.

For raw SQL or bulk imports, call `eng.Record(ctx, tx, tenant, "product", offline.OpUpdate, ids...)` in the transaction that made the change.

---

## 3. Protocol

All endpoints sit behind your auth and tenant middleware. `Options.Identify` decides who the caller is. The default reads the tenant from `privacy.TenantMiddleware` and the user from the auth guards. Requests after registration carry the device in `X-Sync-Device`.

| Endpoint | Request | Response |
|---|---|---|
| `POST /devices` | `{name, platform, app_version, public_key?}` | `Device` (and `server_key` when encrypting) |
| `GET /devices/current` | | `Device` |
| `DELETE /devices/current` | | `204`; the device is revoked |
| `POST /push` | `{mutations: [...]}`, oldest first | `{results: [...], server_hlc}`, one result per mutation |
| `GET /pull?cursor=&limit=` | | `{changes, cursor, has_more, server_hlc}` |
| `GET /snapshot?entity=&after=&limit=` | | `{records, next, has_more, cursor}` |

A mutation:

```json
{ "id": "01JD…", "entity": "product", "entity_id": "01JC…", "op": "update",
  "set": {"price": 450}, "inc": {"stock": -2}, "hlc": "001732700000000:00000:dev-1",
  "group": "sale-01JD…", "base_version": 7 }
```

### Push results

| Status | Meaning | Client action |
|---|---|---|
| `applied` | Done as sent | acknowledge |
| `resolved` | Done after conflict resolution (`reason`: `stale_fields`, `clock_skew`, `deleted`, `superseded`, …). `record` holds the server's state | acknowledge, take `record` |
| `duplicate` | Seen before; `original` is how it went | acknowledge as `original` |
| `rejected` | Permanent failure (`reason`). `record` holds the server's state | drop, undo locally, keep as a dead letter |
| `retry` | Transient failure, or blocked behind one (`reason: blocked`) | send again later, in order |

Each mutation runs in its own transaction. Adjacent mutations that share a `group` run in one transaction: if one member is rejected, every member is (`group_rejected: …`). A transient failure stops the batch at that point, so mutations are never applied out of order.

### Pull and snapshot

A pull returns each changed entity once, in its current state, or as a tombstone if it was deleted or is no longer visible. Results are ordered by the latest change. Each tenant has a sequence counter, locked by every ledger write. Sequence numbers therefore commit in order, and a pull can't skip a change that commits late.

A new device loads each entity with `snapshot`. The first page returns the cursor to start pulling from. Changes made while it pages through are pulled again afterwards, which is harmless.

| Error | Meaning |
|---|---|
| `401 unauthenticated`, `403 no_tenant` | auth or tenant middleware failed |
| `403 unknown_device` / `reregister` | register again (the server key was retired) |
| `403 device_revoked` | the device was signed out: stop |
| `403 encryption_required`, `400 bad_envelope` | §6 |
| `410 resync_required` | the cursor is older than retained history (§8): snapshot again |
| `413 batch_too_large` | over `MaxBatch` (500) mutations or `MaxBody` (4 MiB) |
| `503 unavailable` + `Retry-After` | transient: back off |

---

## 4. Ids, clocks and versions

- **Ids:** clients generate entity and mutation ids (ULIDs, or UUIDv7 for mutations). The server never assigns ids, so records created offline keep their id.
- **Clocks:** every mutation carries a **hybrid logical clock** timestamp, `"<15-digit ms>:<5-digit counter>:<node>"`, which sorts correctly as text. The server merges device clocks into its own.
- **Clock skew:** a device clock more than `MaxDrift` (5 min) ahead of the server's is replaced by server time (`resolved: clock_skew`). Without this, one till with a wrong clock would win every conflict for days.
- **Versions:** `sync_version` counts changes to a row. Clients send the version they saw (`base_version`), which `ServerWins` uses.

---

## 5. Conflicts

| Resolver | Behaviour |
|---|---|
| `FieldLWW` (default) | Per field, the newest timestamp wins. Two devices editing different fields both keep their change. |
| `LWW` | Whole-row last-write-wins: an older mutation's fields are all dropped. |
| `ServerWins` | The client's fields are dropped if the row changed after `base_version`. |
| `ClientWins` | Whatever arrives last wins. |
| `offline.ResolverFunc(...)` | Your own rule: return a `Decision` (fields to write, deltas, `Skip`, a `Note`) or `offline.Reject`. |

All strategies:

- add up `inc` deltas, since deltas commute: two tills selling 2 and 3 offline end at stock − 5, whichever syncs first;
- treat deletes the same way, as below.

| Incoming | Row state | Result |
|---|---|---|
| update | deleted | skipped (`deleted`): deletes win over edits |
| create | deleted, older than the create | restored |
| delete | missing or deleted | skipped (`already_deleted`) |
| delete | edited after the delete was made | skipped (`superseded`) |

---

## 6. Payload encryption

HTTPS protects sync traffic in transit. Payload encryption adds a layer for sensitive data: TLS-terminating proxies, request logs and middleware only see ciphertext, and only the device holding its private key can make requests.

```go
key, _ := offline.GenerateServerKey("2026-11")      // once; store key.Encode() as a secret
key, _ = offline.ParseServerKey("2026-11", os.Getenv("OFFLINE_SYNC_KEY"))

offline.New(offline.Options[*app.Tx]{ /* … */ Keys: []offline.ServerKey{key}, RequireEncryption: true })
```

- **Key exchange:** a client with `encrypt: true` sends a P-256 public key at registration and gets the server's in return. Both derive an AES-256-GCM key: ECDH, then HKDF-SHA256 salted with the device id.
- **Envelopes:** push bodies and push, pull and snapshot responses become envelopes, `{v, kid, device_id, iv, ct}`. The authenticated data binds each one to its direction, endpoint, tenant and device, so an envelope can't be replayed anywhere else. Replaying a push to the same endpoint is harmless, because pushes are idempotent.
- **Plaintext refused:** an encrypting device that sends plaintext gets `403 encryption_required`.
- **Required encryption:** `RequireEncryption` refuses devices without a key.
- **Rotation:** put the new key first and keep old ones listed. New devices use the first key. When a key is removed, its devices get `403 reregister` and register again, without losing their outbox.
- **Where keys live:** the TypeScript client keeps keys as non-extractable `CryptoKey`s in IndexedDB. The Flutter client uses the platform keystore. The server derives each device's key on demand and doesn't store it.

---

## 7. Devices

`Engine.Devices(ctx, tenant)` lists devices for an admin screen: name, platform, user, last push and pull, cursor. `Engine.RevokeDevice(ctx, tenant, id)`, or `DELETE /devices/current` from the device itself, signs a device out. Its key is deleted, and its next request gets `403 device_revoked`.

---

## 8. Compaction

The ledger grows with every change. `Engine.Compact` deletes rows that are both:

- older than `Retain` (30 days by default), and
- at or below the oldest cursor among active devices, meaning devices that pulled within `ActiveWithin` (30 days).

It raises the tenant's *horizon* first. A device whose cursor is below the horizon gets `410` and reloads with a snapshot. Pull reads the horizon after reading changes, so a pull running during compaction either sees everything or resyncs.

```go
schedule.Call(eng.CompactTask(offline.CompactOptions{Retain: 60 * 24 * time.Hour})).Daily()

registry.Register(&artisan.Command{Name: "offline:compact", Run: eng.CompactCLI}) // --retain=720h --tenant=acme
```

> ⚠️ Ledger rows are also the record of which mutations were already applied. Set `Retain` longer than any device can hold a mutation it pushed without seeing the acknowledgment. A mutation retried after its row is gone is applied again.

---

## 9. The example point of sale

`example/` contains a small offline till:

- products, sales and sale lines, generated with `make:syncable`;
- shop rules in `example/app/pos`: sales are immutable, and prices and quantities can't be negative;
- a demo page at `/pos`.

```bash
cd clients/offline && npm install && npm run build && cd ../..
go run ./example
# open http://localhost:8080/pos, sign in as admin@golaravel.dev / password123
```

Tick **Simulate offline**, sell a few items, then untick it: the outbox drains and stock updates on every tab and device. `example/pos_test.go` runs the same flow over HTTP.

---

## 10. Testing

- **Engine tests:** `go test ./pkg/offline/` uses a generated Ent fixture. It covers idempotency, groups, resolvers, clock skew, tenant isolation, concurrent pushes against a pull, encryption, rotation and compaction.
- **Database tests:** `integration/offline_test.go` runs the plain-SQL path on SQLite, MySQL and Postgres.
- **Client tests:** both clients have unit tests against a fake server. Their end-to-end suites (`npm run test:e2e`, `GOLARAVEL_E2E=1 flutter test`) run against the real Go engine, in plaintext and encrypted mode.
