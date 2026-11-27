# golaravel (Flutter)

The Flutter client for GoLaravel. It works on Android, iOS, desktop and web.

So far it covers **per-tenant features** (`pkg/features`). A Dart port of the offline sync client (`pkg/offline`, today in TypeScript as `@golaravel/offline`) is planned.

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
GOLARAVEL_E2E=1 flutter test test/e2e_test.dart   # against the real Go handler; needs Go
```
