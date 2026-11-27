// An in-memory stand-in for pkg/offline, enough to drive SyncClient in unit
// tests (e2e_test.dart runs against the real Go server). A port of
// clients/offline/test/fake-server.ts.
import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

class Row {
  Row(this.data, this.version, this.deleted, this.seq);
  Map<String, Object?> data;
  int version;
  bool deleted;
  int seq;
}

class Failure {
  Failure(this.status, this.code, [this.retryAfter]);
  final int status;
  final String code;
  final int? retryAfter;
}

class FakeSyncServer {
  final rows = <String, Row>{};
  final seen = <String, Map<String, Object?>>{};
  int seq = 0;
  int horizon = 0;
  final devices = <String>{};
  int registered = 0;
  final revoked = <String>{};
  final pushes = <List<Map<String, Object?>>>[];

  /// Throw a network error for the next n requests.
  int offline = 0;
  final failures = <Failure>[];
  final reject = <String>{};
  final retryOnce = <String>{};

  /// Apply the next push but lose its response, like a dropped connection.
  bool dropPushResponse = false;

  String key(String entity, String id) => '$entity/$id';

  late final http.Client client = MockClient((req) async {
    if (offline > 0) {
      offline--;
      throw http.ClientException('connection refused');
    }
    final path = req.url.path.replaceFirst(RegExp(r'^/sync'), '');
    final device = req.headers['X-Sync-Device'] ?? '';
    if (failures.isNotEmpty) {
      final f = failures.removeAt(0);
      return _json(f.status, {'error': f.code, 'message': f.code},
          f.retryAfter == null ? const {} : {'retry-after': '${f.retryAfter}'});
    }
    if (path == '/devices' && req.method == 'POST') {
      final id = 'dev-${++registered}';
      devices.add(id);
      return _json(201, {'id': id, 'tenant_id': 'acme', 'cursor': 0, 'created_at': 0, 'encrypted': false});
    }
    if (!devices.contains(device)) return _json(403, {'error': 'unknown_device'});
    if (revoked.contains(device)) return _json(403, {'error': 'device_revoked'});
    if (path == '/push') {
      final body = _push(((jsonDecode(req.body) as Map)['mutations'] as List).cast<Map>().map((m) => m.cast<String, Object?>()).toList());
      if (dropPushResponse) {
        dropPushResponse = false;
        throw http.ClientException('connection reset');
      }
      return _json(200, body);
    }
    if (path == '/pull') {
      final cursor = int.parse(req.url.queryParameters['cursor']!);
      if (cursor < horizon) return _json(410, {'error': 'resync_required'});
      final limit = int.tryParse(req.url.queryParameters['limit'] ?? '') ?? 500;
      final changed = rows.entries.where((e) => e.value.seq > cursor).toList()
        ..sort((a, b) => a.value.seq.compareTo(b.value.seq));
      final page = changed.take(limit).toList();
      return _json(200, {
        'changes': [
          for (final e in page)
            {
              'entity': e.key.split('/')[0],
              'entity_id': e.key.split('/')[1],
              'seq': e.value.seq,
              'version': e.value.version,
              if (e.value.deleted) 'deleted': true else 'data': e.value.data,
            }
        ],
        'cursor': page.isEmpty ? cursor : page.last.value.seq,
        'has_more': changed.length > limit,
        'server_hlc': '',
      });
    }
    if (path == '/snapshot') {
      final entity = req.url.queryParameters['entity']!;
      if (entity == 'unsynced') return _json(404, {'error': 'unknown_entity'});
      return _json(200, {
        'entity': entity,
        'records': [
          for (final e in rows.entries)
            if (e.key.startsWith('$entity/') && !e.value.deleted)
              {'id': e.key.split('/')[1], 'version': e.value.version, 'data': e.value.data}
        ],
        'has_more': false,
        'cursor': seq,
      });
    }
    return _json(404, {'error': 'not_found'});
  });

  Map<String, Object?> _push(List<Map<String, Object?>> muts) {
    pushes.add(muts);
    final results = <Map<String, Object?>>[];
    var blocked = false;
    for (final m in muts) {
      final id = m['id'] as String;
      if (blocked) {
        results.add({'id': id, 'status': 'retry', 'reason': 'blocked'});
        continue;
      }
      final prev = seen[id];
      if (prev != null) {
        results.add({'id': id, 'status': 'duplicate', 'original': prev['status']});
        continue;
      }
      if (retryOnce.remove(id)) {
        blocked = true;
        results.add({'id': id, 'status': 'retry', 'reason': 'transient'});
        continue;
      }
      final k = key(m['entity'] as String, m['entity_id'] as String);
      final cur = rows[k];
      if (reject.contains(id)) {
        final r = {
          'id': id,
          'status': 'rejected',
          'reason': 'invalid: test',
          'record': cur != null && !cur.deleted
              ? {'id': m['entity_id'], 'version': cur.version, 'data': cur.data}
              : {'id': m['entity_id'], 'version': 0, 'deleted': true},
        };
        seen[id] = r;
        results.add(r);
        continue;
      }
      final data = <String, Object?>{...?(cur != null && !cur.deleted ? cur.data : null), ...?(m['set'] as Map?)?.cast()};
      ((m['inc'] as Map?)?.cast<String, num>() ?? {}).forEach((f, by) => data[f] = ((data[f] as num?) ?? 0) + by);
      rows[k] = Row(data, (cur?.version ?? 0) + 1, m['op'] == 'delete', ++seq);
      final r = {'id': id, 'status': 'applied', 'seq': seq};
      seen[id] = r;
      results.add(r);
    }
    return {'results': results, 'server_hlc': ''};
  }

  /// An edit made on the server (admin panel).
  void edit(String entity, String id, Map<String, Object?> fields) {
    final k = key(entity, id);
    final cur = rows[k];
    rows[k] = Row({...?cur?.data, ...fields}, (cur?.version ?? 0) + 1, false, ++seq);
  }
}

http.Response _json(int status, Object body, [Map<String, String> headers = const {}]) =>
    http.Response(jsonEncode(body), status, headers: {'content-type': 'application/json', ...headers});
