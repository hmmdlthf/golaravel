import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:golaravel/golaravel.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

/// A stand-in for Manager.Handler: ETag + If-None-Match, errors on demand.
class FakeFeatures {
  Map<String, Object?> features = {
    'invoices.recurring': false,
    'ui.theme': {'color': 'blue'},
  };
  bool offline = false;
  int status = 200;
  final requests = <http.BaseRequest>[];

  String get tag => '"${base64.encode(utf8.encode(jsonEncode(features)))}"';

  late final client = MockClient((req) async {
    requests.add(req);
    if (offline) throw http.ClientException('connection refused');
    if (status != 200) return http.Response(jsonEncode({'error': 'Tenant Required'}), status);
    if (req.headers['If-None-Match'] == tag) return http.Response('', 304);
    return http.Response(jsonEncode({'features': features}), 200, headers: {'etag': tag});
  });
}

Future<FeatureClient> open(FakeFeatures srv, {FeatureStorage? storage, HeadersBuilder? headers}) =>
    FeatureClient.open(
      url: Uri.parse('https://api.test/features'),
      client: srv.client,
      storage: storage ?? MemoryFeatureStorage(),
      headers: headers,
      autoStart: false,
      pollInterval: Duration.zero,
    );

void main() {
  test('unknown features are off until the server answers', () async {
    final c = await open(FakeFeatures());
    expect(c.enabled('invoices.recurring'), isFalse);
    expect(c.value<Map?>('ui.theme', null), isNull);
    expect(c.fetchedAt, isNull);
  });

  test('refresh loads values, then revalidates with the ETag', () async {
    final srv = FakeFeatures();
    final c = await open(srv, headers: () => {'Authorization': 'Bearer t'});
    var notified = 0;
    c.addListener(() => notified++);
    final changes = <Map<String, Object?>>[];
    c.changes.listen(changes.add);

    expect(await c.refresh(), isTrue);
    expect(c.value<Map>('ui.theme', {}), {'color': 'blue'});
    expect(srv.requests.first.headers['Authorization'], 'Bearer t');
    expect(srv.requests.first.headers['If-None-Match'], isNull);

    expect(await c.refresh(), isFalse); // 304
    expect(srv.requests[1].headers['If-None-Match'], srv.tag);

    srv.features = {...srv.features, 'invoices.recurring': true};
    expect(await c.refresh(), isTrue);
    expect(c.enabled('invoices.recurring'), isTrue);
    await Future<void>.delayed(Duration.zero);
    expect(notified, 2);
    expect(changes, hasLength(2));
  });

  test('keeps the last known features offline and across restarts', () async {
    final srv = FakeFeatures()..features['invoices.recurring'] = true;
    final storage = MemoryFeatureStorage();
    final first = await open(srv, storage: storage);
    await first.refresh();
    first.close();

    srv.offline = true;
    final second = await open(srv, storage: storage);
    expect(second.enabled('invoices.recurring'), isTrue); // from cache, no network
    expect(second.fetchedAt, isNotNull);
    final errors = <Object>[];
    second.errors.listen(errors.add);
    await expectLater(second.refresh(), throwsA(isA<GoLaravelNetworkError>()));
    expect(second.enabled('invoices.recurring'), isTrue);
    await Future<void>.delayed(Duration.zero);
    expect(errors, hasLength(1));

    // Back online: the cached ETag still validates.
    srv.offline = false;
    expect(await second.refresh(), isFalse);
    expect(srv.requests.last.headers['If-None-Match'], srv.tag);
  });

  test('server errors are reported and do not clear values', () async {
    final srv = FakeFeatures();
    final c = await open(srv);
    await c.refresh();
    srv.status = 403;
    await expectLater(
        c.refresh(), throwsA(isA<GoLaravelHttpError>().having((e) => e.status, 'status', 403)));
    expect(c.value<Map>('ui.theme', {}), {'color': 'blue'});
  });

  test('ignores a corrupt cache', () async {
    final storage = MemoryFeatureStorage()..values['golaravel-features:https://api.test/features'] = '{not json';
    final c = await open(FakeFeatures(), storage: storage);
    expect(c.all, isEmpty);
  });

  test('concurrent refreshes share one request', () async {
    final srv = FakeFeatures();
    final c = await open(srv);
    await Future.wait([c.refresh(), c.refresh(), c.refresh()]);
    expect(srv.requests, hasLength(1));
  });

  test('all is an unmodifiable copy; value checks the type', () async {
    final c = await open(FakeFeatures());
    await c.refresh();
    expect(() => c.all['x'] = 1, throwsUnsupportedError);
    (c.all['ui.theme'] as Map)['color'] = 'red';
    expect(c.value<Map>('ui.theme', {}), {'color': 'blue'});
    expect(c.value<int>('ui.theme', 7), 7);
  });

  testWidgets('refreshes when the app resumes', (tester) async {
    final srv = FakeFeatures();
    late FeatureClient c;
    await tester.runAsync(() async {
      c = await FeatureClient.open(
        url: Uri.parse('https://api.test/features'),
        client: srv.client,
        storage: MemoryFeatureStorage(),
        pollInterval: Duration.zero,
      ); // autoStart: observes the lifecycle and refreshes once
      await Future<void>.delayed(const Duration(milliseconds: 10));
    });
    expect(srv.requests, hasLength(1));
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.runAsync(() => Future<void>.delayed(const Duration(milliseconds: 10)));
    expect(srv.requests, hasLength(2));
    c.dispose();
  });
}
