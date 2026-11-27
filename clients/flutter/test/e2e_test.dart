// End-to-end: SyncClient against the real Go engine (pkg/offline), with and
// without encryption, and FeatureClient against pkg/features' handler, both
// served by pkg/offline/internal/e2eserver. Needs Go; runs when
// GOLARAVEL_E2E=1. GOLARAVEL_GO overrides the go binary.
import 'dart:convert';
import 'dart:io';

import 'package:drift/drift.dart' show QueryExecutor;
import 'package:drift/native.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:golaravel/golaravel.dart';
import 'package:http/http.dart' as http;

final enabled = Platform.environment['GOLARAVEL_E2E'] == '1';

void main() {
  late Process server;
  late Uri base;
  late Directory dir;

  setUpAll(() async {
    if (!enabled) return;
    dir = await Directory.systemTemp.createTemp('golaravel-dart-e2e-');
    final bin = '${dir.path}/e2eserver';
    final repo = Directory.current.parent.parent.path; // clients/flutter -> repo
    final build = await Process.run(
        Platform.environment['GOLARAVEL_GO'] ?? 'go', ['build', '-o', bin, './pkg/offline/internal/e2eserver'],
        workingDirectory: repo);
    if (build.exitCode != 0) fail('go build failed: ${build.stderr}');
    server = await Process.start(bin, ['-addr', '127.0.0.1:0']);
    server.stderr.transform(utf8.decoder).listen(stderr.write);
    final addr = await server.stdout
        .transform(utf8.decoder)
        .transform(const LineSplitter())
        .map((l) => RegExp(r'LISTENING (\S+)').firstMatch(l)?.group(1))
        .firstWhere((m) => m != null)
        .timeout(const Duration(seconds: 20));
    base = Uri.parse(addr!);
  });

  tearDownAll(() async {
    if (!enabled) return;
    server.kill();
    await dir.delete(recursive: true);
  });

  Map<String, String> headers(String tenant) => {'X-Test-User': 'cashier', 'X-Test-Tenant': tenant};

  Future<void> setFeature(String key, Object? value, String tenant) async {
    final r = await http.post(base.resolve('/admin/features/$key'),
        headers: {...headers(tenant), 'Content-Type': 'application/json'}, body: jsonEncode({'value': value}));
    expect(r.statusCode, 200, reason: r.body);
  }

  Future<({int status, Map<String, Object?> body})> admin(String method, String id,
      [Map<String, Object?>? body, String tenant = 'acme']) async {
    final uri = base.resolve('/admin/products/$id');
    final h = {...headers(tenant), 'Content-Type': 'application/json'};
    final r = method == 'GET' ? await http.get(uri, headers: h) : await http.post(uri, headers: h, body: jsonEncode(body));
    return (status: r.statusCode, body: (jsonDecode(r.body) as Map).cast<String, Object?>());
  }

  final clients = <SyncClient>[];
  tearDown(() async {
    for (final c in clients) {
      await c.close();
    }
    clients.clear();
  });

  Future<SyncClient> openSync(
      {required bool encrypt, String tenant = 'acme', QueryExecutor? executor, SecretStore? secrets}) async {
    final c = await SyncClient.open(
      url: base.resolve('/sync'),
      entities: ['product'],
      executor: executor ?? NativeDatabase.memory(),
      keyVault: NativeKeyVault(secrets ?? MemorySecretStore()),
      headers: () => headers(tenant),
      encrypt: encrypt,
      device: const DeviceInfo(name: 'dart e2e', platform: 'flutter-test'),
      autoStart: false,
      pollInterval: Duration.zero,
    );
    clients.add(c);
    return c;
  }

  for (final encrypt in [false, true]) {
    final mode = encrypt ? 'encrypted' : 'plaintext';
    group('sync against the Go server ($mode)', () {
      test('offline writes, counters from two tills, admin edits, rejections', () async {
        final till1 = await openSync(encrypt: encrypt);
        final till2 = await openSync(encrypt: encrypt);
        final p1 = till1.table('product'), p2 = till2.table('product');

        final id = await p1.create({'name': 'Tea', 'price': 250, 'stock': 10});
        await till1.syncNow();
        await till2.syncNow();
        expect(await p2.get(id), allOf(containsPair('name', 'Tea'), containsPair('stock', 10)));

        // Both tills sell offline, then reconnect.
        await p1.increment(id, 'stock', -2);
        await p2.increment(id, 'stock', -3);
        await till1.syncNow();
        await till2.syncNow();
        await till1.syncNow();
        expect((await admin('GET', id)).body['stock'], 5);
        expect((await p1.get(id))?['stock'], 5);
        expect((await p2.get(id))?['stock'], 5);

        // An admin edit reaches the tills.
        await admin('POST', id, {'name': 'Green tea'});
        await till2.syncNow();
        expect((await p2.get(id))?['name'], 'Green tea');

        // A sale grouped with an invalid change is rejected as a whole and undone.
        final rejected = <String>[];
        till1.rejected.listen((d) => rejected.addAll(d.map((x) => x.reason)));
        await till1.batch((b) {
          b.increment('product', id, 'stock', -1);
          b.update('product', id, {'price': -1});
        });
        expect((await p1.get(id))?['stock'], 4);
        await till1.syncNow();
        await Future<void>.delayed(Duration.zero);
        expect(rejected.any((r) => r.contains('price must not be negative')), isTrue, reason: '$rejected');
        expect((await p1.get(id))?['stock'], 5);
        expect((await admin('GET', id)).body['stock'], 5);
        expect(till1.status.value.pending, 0);
      });

      test('a new device loads existing data by snapshot; tenants stay apart', () async {
        await admin('POST', 'dart-seed-$encrypt', {'name': 'Seeded', 'price': 1});
        await admin('POST', 'dart-other-$encrypt', {'name': 'Globex only'}, 'globex');
        final c = await openSync(encrypt: encrypt);
        await c.syncNow();
        final names = (await c.table('product').all()).map((r) => r['name']);
        expect(names, contains('Seeded'));
        expect(names, isNot(contains('Globex only')));
      });

      test('deletes propagate', () async {
        final a = await openSync(encrypt: encrypt);
        final b = await openSync(encrypt: encrypt);
        final id = await a.table('product').create({'name': 'Temp'});
        await a.syncNow();
        await b.syncNow();
        expect(await b.table('product').get(id), isNotNull);
        await a.table('product').delete(id);
        await a.syncNow();
        await b.syncNow();
        expect(await b.table('product').get(id), isNull);
        expect((await admin('GET', id)).body['deleted'], isTrue);
      });
    }, skip: enabled ? false : 'set GOLARAVEL_E2E=1 to run');
  }

  group('device key storage', () {
    test('the key lives in the vault, never in the sync database, and survives a restart', () async {
      final dbFile = File('${dir.path}/keys-${DateTime.now().microsecondsSinceEpoch}.db');
      final secrets = MemorySecretStore();
      var c = await openSync(encrypt: true, executor: NativeDatabase(dbFile), secrets: secrets);
      final id = await c.table('product').create({'name': 'Vaulted'});
      await c.syncNow();
      expect(await c.store.getMeta('key'), isNull);
      expect(secrets.values.keys, ['golaravel-offline:golaravel-offline:device-key']);
      await c.close();
      clients.remove(c);

      // Restart: same database file and keystore, same device, still encrypted.
      c = await openSync(encrypt: true, executor: NativeDatabase(dbFile), secrets: secrets);
      final device = (await c.store.getMeta('device') as Map)['id'];
      await c.table('product').update(id, {'name': 'Vaulted 2'});
      await c.syncNow();
      expect((await c.store.getMeta('device') as Map)['id'], device);
      expect((await admin('GET', id)).body['name'], 'Vaulted 2');
    });

    test('a key an older version kept in the database moves to the vault', () async {
      final dbFile = File('${dir.path}/legacy-${DateTime.now().microsecondsSinceEpoch}.db');
      final first = MemorySecretStore();
      var c = await openSync(encrypt: true, executor: NativeDatabase(dbFile), secrets: first);
      await c.syncNow();
      // Put the database back in the previous release's shape: key in the
      // database, nothing in the keystore.
      final key = first.values.values.single;
      await c.store.setMeta({'key': key});
      await c.close();
      clients.remove(c);

      final keystore = MemorySecretStore();
      c = await openSync(encrypt: true, executor: NativeDatabase(dbFile), secrets: keystore);
      final id = await c.table('product').create({'name': 'Migrated'});
      await c.syncNow(); // same device, same key: the server accepts the envelopes
      expect(await c.store.getMeta('key'), isNull);
      expect(keystore.values.values.single, key);
      expect((await admin('GET', id)).body['name'], 'Migrated');
    });
  }, skip: enabled ? false : 'set GOLARAVEL_E2E=1 to run');

  test('public features per tenant, revalidated by ETag, cached offline', () async {
    final statuses = <int>[];
    final counting = _CountingClient(http.Client(), statuses);
    final storage = MemoryFeatureStorage();
    Future<FeatureClient> open(String tenant, http.Client client) => FeatureClient.open(
          url: base.resolve('/features'),
          headers: () => headers(tenant),
          client: client,
          storage: storage,
          storageKey: 'features:$tenant',
          autoStart: false,
          pollInterval: Duration.zero,
        );

    final acme = await open('acme', counting);
    expect(await acme.refresh(), isTrue);
    expect(acme.enabled('invoices.recurring'), isFalse);
    expect(acme.value<Map>('ui.theme', {}), {'color': 'blue'});
    expect(acme.value<String>('tax.mode', 'unsent'), 'unsent'); // not Public

    expect(await acme.refresh(), isFalse);
    expect(statuses.last, 304);

    await setFeature('invoices.recurring', true, 'acme');
    expect(await acme.refresh(), isTrue);
    expect(acme.enabled('invoices.recurring'), isTrue);

    final globex = await open('globex', counting);
    await globex.refresh();
    expect(globex.enabled('invoices.recurring'), isFalse);

    // Offline restart: the cached values answer.
    final offline = await open('acme', _OfflineClient());
    expect(offline.enabled('invoices.recurring'), isTrue);
    await expectLater(offline.refresh(), throwsA(isA<GoLaravelNetworkError>()));
    expect(offline.enabled('invoices.recurring'), isTrue);

    // No tenant: the endpoint fails closed.
    final none = await FeatureClient.open(
        url: base.resolve('/features'), storage: MemoryFeatureStorage(), autoStart: false, pollInterval: Duration.zero);
    await expectLater(none.refresh(), throwsA(isA<GoLaravelHttpError>().having((e) => e.status, 'status', 403)));
    for (final c in [acme, globex, offline, none]) {
      c.close();
    }
  }, skip: enabled ? false : 'set GOLARAVEL_E2E=1 to run');
}

class _CountingClient extends http.BaseClient {
  _CountingClient(this._inner, this.statuses);
  final http.Client _inner;
  final List<int> statuses;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    final r = await _inner.send(request);
    statuses.add(r.statusCode);
    return r;
  }
}

class _OfflineClient extends http.BaseClient {
  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) =>
      Future.error(const SocketException('network is unreachable'));
}
