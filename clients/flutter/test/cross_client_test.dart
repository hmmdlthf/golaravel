// Cross-client end-to-end: the TypeScript client (@golaravel/offline, run in
// Node through clients/offline/test/cross-client-helper.mjs) and this Dart
// client sync the same tenant through the real Go engine, with and without
// encryption. Runs when GOLARAVEL_E2E=1; needs Go, Node and `npm ci` in
// clients/offline (the test builds its dist).
import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:drift/native.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:golaravel/golaravel.dart';
import 'package:http/http.dart' as http;

import 'support/go_server.dart';

final enabled = Platform.environment['GOLARAVEL_E2E'] == '1';

/// A TypeScript SyncClient in a Node process.
class TsClient {
  TsClient._(this._p, this._lines);

  final Process _p;
  final StreamIterator<String> _lines;
  var _n = 0;

  static Future<TsClient> start(Uri sync, String tenant, {required bool encrypt}) async {
    final p = await Process.start('node', ['test/cross-client-helper.mjs', '$sync', tenant, encrypt ? '1' : '0'],
        workingDirectory: '$repoRoot/clients/offline');
    p.stderr.transform(utf8.decoder).listen(stderr.write);
    final lines = StreamIterator(p.stdout.transform(utf8.decoder).transform(const LineSplitter()));
    if (!await lines.moveNext().timeout(const Duration(seconds: 20)) || !lines.current.contains('"ready"')) {
      throw StateError('TS helper did not start: ${lines.current}');
    }
    return TsClient._(p, lines);
  }

  Future<Object?> call(String cmd, [Map<String, Object?> args = const {}]) async {
    final id = ++_n;
    _p.stdin.writeln(jsonEncode({'id': id, 'cmd': cmd, 'args': args}));
    if (!await _lines.moveNext().timeout(const Duration(seconds: 30))) throw StateError('TS helper exited');
    final resp = jsonDecode(_lines.current) as Map;
    if (resp['id'] != id) throw StateError('out of order: ${_lines.current}');
    if (resp['ok'] != true) throw StateError('TS $cmd failed: ${resp['error']}');
    return resp['result'];
  }

  Future<Map<String, Object?>?> get(String id) async => (await call('get', {'id': id}) as Map?)?.cast();

  Future<void> close() async {
    try {
      await call('close');
    } catch (_) {}
    _p.kill();
  }
}

void main() {
  late GoServer server;

  setUpAll(() async {
    if (!enabled) return;
    final build = await Process.run('npm', ['run', '-s', 'build'], workingDirectory: '$repoRoot/clients/offline');
    if (build.exitCode != 0) fail('npm run build failed (run npm ci in clients/offline): ${build.stderr}');
    server = await GoServer.start();
  });

  tearDownAll(() async {
    if (enabled) await server.stop();
  });

  Map<String, String> headers(String tenant) => {'X-Test-User': 'dart-till', 'X-Test-Tenant': tenant};

  Future<Map<String, Object?>> admin(String id) async {
    final r = await http.get(server.base.resolve('/admin/products/$id'), headers: headers('acme'));
    return (jsonDecode(r.body) as Map).cast();
  }

  for (final encrypt in [false, true]) {
    final mode = encrypt ? 'encrypted' : 'plaintext';
    test('TypeScript and Dart clients share a tenant ($mode)', () async {
      final dart = await SyncClient.open(
        url: server.base.resolve('/sync'),
        entities: ['product'],
        executor: NativeDatabase.memory(),
        headers: () => headers('acme'),
        encrypt: encrypt,
        keyVault: NativeKeyVault(MemorySecretStore()),
        autoStart: false,
        pollInterval: Duration.zero,
      );
      final ts = await TsClient.start(server.base.resolve('/sync'), 'acme', encrypt: encrypt);
      final globex = await TsClient.start(server.base.resolve('/sync'), 'globex', encrypt: encrypt);
      addTearDown(() async {
        await dart.close();
        await ts.close();
        await globex.close();
      });
      final products = dart.table('product');

      // Dart creates; TypeScript sees it.
      final id = await products.create({'name': 'Tea', 'price': 250, 'stock': 10});
      await dart.syncNow();
      await ts.call('sync');
      expect(await ts.get(id), allOf(containsPair('name', 'Tea'), containsPair('stock', 10)));

      // TypeScript creates; Dart sees it. Ids made by either client sort together.
      final tsId = await ts.call('create', {'fields': {'name': 'Coffee', 'price': 300, 'stock': 4}}) as String;
      await ts.call('sync');
      await dart.syncNow();
      expect(await products.get(tsId), containsPair('name', 'Coffee'));

      // Both sell offline: counter deltas from both clients add up.
      await products.increment(id, 'stock', -2);
      await ts.call('increment', {'id': id, 'field': 'stock', 'by': -3});
      await dart.syncNow();
      await ts.call('sync');
      await dart.syncNow();
      expect((await admin(id))['stock'], 5);
      expect((await products.get(id))?['stock'], 5);
      expect((await ts.get(id))?['stock'], 5);

      // Concurrent edits to different fields both survive (field-level LWW).
      await products.update(id, {'name': 'Green tea'});
      await ts.call('update', {'id': id, 'fields': {'price': 275}});
      await dart.syncNow();
      await ts.call('sync');
      await dart.syncNow();
      for (final row in [await products.get(id), await ts.get(id), await admin(id)]) {
        expect(row, allOf(containsPair('name', 'Green tea'), containsPair('price', 275)));
      }

      // A TypeScript batch with an invalid change is rejected as a whole; Dart never sees it.
      await ts.call('batch', {
        'changes': [
          {'op': 'increment', 'id': id, 'field': 'stock', 'by': -1},
          {'op': 'update', 'id': id, 'fields': {'price': -1}},
        ]
      });
      await ts.call('sync');
      expect((await ts.call('rejected') as List).join(), contains('price must not be negative'));
      await dart.syncNow();
      expect((await products.get(id))?['stock'], 5);
      expect((await ts.get(id))?['stock'], 5);

      // Dart deletes; TypeScript sees the deletion.
      await products.delete(tsId);
      await dart.syncNow();
      await ts.call('sync');
      expect(await ts.get(tsId), isNull);

      // The other tenant sees none of it.
      await globex.call('sync');
      expect(await globex.call('all'), isEmpty);
      expect((await ts.call('status') as Map)['pending'], 0);
      expect(dart.status.value.pending, 0);
    }, skip: enabled ? false : 'set GOLARAVEL_E2E=1 to run');
  }
}
