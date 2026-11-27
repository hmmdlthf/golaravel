// SyncClient on web: SQLite compiled to WebAssembly, stored in IndexedDB, as
// a Flutter web app would run it. Needs test/sqlite3.wasm (run
// tool/web_test_assets.sh) and runs with --platform chrome.
@TestOn('browser')
library;

import 'package:drift/wasm.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:golaravel/golaravel.dart';
import 'package:sqlite3/wasm.dart';

import 'support/fake_sync_server.dart';

void main() {
  late WasmSqlite3 sqlite3;

  setUpAll(() async {
    sqlite3 = await WasmSqlite3.loadFromUrl(Uri.parse('/sqlite3.wasm'));
  });

  Future<(SyncClient, IndexedDbFileSystem)> open(String dbName, FakeSyncServer server) async {
    final fs = await IndexedDbFileSystem.open(dbName: dbName);
    sqlite3.registerVirtualFileSystem(fs, makeDefault: true);
    final c = await SyncClient.open(
      url: Uri.parse('http://sync.test/sync'),
      entities: ['product'],
      executor: WasmDatabase(sqlite3: sqlite3, path: '/sync.db', fileSystem: fs),
      client: server.client,
      autoStart: false,
      pollInterval: Duration.zero,
    );
    return (c, fs);
  }

  Future<void> shut(SyncClient c, IndexedDbFileSystem fs) async {
    await c.close();
    await fs.flush();
    sqlite3.unregisterVirtualFileSystem(fs);
    await fs.close();
  }

  test('offline writes survive a reload and sync afterwards', () async {
    final dbName = 'golaravel-web-${DateTime.now().microsecondsSinceEpoch}';
    final server = FakeSyncServer()..edit('product', 'seed', {'name': 'From server'});

    // First page load: sync once, then go offline and write.
    var (c, fs) = await open(dbName, server);
    await c.syncNow();
    expect(await c.table('product').get('seed'), containsPair('name', 'From server'));
    server.offline = 100;
    final id = await c.table('product').create({'name': 'Tea', 'stock': 10});
    await c.table('product').increment(id, 'stock', -2);
    await expectLater(c.syncNow(), throwsA(isA<GoLaravelNetworkError>()));
    expect(c.status.value.pending, 2);
    await shut(c, fs);

    // Reload: everything is read back from IndexedDB.
    (c, fs) = await open(dbName, server);
    expect(c.status.value.pending, 2);
    expect((await c.table('product').get(id))?['stock'], 8);
    expect(await c.table('product').get('seed'), isNotNull);

    // Back online: the queued writes reach the server, on the same device.
    server.offline = 0;
    await c.syncNow();
    expect(c.status.value.pending, 0);
    expect(server.rows['product/$id']?.data, {'name': 'Tea', 'stock': 8});
    expect(server.registered, 1, reason: 'the device registration persisted across the reload');
    await shut(c, fs);
  });
}
