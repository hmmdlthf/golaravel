// Offline-first sync client for pkg/offline: the Dart port of
// @golaravel/offline's SyncClient, with the same wire protocol.

import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'package:drift/drift.dart' show QueryExecutor;
import 'package:drift_flutter/drift_flutter.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/widgets.dart';
import 'package:http/http.dart' as http;

import '../errors.dart';
import '../features.dart' show HeadersBuilder;
import 'crypto.dart';
import 'key_vault.dart';
import 'hlc.dart';
import 'store.dart';
import 'transport.dart';
import 'types.dart';
import 'ulid.dart';

// 2^53 - 1: the largest int that is exact on every platform (web included).
const _never = 0x1FFFFFFFFFFFFF;

/// What the sync loop is doing.
enum SyncState { idle, syncing, offline, backoff, error, revoked, stopped }

@immutable
class SyncStatus {
  const SyncStatus({
    this.state = SyncState.stopped,
    this.pending = 0,
    this.lastSyncAt,
    this.nextRetryAt,
    this.error,
  });

  final SyncState state;

  /// Mutations waiting for the server.
  final int pending;
  final DateTime? lastSyncAt;
  final DateTime? nextRetryAt;

  /// Error code or message of the last failure.
  final String? error;

  SyncStatus copyWith({
    SyncState? state,
    int? pending,
    DateTime? lastSyncAt,
    DateTime? Function()? nextRetryAt,
    String? Function()? error,
  }) =>
      SyncStatus(
        state: state ?? this.state,
        pending: pending ?? this.pending,
        lastSyncAt: lastSyncAt ?? this.lastSyncAt,
        nextRetryAt: nextRetryAt != null ? nextRetryAt() : this.nextRetryAt,
        error: error != null ? error() : this.error,
      );

  @override
  String toString() => 'SyncStatus(${state.name}, pending: $pending${error == null ? '' : ', error: $error'})';
}

/// Shown in the server's device list.
class DeviceInfo {
  const DeviceInfo({this.name = '', this.platform = '', this.appVersion = ''});

  final String name;
  final String platform;
  final String appVersion;
}

/// One local change, before ids and clocks are assigned.
class LocalChange {
  const LocalChange(this.entity, this.id, this.op, {this.set, this.inc});

  final String entity;
  final String id;
  final Op op;
  final Map<String, Object?>? set;
  final Map<String, num>? inc;
}

/// The batch stopped at a mutation the server could not apply yet.
class _RetryLater implements Exception {}

class _Session {
  _Session(this.device, this.cipher);

  final Device device;
  final SessionCipher? cipher;
}

/// Offline-first sync client. Reads and writes go to a local SQLite
/// database and never wait for the network; a background loop pushes the
/// outbox and pulls server changes whenever the server can be reached.
///
/// ```dart
/// final sync = await SyncClient.open(
///   url: Uri.parse('https://pos.example.com/api/sync'),
///   entities: ['product', 'sale', 'sale_line'],
///   database: 'pos-$tenantId', // one database per tenant
///   headers: () async => {'Authorization': 'Bearer ${await token()}'},
///   encrypt: true,
/// );
/// final products = sync.table('product');
/// StreamBuilder(stream: products.watch(), builder: ...);
/// ```
///
/// Use one client per database in an app.
class SyncClient with WidgetsBindingObserver {
  SyncClient._(this.store, this._clock, this._opts)
      : _transport = Transport(url: _opts.url, client: _opts.client, timeout: _opts.timeout, headers: _opts.headers);

  /// Opens the local database and (with [autoStart]) starts syncing.
  ///
  /// [executor] defaults to drift_flutter's `driftDatabase(name: database)`:
  /// SQLite on Android, iOS and desktop; on web, SQLite in WebAssembly, which
  /// needs `sqlite3.wasm` and `drift_worker.js` in your `web/` folder (see
  /// the drift docs) or a [web] option pointing at them.
  ///
  /// With [encrypt], the device key is kept by [keyVault], never in the
  /// database: the platform keystore on Android, iOS and desktop, and a
  /// non-extractable WebCrypto key in IndexedDB on web.
  static Future<SyncClient> open({
    required Uri url,
    required List<String> entities,
    String database = 'golaravel-offline',
    QueryExecutor? executor,
    DriftWebOptions? web,
    HeadersBuilder? headers,
    http.Client? client,
    bool encrypt = false,
    DeviceInfo device = const DeviceInfo(),
    int batchSize = 100,
    int pullLimit = 500,
    Duration pollInterval = const Duration(seconds: 30),
    Duration writeDelay = const Duration(milliseconds: 200),
    Duration backoffInitial = const Duration(seconds: 1),
    Duration backoffMax = const Duration(minutes: 5),
    Duration keepAcknowledged = const Duration(days: 7),
    Duration timeout = const Duration(seconds: 30),
    bool autoStart = true,
    DateTime Function()? now,
    Random? random,
    DeviceKeyVault? keyVault,
  }) async {
    final nowMs = now == null ? () => DateTime.now().millisecondsSinceEpoch : () => now().millisecondsSinceEpoch;
    final store = await LocalStore.open(
      executor ??
          driftDatabase(
            name: database,
            web: web ?? DriftWebOptions(sqlite3Wasm: Uri.parse('sqlite3.wasm'), driftWorker: Uri.parse('drift_worker.js')),
          ),
      now: nowMs,
    );
    var node = await store.getMeta('node') as String?;
    if (node == null) {
      node = ulid();
      await store.setMeta({'node': node});
    }
    final clock = Clock(node, await store.getMeta('hlc') as String?, nowMs);
    final c = SyncClient._(
      store,
      clock,
      _Options(
        url: url,
        entities: List.unmodifiable(entities),
        headers: headers,
        client: client ?? http.Client(),
        ownsClient: client == null,
        encrypt: encrypt,
        device: device,
        batchSize: batchSize,
        pullLimit: pullLimit,
        pollInterval: pollInterval,
        writeDelay: writeDelay,
        backoffInitial: backoffInitial,
        backoffMax: backoffMax,
        keepAcknowledged: keepAcknowledged,
        timeout: timeout,
        now: nowMs,
        random: random ?? Random(),
        keyVault: keyVault ?? platformKeyVault(),
        keyName: 'golaravel-offline:$database:device-key',
      ),
    );
    c._status.value = c._status.value.copyWith(pending: await store.pendingCount());
    if (autoStart) c.start();
    return c;
  }

  final LocalStore store;
  final Clock _clock;
  final _Options _opts;
  final Transport _transport;

  final _status = ValueNotifier(const SyncStatus());
  final _changes = StreamController<List<Touched>>.broadcast();
  final _rejected = StreamController<List<DeadLetter>>.broadcast();
  Future<void>? _running;
  Timer? _timer;
  int _timerAt = _never;
  int _attempt = 0;
  bool _started = false;
  bool _observing = false;
  bool _closed = false;

  // ---- events and status ----------------------------------------------

  /// The current status; listen to rebuild a sync badge.
  ValueListenable<SyncStatus> get status => _status;

  /// Entities whose local state changed (local write, push answer or pull).
  Stream<List<Touched>> get changes => _changes.stream;

  /// Mutations the server refused for good; their local effect is undone.
  Stream<List<DeadLetter>> get rejected => _rejected.stream;

  void _setStatus(SyncStatus Function(SyncStatus s) patch) {
    if (_closed) return;
    _status.value = patch(_status.value);
  }

  void _changed(List<Touched> touched) {
    if (touched.isNotEmpty && !_changes.isClosed) _changes.add(touched);
  }

  // ---- local reads and writes ----------------------------------------

  /// One entity type's local view.
  SyncTable table(String entity) => SyncTable._(this, entity);

  /// Writes changes that must reach the server together (a sale, its lines,
  /// the stock changes): they are pushed as one group and applied or
  /// rejected as a whole.
  Future<void> batch(FutureOr<void> Function(SyncBatch b) fn) async {
    final b = SyncBatch._();
    await fn(b);
    if (b.changes.isNotEmpty) await write(b.changes, group: ulid());
  }

  /// Records changes locally and queues them for the server.
  Future<void> write(List<LocalChange> changes, {String? group}) async {
    final muts = <Mutation>[];
    for (final c in changes) {
      if (!_opts.entities.contains(c.entity)) {
        throw ArgumentError('golaravel: "${c.entity}" is not in SyncClient.open(entities: ...)');
      }
      final row = await store.row(c.entity, c.id);
      muts.add(Mutation(
        id: ulid(),
        entity: c.entity,
        entityId: c.id,
        op: c.op,
        hlc: _clock.now(),
        at: _opts.now(),
        set: c.set == null || c.set!.isEmpty ? null : c.set,
        inc: c.inc == null || c.inc!.isEmpty ? null : c.inc,
        group: group,
        baseVersion: row?.base?.version,
      ));
    }
    final touched = await store.write(muts);
    await store.setMeta({'hlc': _clock.current});
    final pending = await store.pendingCount();
    _setStatus((s) => s.copyWith(pending: pending));
    _changed(touched);
    _kick(_opts.writeDelay);
  }

  /// Mutations the server refused.
  Future<List<DeadLetter>> deadLetters() => store.deadLetters();

  /// Queues a refused mutation again (as a new mutation), e.g. after fixing data.
  Future<void> retryDeadLetter(String id) async {
    final dl = await store.takeDeadLetter(id);
    if (dl == null) return;
    final m = dl.mutation;
    await write([LocalChange(m.entity, m.entityId, m.op, set: m.set, inc: m.inc)]);
  }

  /// Forgets a refused mutation.
  Future<void> discardDeadLetter(String id) => store.takeDeadLetter(id);

  // ---- loop control ----------------------------------------------------

  /// Starts background sync ([open] does this unless autoStart is false).
  /// Sync also runs when the app returns to the foreground.
  void start() {
    if (_started || _closed) return;
    _started = true;
    _setStatus((s) => s.copyWith(state: SyncState.idle));
    if (!_observing) {
      try {
        WidgetsBinding.instance.addObserver(this);
        _observing = true;
      } catch (_) {
        // No Flutter binding (pure Dart tests).
      }
    }
    _kick(Duration.zero);
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      _attempt = 0;
      _kick(Duration.zero);
    }
  }

  /// Stops background sync; local reads and writes keep working.
  void stop() {
    _started = false;
    _clearTimer();
    if (_observing) {
      WidgetsBinding.instance.removeObserver(this);
      _observing = false;
    }
    _setStatus((s) => s.copyWith(state: SyncState.stopped));
  }

  /// Stops syncing and closes the database.
  Future<void> close() async {
    if (_closed) return;
    stop();
    await _running?.catchError((_) {});
    _closed = true;
    await store.close();
    if (_opts.ownsClient) _opts.client.close();
    await _changes.close();
    await _rejected.close();
    _status.dispose();
  }

  /// Syncs now and completes when done; throws the failure. Without
  /// [start] (autoStart: false) it syncs once, with no background retries.
  Future<void> syncNow() => _cycle();

  void _kick(Duration delay) {
    if (!_started || _closed) return;
    final at = _opts.now() + delay.inMilliseconds;
    if (_timer != null && at >= _timerAt) return;
    _clearTimer();
    _timerAt = at;
    _timer = Timer(delay, () {
      _timer = null;
      _timerAt = _never;
      unawaited(_cycle().catchError((_) {}));
    });
  }

  void _clearTimer() {
    _timer?.cancel();
    _timer = null;
    _timerAt = _never;
  }

  Future<void> _cycle() {
    if (_closed) return Future.value();
    return _running ??= () async {
      _setStatus((s) => s.copyWith(state: SyncState.syncing));
      try {
        final session = await _session();
        if (await store.getMeta('snapshot') != true) await _snapshotAll(session);
        await _pushAll(session);
        await _pullAll(session);
        await store.prune(_opts.now() - _opts.keepAcknowledged.inMilliseconds);
        await store.setMeta({'hlc': _clock.current});
        _attempt = 0;
        final pending = await store.pendingCount();
        _setStatus((s) => s.copyWith(
              state: SyncState.idle,
              lastSyncAt: DateTime.fromMillisecondsSinceEpoch(_opts.now()),
              pending: pending,
              error: () => null,
              nextRetryAt: () => null,
            ));
        // Something was written while we were busy: go again.
        if (pending > 0) {
          _kick(_opts.writeDelay);
        } else if (_opts.pollInterval > Duration.zero) {
          _kick(_opts.pollInterval);
        }
      } catch (err) {
        await _failed(err);
        rethrow;
      }
    }()
        .whenComplete(() => _running = null);
  }

  Future<void> _failed(Object err) async {
    int pending;
    try {
      pending = await store.pendingCount();
    } catch (_) {
      pending = _status.value.pending;
    }
    if (err is GoLaravelHttpError) {
      switch (err.code) {
        case 'device_revoked':
          // Signed out from the server; the app decides what happens next.
          _clearTimer();
          _setStatus((s) => s.copyWith(
              state: SyncState.revoked, error: () => err.code, pending: pending, nextRetryAt: () => null));
          return;
        case 'resync_required':
          await store.setMeta({'snapshot': null, 'cursor': null});
          return _retry(SyncState.backoff, err.code, pending, retryAfter: 0);
        case 'unknown_device':
        case 'reregister':
          await store.setMeta({'device': null, 'key': null});
          if (_opts.encrypt) {
            try {
              await _opts.keyVault.delete(_opts.keyName); // registering again makes a new key anyway
            } catch (_) {}
          }
          return _retry(SyncState.backoff, err.code, pending, retryAfter: 0);
      }
      return _retry(err.transient ? SyncState.backoff : SyncState.error, err.code, pending,
          retryAfter: err.retryAfter);
    }
    if (err is GoLaravelNetworkError) return _retry(SyncState.offline, err.cause.toString(), pending);
    if (err is _RetryLater) return _retry(SyncState.backoff, 'retry', pending);
    return _retry(SyncState.error, err.toString(), pending);
  }

  void _retry(SyncState state, String error, int pending, {int? retryAfter}) {
    var delay = backoffDelay(_attempt++, _opts.backoffInitial, _opts.backoffMax, _opts.random);
    if (retryAfter != null) {
      delay = retryAfter == 0
          ? Duration(milliseconds: min(delay.inMilliseconds, 1000))
          : Duration(milliseconds: max(delay.inMilliseconds, retryAfter * 1000));
    }
    _setStatus((s) => s.copyWith(
          state: state,
          error: () => error,
          pending: pending,
          nextRetryAt: () => DateTime.fromMillisecondsSinceEpoch(_opts.now() + delay.inMilliseconds),
        ));
    _kick(delay);
  }

  // ---- protocol ---------------------------------------------------------

  Future<_Session> _session() async {
    final deviceJson = await store.getMeta('device');
    if (deviceJson is Map) {
      final device = Device.fromJson(deviceJson.cast());
      if (!_opts.encrypt) return _Session(device, null);
      final cipher = await _storedCipher();
      if (cipher != null) return _Session(device, cipher);
    }
    final info = <String, String>{
      'name': _opts.device.name,
      'platform': _opts.device.platform,
      'app_version': _opts.device.appVersion,
    };
    PendingDeviceKey? pending;
    if (_opts.encrypt) {
      pending = await _opts.keyVault.generate();
      info['public_key'] = pending.publicKey;
    }
    final json = await _transport.call('POST', '/devices', body: info);
    if (json is! Map) throw const FormatException('golaravel: malformed device registration response');
    final registered = Device.fromJson(json.cast());
    if (!_opts.encrypt) {
      await store.setMeta({'device': registered.json, 'cursor': null, 'snapshot': null});
      return _Session(registered, null);
    }
    final serverKey = registered.serverPublicKey;
    if (serverKey == null) throw StateError('golaravel: the server did not enable encryption for this device');
    final cipher = await pending!.derive(serverKey, registered.id);
    // The key goes to the vault first: a device is only recorded once its key is safe.
    await _opts.keyVault.save(_opts.keyName, cipher);
    await store.setMeta({'device': registered.json, 'key': null, 'cursor': null, 'snapshot': null});
    return _Session(registered, cipher);
  }

  /// The device key from the vault. A key that an older version stored in
  /// the database is moved into the vault and deleted from the database.
  Future<SessionCipher?> _storedCipher() async {
    final legacy = await store.getMeta('key');
    if (legacy is String) {
      final cipher = await _opts.keyVault.importRaw(fromBase64Url(legacy));
      await _opts.keyVault.save(_opts.keyName, cipher);
      await store.setMeta({'key': null});
      return cipher;
    }
    return _opts.keyVault.load(_opts.keyName);
  }

  Future<Map<String, Object?>> _call(_Session s, Endpoint endpoint, String method, String path, [Object? body]) async {
    final d = s.device;
    final cipher = s.cipher;
    Object? resp;
    if (cipher == null) {
      resp = await _transport.call(method, path, device: d.id, body: body);
    } else {
      final kid = d.serverKid ?? '';
      final payload = body == null
          ? null
          : await cipher.seal(kid, d.id, aad(Direction.req, endpoint, d.tenantId, d.id), jsonEncode(body));
      final env = await _transport.call(method, path, device: d.id, body: payload);
      resp = jsonDecode(await cipher.open(env, aad(Direction.res, endpoint, d.tenantId, d.id)));
    }
    if (resp is! Map) throw FormatException('golaravel: malformed ${endpoint.name} response');
    return resp.cast();
  }

  Future<void> _pushAll(_Session s) async {
    final size = max(1, _opts.batchSize);
    for (;;) {
      final candidates = await store.pending(size + 200);
      if (candidates.isEmpty) return;
      // Never split a group across batches: the server applies a group
      // only when all of it arrives together.
      var n = min(size, candidates.length);
      final g = candidates[n - 1].group;
      while (g != null && n < candidates.length && candidates[n].group == g) {
        n++;
      }
      final batch = candidates.sublist(0, n);

      final resp = await _call(s, Endpoint.push, 'POST', '/push', {
        'device_id': s.device.id,
        'mutations': [for (final m in batch) m.toJson()],
      });
      final results = resp['results'];
      if (results is! List || results.length != batch.length) {
        throw const FormatException('golaravel: malformed push response');
      }
      _clock.observe(resp['server_hlc'] as String?);
      final parsed = [for (final r in results) PushResult.fromJson((r as Map).cast())];
      final applied = await store.applyResults(batch, parsed);
      final pending = await store.pendingCount();
      _setStatus((st) => st.copyWith(pending: pending));
      _changed(applied.touched);
      if (applied.rejected.isNotEmpty && !_rejected.isClosed) _rejected.add(applied.rejected);
      if (parsed.any((r) => r.status == Status.retry)) throw _RetryLater();
    }
  }

  Future<void> _pullAll(_Session s) async {
    var cursor = (await store.getMeta('cursor') as num?)?.toInt() ?? 0;
    for (;;) {
      final resp = await _call(s, Endpoint.pull, 'GET', '/pull?cursor=$cursor&limit=${_opts.pullLimit}');
      _clock.observe(resp['server_hlc'] as String?);
      final changes = [for (final c in (resp['changes'] as List?) ?? const []) RemoteChange.fromJson((c as Map).cast())];
      cursor = (resp['cursor'] as num?)?.toInt() ?? cursor;
      _changed(await store.applyChanges(changes, {'cursor': cursor}));
      if (resp['has_more'] != true) return;
    }
  }

  Future<void> _snapshotAll(_Session s) async {
    int? start;
    for (final entity in _opts.entities) {
      final seen = <String>{};
      var after = '';
      var complete = true;
      for (;;) {
        Map<String, Object?> page;
        try {
          page = await _call(s, Endpoint.snapshot, 'GET',
              '/snapshot?entity=${Uri.encodeQueryComponent(entity)}&after=${Uri.encodeQueryComponent(after)}&limit=${_opts.pullLimit}');
        } on GoLaravelHttpError catch (err) {
          if (err.code == 'unknown_entity') {
            complete = false; // not snapshot-able on the server: pull only
            break;
          }
          rethrow;
        }
        if (after.isEmpty) start = min(start ?? (_never), (page['cursor'] as num?)?.toInt() ?? 0);
        final records = [for (final r in (page['records'] as List?) ?? const []) ServerRecord.fromJson((r as Map).cast())];
        seen.addAll(records.map((r) => r.id));
        _changed(await store.applySnapshot(entity, records));
        final next = page['next'] as String?;
        if (page['has_more'] != true || next == null || next.isEmpty) break;
        after = next;
      }
      if (complete) _changed(await store.dropMissing(entity, seen));
    }
    await store.setMeta({'cursor': start ?? 0, 'snapshot': true});
  }
}

class _Options {
  _Options({
    required this.url,
    required this.entities,
    required this.headers,
    required this.client,
    required this.ownsClient,
    required this.encrypt,
    required this.device,
    required this.batchSize,
    required this.pullLimit,
    required this.pollInterval,
    required this.writeDelay,
    required this.backoffInitial,
    required this.backoffMax,
    required this.keepAcknowledged,
    required this.timeout,
    required this.now,
    required this.random,
    required this.keyVault,
    required this.keyName,
  });

  final Uri url;
  final List<String> entities;
  final HeadersBuilder? headers;
  final http.Client client;
  final bool ownsClient;
  final bool encrypt;
  final DeviceInfo device;
  final int batchSize;
  final int pullLimit;
  final Duration pollInterval;
  final Duration writeDelay;
  final Duration backoffInitial;
  final Duration backoffMax;
  final Duration keepAcknowledged;
  final Duration timeout;
  final int Function() now;
  final Random random;
  final DeviceKeyVault keyVault;
  final String keyName;
}

/// One entity type's local view. Rows are maps with an `id` key.
class SyncTable {
  SyncTable._(this._client, this.entity);

  final SyncClient _client;
  final String entity;

  Future<Map<String, Object?>?> get(String id) => _client.store.get(entity, id);

  Future<List<Map<String, Object?>>> all() => _client.store.all(entity);

  /// Creates an entity offline; returns its (client-generated) id.
  Future<String> create(Map<String, Object?> fields, {String? id}) async {
    final eid = id ?? ulid();
    await _client.write([LocalChange(entity, eid, Op.create, set: fields)]);
    return eid;
  }

  Future<void> update(String id, Map<String, Object?> fields) =>
      _client.write([LocalChange(entity, id, Op.update, set: fields)]);

  /// Adds to a counter field; concurrent increments from devices add up.
  Future<void> increment(String id, String field, num by) =>
      _client.write([LocalChange(entity, id, Op.update, inc: {field: by})]);

  Future<void> delete(String id) => _client.write([LocalChange(entity, id, Op.delete)]);

  /// All rows now and whenever they change, for a StreamBuilder.
  Stream<List<Map<String, Object?>>> watch() {
    late final StreamController<List<Map<String, Object?>>> ctrl;
    StreamSubscription<List<Touched>>? sub;
    var queue = Future<void>.value();
    // Reads run one after another, so results arrive in order.
    void refresh() => queue = queue.then((_) async {
          if (ctrl.isClosed) return;
          try {
            final rows = await all();
            if (!ctrl.isClosed) ctrl.add(rows);
          } catch (err, st) {
            if (!ctrl.isClosed) ctrl.addError(err, st);
          }
        });
    ctrl = StreamController(
      onListen: () {
        sub = _client.changes.listen((touched) {
          if (touched.any((t) => t.entity == entity)) refresh();
        });
        refresh();
      },
      onCancel: () async {
        await sub?.cancel();
        await ctrl.close();
      },
    );
    return ctrl.stream;
  }
}

/// Collects the changes of a [SyncClient.batch] call.
class SyncBatch {
  SyncBatch._();

  final List<LocalChange> changes = [];

  String create(String entity, Map<String, Object?> fields, {String? id}) {
    final eid = id ?? ulid();
    changes.add(LocalChange(entity, eid, Op.create, set: fields));
    return eid;
  }

  void update(String entity, String id, Map<String, Object?> fields) =>
      changes.add(LocalChange(entity, id, Op.update, set: fields));

  void increment(String entity, String id, String field, num by) =>
      changes.add(LocalChange(entity, id, Op.update, inc: {field: by}));

  void delete(String entity, String id) => changes.add(LocalChange(entity, id, Op.delete));
}
