# golaravel (Flutter)

The Flutter client for GoLaravel. It works on Android, iOS, desktop and web.

It covers **offline sync** (`pkg/offline`) and **per-tenant features** (`pkg/features`). Both speak the same protocol as the TypeScript client `@golaravel/offline`.

## Offline sync

`SyncClient` is the Dart port of `@golaravel/offline`. Reads and writes go to a local SQLite database and never wait for the network, and a background loop pushes the outbox and pulls server changes whenever the server can be reached.

```dart
import 'package:golaravel/golaravel.dart';

final sync = await SyncClient.open(
  url: Uri.parse('https://pos.example.com/api/sync'),
  entities: ['product', 'sale', 'sale_line'],
  database: 'pos-$tenantId',            // one database per tenant
  headers: () async => {'Authorization': 'Bearer ${await token()}'},
  encrypt: true,
  device: const DeviceInfo(name: 'Till 1', platform: 'android', appVersion: '1.4.0'),
);

final products = sync.table('product');
StreamBuilder(stream: products.watch(), builder: (context, snap) => ProductList(snap.data ?? const []));

await sync.batch((b) {
  final sale = b.create('sale', {'total': 1250, 'paid_at': DateTime.now().millisecondsSinceEpoch});
  b.create('sale_line', {'sale_id': sale, 'product_id': teaId, 'qty': 2});
  b.increment('product', teaId, 'stock', -2);   // deltas from all tills add up
});

ValueListenableBuilder(valueListenable: sync.status, builder: (context, s, _) => SyncBadge(s.state, s.pending));
sync.rejected.listen((dead) => toast('${dead.length} change(s) refused by the server'));
```

- **Local first.** `create`, `update`, `increment` and `delete` write to SQLite at once. Rows are maps with an `id` key.
- **Append-only outbox.** Every change becomes an immutable mutation with a ULID, a hybrid logical clock timestamp and the entity version it was based on. Mutations are pushed oldest first and acknowledged one by one, so a retried push is never applied twice.
- **Rebase on pull.** Each row keeps the last state the server confirmed. Pending local changes are replayed on top of it, so edits stay visible until the server has them, and a change the server rejects is undone.
- **Groups.** Changes made in one `batch()` are applied or rejected together, and a batch is never split across pushes.
- **Encryption (optional).** `encrypt: true` registers a P-256 key, and every payload is then AES-256-GCM encrypted with a per-device key. The key is never written to the sync database:
  - **Android, iOS, macOS, Windows, Linux:** the platform keystore (Keychain, Android Keystore, DPAPI, libsecret) through `flutter_secure_storage`. On Linux the device needs libsecret and a running keyring. The crypto is pure Dart (pointycastle).
  - **Web:** WebCrypto derives a non-extractable `CryptoKey`, stored in IndexedDB. Page script can use the key but never read it, the same as `@golaravel/offline`.
  - A key that an earlier version kept in the database is moved into the keystore and deleted from the database on first use.
  - Pass `keyVault:` to choose another place, e.g. `NativeKeyVault(mySecretStore)`.
- **Resilient loop.** Exponential backoff with full jitter, and `Retry-After` is honored. Sync also runs when the app returns to the foreground.
- **Storage.** By default `driftDatabase(name: database)` from `drift_flutter`: SQLite on Android, iOS and desktop. On web it runs SQLite in WebAssembly, which needs `sqlite3.wasm` and `drift_worker.js` in your app's `web/` folder (see the drift web setup docs), or a `web:` option pointing at them. Pass `executor:` to use your own drift executor.

Use one `SyncClient` per database in an app, and `close()` it when done.

| Server answer | What the client does |
|---|---|
| network failure, 5xx, 429 | retries with backoff; state `offline` or `backoff` |
| push result `retry` | stops the batch and retries it later, in order |
| push result `rejected` | removes the mutation from the queue, undoes it locally and stores it as a dead letter. `deadLetters()`, `retryDeadLetter(id)`, `discardDeadLetter(id)` |
| `410 resync_required` | loads every entity again by snapshot, then pulls |
| `403 unknown_device`, `reregister` | registers again and keeps the pending outbox |
| `403 device_revoked` | stops; state `revoked`, and the app decides (sign in again, wipe) |

Server side:

```go
eng.Routes(app.Group("/api/sync", auth.BearerGuard(jwt), privacy.TenantMiddleware(privacy.JWTClaimResolver())))
```

## Features

`FeatureClient` reads the tenant's public features from `pkg/features`' endpoint (`mgr.Handler()`) and keeps the last known values, so an offline device still knows which features are on.

```dart
import 'package:golaravel/golaravel.dart';

final features = await FeatureClient.open(
  url: Uri.parse('https://pos.example.com/api/features'),
  headers: () async => {'Authorization': 'Bearer ${await token()}'},
  storageKey: 'features:$tenantId', // one cache per tenant
);

features.enabled('invoices.recurring');                    // true only when the value is exactly true
features.value<Map>('ui.theme', const {'color': 'blue'}); // fallback when unknown or another type

// FeatureClient is a ChangeNotifier:
ListenableBuilder(
  listenable: features,
  builder: (context, _) => features.enabled('invoices.recurring') ? const RecurringTab() : const SizedBox(),
);
```

- **Reads never wait for the network.** Values come from the last server answer, saved with `shared_preferences`. Pass `storage:` for your own, or `MemoryFeatureStorage()` for none. Before the first answer every feature is off.
- **Cheap polling.** It checks with `If-None-Match` every `pollInterval` (default 60 s), and again when the app returns to the foreground. An unchanged answer is a `304` with no body.
- **Failures keep the last values.** `refresh()` throws `GoLaravelNetworkError` or `GoLaravelHttpError`, and `errors` emits, but the cached values stay in use. `changes` emits after each change.
- **Display only.** Hiding something in the app is for display. The server must still check with `features.Require` or `Get`.

Call `dispose()` (or `close()`) when you're done with the client.

Server side:

```go
api.GET("/features", auth.BearerGuard(jwt), privacy.TenantMiddleware(privacy.JWTClaimResolver()), mgr.Handler())
```

## Using it from an app

Until the package is published, depend on it from git:

```yaml
dependencies:
  golaravel:
    git:
      url: https://github.com/hmmdlthf/golaravel.git
      path: clients/flutter
```

## Development

```bash
flutter pub get
flutter analyze
flutter test
GOLARAVEL_E2E=1 flutter test test/e2e_test.dart   # against the real Go server, plaintext and encrypted; needs Go
./tool/web_test_assets.sh   # copies drift's sqlite3.wasm into test/ for the web tests
flutter test --platform chrome test/web_storage_test.dart test/web_key_vault_test.dart test/web_logic_test.dart test/features_test.dart test/native_key_vault_test.dart
```

`GOLARAVEL_E2E=1 flutter test` also runs `test/cross_client_test.dart`, where a TypeScript client (`@golaravel/offline` in Node) and a Dart client sync the same tenant through the Go server. It needs `npm ci` in `clients/offline` first.

```bash
```
