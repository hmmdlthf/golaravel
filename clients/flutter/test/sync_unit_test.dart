import 'dart:math';

import 'package:drift/native.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:golaravel/golaravel.dart';
import 'package:golaravel/src/sync/crypto.dart';
import 'package:golaravel/src/sync/transport.dart';

Mutation mut(String entityId, Op op, {Map<String, Object?>? set, Map<String, num>? inc}) =>
    Mutation(id: ulid(), entity: 'product', entityId: entityId, op: op, hlc: '', at: 0, set: set, inc: inc);

Future<LocalStore> store([int Function()? now]) => LocalStore.open(NativeDatabase.memory(), now: now);

void main() {
  group('ulid', () {
    test('is a monotonic 26-char Crockford ULID', () {
      var prev = '';
      for (var i = 0; i < 2000; i++) {
        final id = ulid();
        expect(id, matches(RegExp(r'^[0-7][0-9A-HJKMNP-TV-Z]{25}$')));
        expect(id.compareTo(prev) > 0, isTrue);
        prev = id;
      }
    });
    test('starts with the time, so later ids sort later', () {
      final now = DateTime.now().millisecondsSinceEpoch;
      final a = ulid(now + 60000), b = ulid(now + 120000);
      expect(a.substring(0, 10).compareTo(b.substring(0, 10)) < 0, isTrue);
    });
  });

  test('hlc matches the server encoding and sorts as text', () {
    expect(const Hlc(1700000000000, 3, 'a').toString(), '001700000000000:00003:a');
    final h = Hlc.parse('001700000000000:00003:a')!;
    expect([h.wall, h.count, h.node], [1700000000000, 3, 'a']);
    expect(Hlc.parse('junk'), isNull);
    var t = 1000;
    final c = Clock('n', null, () => t);
    final a = c.now(), b = c.now();
    expect(b.compareTo(a) > 0, isTrue);
    c.observe('000000000005000:00007:server');
    expect(c.now(), '000000000005000:00008:n');
    t = 9000;
    expect(c.now(), '000000000009000:00000:n');
  });

  test('backoff is full jitter within the exponential cap', () {
    const i = Duration(seconds: 1), m = Duration(seconds: 30);
    expect(backoffDelay(0, i, m, _R(1)).inMilliseconds, 1000);
    expect(backoffDelay(3, i, m, _R(1)).inMilliseconds, 8000);
    expect(backoffDelay(10, i, m, _R(1)).inMilliseconds, 30000);
    expect(backoffDelay(10, i, m, _R(0)).inMilliseconds, 250);
  });

  test('applyLocal: create, update, counters, delete', () {
    var d = applyLocal(null, mut('p', Op.create, set: {'name': 'Tea', 'stock': 3}));
    d = applyLocal(d, mut('p', Op.update, inc: {'stock': -1, 'sold': 1}));
    expect(d, {'name': 'Tea', 'stock': 2, 'sold': 1});
    expect(applyLocal(null, mut('p', Op.update, set: {'a': 1})), isNull);
    expect(applyLocal(d, mut('p', Op.delete)), isNull);
  });

  group('LocalStore', () {
    test('local writes stay visible over newer server state until acknowledged', () async {
      final s = await store();
      final create = mut('p1', Op.create, set: {'name': 'Tea', 'stock': 10});
      await s.write([create]);
      await s.applyResults([create], [PushResult(id: create.id, status: Status.applied, seq: 1)]);
      await s.applyChanges(
          [RemoteChange(entity: 'product', entityId: 'p1', version: 1, data: {'name': 'Tea', 'stock': 10})], {});

      final sale = mut('p1', Op.update, inc: {'stock': -2});
      await s.write([sale]);
      expect((await s.get('product', 'p1'))?['stock'], 8);

      // Another till's sale arrives first: base becomes 9, our pending -2 replays on top.
      await s.applyChanges([
        RemoteChange(entity: 'product', entityId: 'p1', version: 2, data: {'name': 'Tea', 'stock': 9})
      ], {'cursor': 2});
      expect((await s.get('product', 'p1'))?['stock'], 7);
      expect(await s.getMeta('cursor'), 2);

      // Stale changes are ignored.
      await s.applyChanges(
          [RemoteChange(entity: 'product', entityId: 'p1', version: 1, data: {'name': 'Tea', 'stock': 10})], {});
      expect((await s.get('product', 'p1'))?['stock'], 7);

      expect((await s.pending(10)).map((m) => m.id), [sale.id]);
      await s.applyResults([sale], [PushResult(id: sale.id, status: Status.applied, seq: 3)]);
      expect(await s.pendingCount(), 0);
      await s.applyChanges(
          [RemoteChange(entity: 'product', entityId: 'p1', version: 3, data: {'name': 'Tea', 'stock': 7})], {});
      expect((await s.get('product', 'p1'))?['stock'], 7);
      await s.close();
    });

    test('a rejected mutation is undone and kept as a dead letter', () async {
      final s = await store();
      final create = mut('p9', Op.create, set: {'name': 'Bad'});
      await s.write([create]);
      expect(await s.get('product', 'p9'), isNotNull);
      final r = await s.applyResults([create], [
        PushResult(
            id: create.id,
            status: Status.rejected,
            reason: 'invalid',
            record: ServerRecord(id: 'p9', version: 0, deleted: true))
      ]);
      expect(r.rejected, hasLength(1));
      expect(await s.get('product', 'p9'), isNull);
      expect((await s.deadLetters()).first.reason, 'invalid');
      expect(await s.pendingCount(), 0);
      await s.close();
    });

    test('retry results stay queued; the outbox is append-only until pruned', () async {
      var now = 1000;
      final s = await store(() => now);
      final a = mut('a', Op.create, set: {}), b = mut('b', Op.create, set: {});
      await s.write([a, b]);
      await s.applyResults(
          [a, b], [PushResult(id: a.id, status: Status.applied), PushResult(id: b.id, status: Status.retry)]);
      expect((await s.pending(10)).map((m) => m.id), [b.id]);
      expect(await s.outboxSize(), 2);
      expect((await s.ack(a.id))?.status, Status.applied);
      now = 5000;
      expect(await s.prune(2000), 1);
      expect(await s.outboxSize(), 1);
      await s.close();
    });

    test('dropMissing removes what the server lost but keeps pending creates', () async {
      final s = await store();
      await s.applySnapshot('product', [
        ServerRecord(id: 'gone', version: 1, data: {'name': 'x'}),
        ServerRecord(id: 'kept', version: 1, data: {'name': 'y'}),
      ]);
      await s.write([mut('new', Op.create, set: {'name': 'z'})]);
      await s.dropMissing('product', {'kept'});
      expect((await s.all('product')).map((r) => r['id']).toList()..sort(), ['kept', 'new']);
      await s.close();
    });

    test('a write is atomic: a failing mutation leaves nothing behind', () async {
      final s = await store();
      final m = mut('p', Op.create, set: {'a': 1});
      await s.write([m]);
      await expectLater(s.write([mut('q', Op.create, set: {}), m]), throwsA(anything)); // duplicate outbox id
      expect(await s.get('product', 'q'), isNull);
      expect(await s.pendingCount(), 1);
      await s.close();
    });
  });

  test('crypto: both sides derive the same key; envelopes bind their context', () {
    final device = DeviceKeyPair.generate();
    final server = DeviceKeyPair.generate();
    final k1 = device.deriveKey(server.publicKey, 'dev-1');
    final k2 = server.deriveKey(device.publicKey, 'dev-1');
    expect(k1, k2);
    final ad = aad(Direction.req, Endpoint.push, 'acme', 'dev-1');
    final env = sealEnvelope(k1, 'k1', 'dev-1', ad, '{"x":1}');
    expect(openEnvelope(k2, env, ad), '{"x":1}');
    expect(() => openEnvelope(k2, env, aad(Direction.req, Endpoint.pull, 'acme', 'dev-1')),
        throwsA(isA<EnvelopeError>()));
    final k3 = device.deriveKey(server.publicKey, 'dev-2');
    expect(() => openEnvelope(k3, env, ad), throwsA(isA<EnvelopeError>()));
    expect(toBase64Url(fromBase64Url('AQID_-8')), 'AQID_-8');
    expect(fromBase64Url(device.publicKey), hasLength(65)); // uncompressed P-256 point
  });
}

class _R implements Random {
  _R(this.v);
  final double v;
  @override
  double nextDouble() => v;
  @override
  int nextInt(int max) => 0;
  @override
  bool nextBool() => false;
}
