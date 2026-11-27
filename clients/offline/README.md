# @golaravel/offline

The browser and Tauri client for GoLaravel's offline sync engine (`pkg/offline`). Reads and writes go to IndexedDB and never wait for the network. A background loop pushes the outbox and pulls server changes whenever the server can be reached.

- **Local first.** `table.create / update / increment / delete` write to IndexedDB at once. The UI reads local data and keeps working through an internet outage.
- **Append-only outbox.** Every change becomes an immutable mutation with a ULID, a hybrid logical clock timestamp and the entity version it was based on. Mutations are pushed oldest first and acknowledged one by one. A retried push is never applied twice.
- **Rebase on pull.** Each row keeps the last state the server confirmed. Pending local changes are replayed on top of it, so edits stay visible until the server has them, and a change the server rejects is undone.
- **Groups.** `batch()` changes (a sale, its lines and the stock change) are applied or rejected together, and a batch is never split across pushes.
- **Encryption (optional).** `encrypt: true` registers a P-256 key. Every payload is then AES-256-GCM encrypted with a per-device key. The private key and derived key are non-extractable `CryptoKey`s kept in IndexedDB.
- **Resilient loop.** Exponential backoff with full jitter, and `Retry-After` is honored. Sync also runs when the device comes back online or the tab becomes visible. One tab per database runs the loop (Web Locks), and other tabs hand it their writes (BroadcastChannel).

No runtime dependencies.

## Usage

```ts
import { SyncClient } from "@golaravel/offline";

const sync = await SyncClient.open({
  url: "https://pos.example.com/api/sync",
  entities: ["product", "sale", "sale_line"],
  database: `pos-${tenantId}`,            // one database per tenant
  headers: async () => ({ Authorization: `Bearer ${await token()}` }),
  encrypt: true,
  device: { name: "Till 1", platform: "tauri", appVersion: "1.4.0" },
});

const products = sync.table<{ name: string; price: number; stock: number }>("product");
products.subscribe((rows) => render(rows));

await sync.batch((b) => {
  const sale = b.create("sale", { total: 1250, paid_at: Date.now() });
  b.create("sale_line", { sale_id: sale, product_id: teaId, qty: 2 });
  b.increment("product", teaId, "stock", -2);   // deltas from all tills add up
});

sync.on("status", (s) => showBadge(s.state, s.pending));  // idle, syncing, offline, backoff, error, revoked
sync.on("rejected", (dead) => toast(`${dead.length} change(s) refused by the server`));
```

Set the server up with `pkg/offline`, mounting the routes behind your auth and tenant middleware:

```go
eng.Routes(app.Group("/api/sync", auth.BearerGuard(jwt), privacy.TenantMiddleware(privacy.JWTClaimResolver())))
```

## Errors

| Server answer | What the client does |
|---|---|
| network failure, 5xx, 429 | retries with backoff; state `offline` or `backoff` |
| push result `retry` | stops the batch and retries it later, in order |
| push result `rejected` | removes the mutation from the queue, undoes it locally and stores it as a dead letter. `deadLetters()`, `retryDeadLetter(id)`, `discardDeadLetter(id)` |
| `410 resync_required` | loads every entity again by snapshot, then pulls |
| `403 unknown_device`, `reregister` | registers again and keeps the pending outbox |
| `403 device_revoked` | stops; state `revoked`, and the app decides (sign in again, wipe) |

## Tauri

Everything runs in the webview. Ask the platform to keep the data (`navigator.storage.persist()`). If the API is on another origin and you'd rather not configure CORS, pass the Tauri HTTP plugin's `fetch` as `options.fetch`.

## Development

```bash
npm install
npm test             # unit tests (fake IndexedDB)
npm run test:e2e     # against the real Go engine; needs Go
npm run build
```
