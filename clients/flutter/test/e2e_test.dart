// End-to-end: FeatureClient against the real Go handler (pkg/features
// Manager.Handler) in pkg/offline/internal/e2eserver. Needs Go; runs when
// GOLARAVEL_E2E=1. GOLARAVEL_GO overrides the go binary.
import 'dart:convert';
import 'dart:io';

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
