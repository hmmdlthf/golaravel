import 'dart:math';

import 'package:drift/native.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:golaravel/golaravel.dart';

import 'support/fake_sync_server.dart';

final _open = <SyncClient>[];

Future<SyncClient> client(
  FakeSyncServer server, {
  List<String> entities = const ['product', 'sale'],
  int batchSize = 100,
  Random? random,
  bool autoStart = false,
  Duration backoffInitial = const Duration(seconds: 1),
  Duration backoffMax = const Duration(minutes: 5),
  Duration writeDelay = const Duration(milliseconds: 200),
}) async {
  final c = await SyncClient.open(
    url: Uri.parse('http://sync.test/sync'),
    entities: entities,
    executor: NativeDatabase.memory(),
    client: server.client,
    autoStart: autoStart,
    pollInterval: Duration.zero,
    batchSize: batchSize,
    random: random,
    backoffInitial: backoffInitial,
    backoffMax: backoffMax,
    writeDelay: writeDelay,
  );
  _open.add(c);
  return c;
}

class _FixedRandom implements Random {
  _FixedRandom(this.v);
  final double v;
  @override
  double nextDouble() => v;
  @override
  int nextInt(int max) => (v * max).floor().clamp(0, max - 1);
  @override
  bool nextBool() => v >= 0.5;
}

Future<void> waitFor(bool Function() cond, [Duration timeout = const Duration(seconds: 3)]) async {
  final until = DateTime.now().add(timeout);
  while (!cond()) {
    if (DateTime.now().isAfter(until)) throw StateError('timed out');
    await Future<void>.delayed(const Duration(milliseconds: 5));
  }
}

void main() {
  tearDown(() async {
    for (final c in _open) {
      await c.close();
    }
    _open.clear();
  });

  test('writes work offline and reach the server when it comes back', () async {
    final server = FakeSyncServer()..offline = 100;
    final c = await client(server);
    final products = c.table('product');
    final id = await products.create({'name': 'Tea', 'stock': 10});
    await products.increment(id, 'stock', -2);
    expect((await products.get(id))?['stock'], 8);
    expect(c.status.value.pending, 2);

    await expectLater(c.syncNow(), throwsA(isA<GoLaravelNetworkError>()));
    expect(c.status.value.state, SyncState.offline);

    server.offline = 0;
    await c.syncNow();
    expect(c.status.value.state, SyncState.idle);
    expect(c.status.value.pending, 0);
    expect(server.rows['product/$id']?.data, {'name': 'Tea', 'stock': 8});
    expect((await products.get(id))?['stock'], 8);
  });

  test('first sync loads a snapshot and server edits arrive by pull', () async {
    final server = FakeSyncServer()..edit('product', 'p1', {'name': 'From admin', 'stock': 5});
    final c = await client(server, entities: ['product', 'unsynced']);
    await c.syncNow();
    expect(await c.table('product').get('p1'), containsPair('name', 'From admin'));

    server.edit('product', 'p1', {'name': 'Renamed'});
    final seen = <String>[];
    c.changes.listen((t) => seen.addAll(t.map((x) => x.id)));
    await c.syncNow();
    final p1 = await c.table('product').get('p1');
    expect(p1?['name'], 'Renamed');
    expect(p1?['stock'], 5);
    await Future<void>.delayed(Duration.zero);
    expect(seen, contains('p1'));
  });

  test('a retried batch is not applied twice (lost acknowledgment)', () async {
    final server = FakeSyncServer();
    final c = await client(server);
    final id = await c.table('product').create({'stock': 1});
    await c.table('product').increment(id, 'stock', 4);
    server.dropPushResponse = true;
    await expectLater(c.syncNow(), throwsA(anything));
    expect(c.status.value.pending, 2);
    await c.syncNow();
    expect(server.pushes, hasLength(2));
    expect(server.rows['product/$id']?.data['stock'], 5);
    expect(c.status.value.pending, 0);
  });

  test('groups are never split across batches', () async {
    final server = FakeSyncServer();
    final c = await client(server, batchSize: 2);
    await c.table('product').create({'name': 'a'});
    await c.batch((b) {
      final sale = b.create('sale', {'total': 300});
      b.create('sale', {'sale': sale, 'line': 1});
      b.increment('product', 'p-x', 'stock', -1);
    });
    await c.syncNow();
    expect(server.pushes.map((p) => p.length), [4]);
    expect(server.pushes.first.skip(1).map((m) => m['group']).toSet(), hasLength(1));
  });

  test('a retry result stops the batch and backs off', () async {
    final server = FakeSyncServer();
    final c = await client(server, random: _FixedRandom(1));
    final a = await c.table('product').create({'n': 1});
    await c.table('product').create({'n': 2});
    final first = (await c.store.pending(1)).single;
    server.retryOnce.add(first.id);
    await expectLater(c.syncNow(), throwsA(anything));
    expect(c.status.value.state, SyncState.backoff);
    expect(c.status.value.error, 'retry');
    expect(c.status.value.pending, 2);
    expect(c.status.value.nextRetryAt!.isAfter(DateTime.now()), isTrue);
    await c.syncNow();
    expect(c.status.value.pending, 0);
    expect(server.rows.containsKey('product/$a'), isTrue);
  });

  test('rejected mutations are undone locally and reported', () async {
    final server = FakeSyncServer();
    final c = await client(server);
    final products = c.table('product');
    final id = await products.create({'name': 'Bad'});
    server.reject.add((await c.store.pending(1)).single.id);
    final rejected = <String>[];
    c.rejected.listen((d) => rejected.addAll(d.map((x) => x.reason)));
    await c.syncNow();
    await Future<void>.delayed(Duration.zero);
    expect(rejected, ['invalid: test']);
    expect(await products.get(id), isNull);
    final dl = (await c.deadLetters()).single;
    expect(dl.mutation.entityId, id);

    server.reject.clear();
    await c.retryDeadLetter(dl.id);
    await c.syncNow();
    expect(await products.get(id), containsPair('name', 'Bad'));
    expect(await c.deadLetters(), isEmpty);
  });

  test('410 resync reloads from a snapshot', () async {
    final server = FakeSyncServer();
    final c = await client(server);
    server.edit('product', 'p1', {'name': 'one'});
    await c.syncNow();
    server.edit('product', 'p2', {'name': 'two'});
    server.rows.remove('product/p1'); // hard-deleted, and the history compacted
    server.horizon = server.seq + 1;
    await expectLater(c.syncNow(), throwsA(anything));
    expect(c.status.value.error, 'resync_required');
    server.horizon = 0;
    await c.syncNow();
    expect((await c.table('product').all()).map((r) => r['id']).toList()..sort(), ['p2']);
  });

  test('a revoked device stops; an unknown one registers again', () async {
    final server = FakeSyncServer();
    final c = await client(server);
    await c.syncNow();
    server.devices.clear();
    await expectLater(c.syncNow(), throwsA(anything));
    expect(c.status.value.error, 'unknown_device');
    await c.syncNow();
    expect(server.devices, contains('dev-2'));

    server.revoked.add('dev-2');
    await expectLater(c.syncNow(), throwsA(anything));
    expect(c.status.value.state, SyncState.revoked);
  });

  test('Retry-After is honored', () async {
    final server = FakeSyncServer()..failures.add(Failure(503, 'unavailable', 60));
    final c = await client(server, random: _FixedRandom(0));
    await expectLater(c.syncNow(), throwsA(isA<GoLaravelHttpError>()));
    expect(c.status.value.state, SyncState.backoff);
    expect(c.status.value.nextRetryAt!.difference(DateTime.now()).inSeconds, greaterThan(55));
  });

  test('background loop retries with backoff until the server is back', () async {
    final server = FakeSyncServer()..offline = 2;
    final c = await client(server,
        autoStart: true,
        backoffInitial: const Duration(milliseconds: 20),
        backoffMax: const Duration(milliseconds: 50),
        writeDelay: Duration.zero);
    await c.table('product').create({'name': 'Tea'});
    await waitFor(() => c.status.value.state == SyncState.idle && c.status.value.pending == 0);
    expect(server.rows, hasLength(1));
  });

  test('watch emits rows now and after each change', () async {
    final server = FakeSyncServer();
    final c = await client(server);
    final products = c.table('product');
    final seen = <int>[];
    final sub = products.watch().listen((rows) => seen.add(rows.length));
    await waitFor(() => seen.isNotEmpty);
    await products.create({'name': 'Tea'});
    await products.create({'name': 'Coffee'});
    await waitFor(() => seen.last == 2);
    await sub.cancel();
    expect(seen.first, 0);
  });

  test('writes to entities not in the list are refused', () async {
    final c = await client(FakeSyncServer());
    await expectLater(c.table('nope').create({}), throwsArgumentError);
  });
}
