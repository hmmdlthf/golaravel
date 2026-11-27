// The local database, mirroring @golaravel/offline's IndexedDB layout in
// SQLite (drift, raw SQL, no code generation):
//
//   entities  (entity, id) -> base, data, pending   what the UI reads
//   outbox    id -> mutation                         append-only: entries never change
//   queue     id                                     outbox ids not yet acknowledged
//   acks      id -> the server's answer
//   dead      id -> dead letter                      rejected mutations, for the UI
//   meta      key -> JSON value                      device, cursor, clock, key
//
// Each row keeps the last state the server confirmed (base) and the state
// the UI sees (data): base with every unacknowledged mutation replayed on
// top. When the server's state arrives, base is replaced and pending
// mutations replayed again, so local edits stay visible until the server
// has them, and a rejected edit disappears once it leaves the queue.

import 'dart:convert';

import 'package:drift/drift.dart';

import 'types.dart';

class _SyncDatabase extends GeneratedDatabase {
  _SyncDatabase(super.executor);

  @override
  Iterable<TableInfo<Table, dynamic>> get allTables => const [];

  @override
  int get schemaVersion => 1;

  @override
  MigrationStrategy get migration => MigrationStrategy(onCreate: (m) async {
        for (final s in const [
          'CREATE TABLE entities (entity TEXT NOT NULL, id TEXT NOT NULL, base TEXT, data TEXT, '
              'pending INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (entity, id))',
          'CREATE TABLE outbox (id TEXT PRIMARY KEY, entity TEXT NOT NULL, entity_id TEXT NOT NULL, body TEXT NOT NULL)',
          'CREATE INDEX outbox_entity ON outbox (entity, entity_id)',
          'CREATE TABLE queue (id TEXT PRIMARY KEY)',
          'CREATE TABLE acks (id TEXT PRIMARY KEY, status TEXT NOT NULL, original TEXT, seq INTEGER, reason TEXT, '
              'at INTEGER NOT NULL)',
          'CREATE INDEX acks_at ON acks (at)',
          'CREATE TABLE dead (id TEXT PRIMARY KEY, body TEXT NOT NULL)',
          'CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)',
        ]) {
          await customStatement(s);
        }
      });
}

/// The last state the server confirmed for an entity.
class Base {
  const Base(this.data, this.version, this.hlc);

  /// Null when the server has the entity deleted.
  final Map<String, Object?>? data;
  final int version;
  final String hlc;

  Map<String, Object?> toJson() => {'data': data, 'version': version, 'hlc': hlc};

  static Base? fromJson(Object? j) {
    if (j is! Map) return null;
    return Base((j['data'] as Map?)?.cast<String, Object?>(), (j['version'] as num?)?.toInt() ?? 0,
        j['hlc'] as String? ?? '');
  }
}

/// One local entity: its server state, what the UI sees, and how many
/// local mutations are still pending.
class EntityRow {
  EntityRow(this.entity, this.id, this.base, this.data, this.pending);

  final String entity;
  final String id;
  Base? base;
  Map<String, Object?>? data;
  int pending;
}

/// The server's stored answer for one mutation.
class AckRow {
  AckRow(this.id, this.status, this.original, this.seq, this.reason, this.at);

  final String id;
  final Status status;
  final Status? original;
  final int? seq;
  final String? reason;
  final int at;
}

/// Applies a mutation to an entity's data, like the server does.
Map<String, Object?>? applyLocal(Map<String, Object?>? data, Mutation m) {
  if (m.op == Op.delete) return null;
  Map<String, Object?> out;
  if (m.op == Op.create) {
    out = {...?data, ...?m.set};
  } else if (data == null) {
    return null; // update of something we do not have
  } else {
    out = {...data, ...?m.set};
  }
  m.inc?.forEach((field, by) {
    final cur = out[field];
    out[field] = (cur is num ? cur : 0) + by;
  });
  return out;
}

class LocalStore {
  LocalStore._(this._db, this._now);

  final _SyncDatabase _db;
  final int Function() _now;

  static Future<LocalStore> open(QueryExecutor executor, {int Function()? now}) async {
    final db = _SyncDatabase(executor);
    await db.customSelect('SELECT 1').get(); // runs the migration
    return LocalStore._(db, now ?? () => DateTime.now().millisecondsSinceEpoch);
  }

  Future<void> close() => _db.close();

  // ---- local writes ---------------------------------------------------

  /// Appends mutations to the outbox and applies them locally, atomically.
  Future<List<Touched>> write(List<Mutation> muts) => _db.transaction(() async {
        final touched = <Touched>{};
        for (final m in muts) {
          await _exec('INSERT INTO outbox (id, entity, entity_id, body) VALUES (?, ?, ?, ?)',
              [m.id, m.entity, m.entityId, jsonEncode(m.toJson())]);
          await _exec('INSERT INTO queue (id) VALUES (?)', [m.id]);
          final row = await _row(m.entity, m.entityId) ?? EntityRow(m.entity, m.entityId, null, null, 0);
          row.data = applyLocal(row.data, m);
          row.pending++;
          await _put(row);
          touched.add(Touched(m.entity, m.entityId));
        }
        return touched.toList();
      });

  // ---- reads ----------------------------------------------------------

  Future<Map<String, Object?>?> get(String entity, String id) async {
    final row = await _row(entity, id);
    final data = row?.data;
    return data == null ? null : {...data, 'id': id};
  }

  Future<EntityRow?> row(String entity, String id) => _row(entity, id);

  Future<List<Map<String, Object?>>> all(String entity) async {
    final rows = await _select('SELECT id, data FROM entities WHERE entity = ? AND data IS NOT NULL ORDER BY id', [entity]);
    return [
      for (final r in rows) {...(jsonDecode(r.read<String>('data')) as Map).cast<String, Object?>(), 'id': r.read<String>('id')}
    ];
  }

  /// Oldest unacknowledged mutations, in the order they were made.
  Future<List<Mutation>> pending(int limit) async {
    final rows = await _select(
        'SELECT o.body FROM queue q JOIN outbox o ON o.id = q.id ORDER BY q.id LIMIT ?', [limit]);
    return [for (final r in rows) Mutation.fromJson((jsonDecode(r.read<String>('body')) as Map).cast())];
  }

  Future<int> pendingCount() async =>
      (await _select('SELECT COUNT(*) AS n FROM queue', const [])).single.read<int>('n');

  // ---- server answers -------------------------------------------------

  /// Records push results. Acknowledged mutations leave the queue; rejected
  /// ones go to dead letters; records the server sent replace the base.
  Future<({List<Touched> touched, List<DeadLetter> rejected})> applyResults(
      List<Mutation> muts, List<PushResult> results) {
    final at = _now();
    return _db.transaction(() async {
      final rejected = <DeadLetter>[];
      final records = <Touched, ServerRecord>{};
      final touched = <Touched>{};
      for (var i = 0; i < results.length; i++) {
        final r = results[i];
        final m = i < muts.length ? muts[i] : null;
        if (m == null || r.id != m.id || r.status == Status.retry) continue;
        final status = r.status == Status.duplicate ? (r.original ?? Status.applied) : r.status;
        await _exec('DELETE FROM queue WHERE id = ?', [m.id]);
        await _exec('INSERT OR REPLACE INTO acks (id, status, original, seq, reason, at) VALUES (?, ?, ?, ?, ?, ?)',
            [m.id, r.status.name, r.original?.name, r.seq, r.reason, at]);
        if (status == Status.rejected) {
          final dl = DeadLetter(id: m.id, mutation: m, reason: r.reason ?? 'rejected', at: at);
          await _exec('INSERT OR REPLACE INTO dead (id, body) VALUES (?, ?)', [m.id, jsonEncode(dl.toJson())]);
          rejected.add(dl);
        }
        final t = Touched(m.entity, m.entityId);
        touched.add(t);
        if (r.record != null) records[t] = r.record!;
      }
      for (final t in touched) {
        final rec = records[t];
        await _rebase(t, rec == null ? null : _toBase(rec));
      }
      return (touched: touched.toList(), rejected: rejected);
    });
  }

  /// Applies pulled changes and saves [meta] (the cursor) in the same transaction.
  Future<List<Touched>> applyChanges(List<RemoteChange> changes, Map<String, Object?> meta) =>
      _db.transaction(() async {
        final touched = <Touched>[];
        for (final c in changes) {
          final t = Touched(c.entity, c.entityId);
          final base = Base(c.deleted ? null : (c.data ?? {}), c.version ?? 0, c.hlc ?? '');
          if (await _rebase(t, base)) touched.add(t);
        }
        await _setMeta(meta);
        return touched;
      });

  /// Stores one snapshot page as server state.
  Future<List<Touched>> applySnapshot(String entity, List<ServerRecord> records) => applyChanges([
        for (final r in records)
          RemoteChange(entity: entity, entityId: r.id, version: r.version, hlc: r.hlc, data: r.data, deleted: r.deleted)
      ], const {});

  /// After a full snapshot: entities the server no longer has are dropped,
  /// unless local mutations are still pending for them.
  Future<List<Touched>> dropMissing(String entity, Set<String> seen) => _db.transaction(() async {
        final rows = await _select('SELECT id, base FROM entities WHERE entity = ? AND base IS NOT NULL', [entity]);
        final touched = <Touched>[];
        for (final r in rows) {
          final id = r.read<String>('id');
          if (seen.contains(id)) continue;
          final base = Base.fromJson(jsonDecode(r.read<String>('base')))!;
          final t = Touched(entity, id);
          await _rebase(t, Base(null, base.version, base.hlc), force: true);
          touched.add(t);
        }
        return touched;
      });

  /// Replaces the row's base (when given and not older than the current
  /// one) and replays pending mutations. Returns whether anything changed.
  Future<bool> _rebase(Touched t, Base? base, {bool force = false}) async {
    final row = await _row(t.entity, t.id) ?? EntityRow(t.entity, t.id, null, null, 0);
    if (base != null) {
      if (!force && row.base != null && base.version < row.base!.version) return false; // stale
      row.base = base;
    }
    final pending = await _pendingFor(t);
    Map<String, Object?>? data = row.base?.data == null ? null : {...row.base!.data!};
    for (final m in pending) {
      data = applyLocal(data, m);
    }
    row.data = data;
    row.pending = pending.length;
    if (row.data == null && row.pending == 0 && row.base == null) {
      // Nothing known and nothing pending (a rejected local create).
      await _exec('DELETE FROM entities WHERE entity = ? AND id = ?', [t.entity, t.id]);
      return true;
    }
    // A deleted base stays as a tombstone so older changes are ignored.
    await _put(row);
    return true;
  }

  Future<List<Mutation>> _pendingFor(Touched t) async {
    final rows = await _select(
        'SELECT o.body FROM outbox o JOIN queue q ON q.id = o.id WHERE o.entity = ? AND o.entity_id = ? ORDER BY o.id',
        [t.entity, t.id]);
    return [for (final r in rows) Mutation.fromJson((jsonDecode(r.read<String>('body')) as Map).cast())];
  }

  // ---- dead letters ---------------------------------------------------

  Future<List<DeadLetter>> deadLetters() async {
    final rows = await _select('SELECT body FROM dead ORDER BY id', const []);
    return [for (final r in rows) DeadLetter.fromJson((jsonDecode(r.read<String>('body')) as Map).cast())];
  }

  Future<DeadLetter?> takeDeadLetter(String id) => _db.transaction(() async {
        final rows = await _select('SELECT body FROM dead WHERE id = ?', [id]);
        if (rows.isEmpty) return null;
        await _exec('DELETE FROM dead WHERE id = ?', [id]);
        return DeadLetter.fromJson((jsonDecode(rows.single.read<String>('body')) as Map).cast());
      });

  // ---- meta -----------------------------------------------------------

  Future<Object?> getMeta(String key) async {
    final rows = await _select('SELECT value FROM meta WHERE key = ?', [key]);
    return rows.isEmpty ? null : jsonDecode(rows.single.read<String>('value'));
  }

  /// Sets meta values; a null value deletes the key.
  Future<void> setMeta(Map<String, Object?> values) => _db.transaction(() => _setMeta(values));

  Future<void> _setMeta(Map<String, Object?> values) async {
    for (final e in values.entries) {
      if (e.value == null) {
        await _exec('DELETE FROM meta WHERE key = ?', [e.key]);
      } else {
        await _exec('INSERT OR REPLACE INTO meta (key, value) VALUES (?, ?)', [e.key, jsonEncode(e.value)]);
      }
    }
  }

  // ---- housekeeping ---------------------------------------------------

  /// Deletes outbox entries (and their acks) acknowledged before [before].
  Future<int> prune(int before) => _db.transaction(() async {
        final rows = await _select('SELECT id FROM acks WHERE at < ?', [before]);
        for (final r in rows) {
          final id = r.read<String>('id');
          await _exec('DELETE FROM acks WHERE id = ?', [id]);
          await _exec('DELETE FROM outbox WHERE id = ?', [id]);
        }
        return rows.length;
      });

  Future<AckRow?> ack(String id) async {
    final rows = await _select('SELECT * FROM acks WHERE id = ?', [id]);
    if (rows.isEmpty) return null;
    final r = rows.single;
    return AckRow(id, Status.parse(r.read<String>('status'))!, Status.parse(r.readNullable<String>('original')),
        r.readNullable<int>('seq'), r.readNullable<String>('reason'), r.read<int>('at'));
  }

  Future<int> outboxSize() async =>
      (await _select('SELECT COUNT(*) AS n FROM outbox', const [])).single.read<int>('n');

  // ---- helpers --------------------------------------------------------

  Future<EntityRow?> _row(String entity, String id) async {
    final rows = await _select('SELECT base, data, pending FROM entities WHERE entity = ? AND id = ?', [entity, id]);
    if (rows.isEmpty) return null;
    final r = rows.single;
    final base = r.readNullable<String>('base');
    final data = r.readNullable<String>('data');
    return EntityRow(entity, id, base == null ? null : Base.fromJson(jsonDecode(base)),
        data == null ? null : (jsonDecode(data) as Map).cast<String, Object?>(), r.read<int>('pending'));
  }

  Future<void> _put(EntityRow row) => _exec(
      'INSERT OR REPLACE INTO entities (entity, id, base, data, pending) VALUES (?, ?, ?, ?, ?)', [
    row.entity,
    row.id,
    row.base == null ? null : jsonEncode(row.base!.toJson()),
    row.data == null ? null : jsonEncode(row.data),
    row.pending,
  ]);

  Future<void> _exec(String sql, List<Object?> args) => _db.customStatement(sql, args);

  Future<List<QueryRow>> _select(String sql, List<Object?> args) =>
      _db.customSelect(sql, variables: [for (final a in args) _variable(a)]).get();

  static Variable<Object> _variable(Object? v) => switch (v) {
        null => const Variable(null),
        final String s => Variable.withString(s),
        final int i => Variable.withInt(i),
        final bool b => Variable.withBool(b),
        final double d => Variable.withReal(d),
        _ => Variable.withString(v.toString()),
      };
}

Base _toBase(ServerRecord r) => Base(r.deleted ? null : (r.data ?? {}), r.version, r.hlc ?? '');
