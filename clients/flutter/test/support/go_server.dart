import 'dart:async';
import 'dart:convert';
import 'dart:io';

/// The repository root (this package lives in clients/flutter).
String get repoRoot => Directory.current.parent.parent.path;

/// Builds and starts pkg/offline/internal/e2eserver; returns its base URL.
class GoServer {
  GoServer._(this._process, this.base, this._dir);

  final Process _process;
  final Uri base;
  final Directory _dir;

  static Future<GoServer> start() async {
    final dir = await Directory.systemTemp.createTemp('golaravel-dart-e2e-');
    final bin = '${dir.path}/e2eserver';
    final build = await Process.run(
        Platform.environment['GOLARAVEL_GO'] ?? 'go', ['build', '-o', bin, './pkg/offline/internal/e2eserver'],
        workingDirectory: repoRoot);
    if (build.exitCode != 0) throw StateError('go build failed: ${build.stderr}');
    final p = await Process.start(bin, ['-addr', '127.0.0.1:0']);
    p.stderr.transform(utf8.decoder).listen(stderr.write);
    final addr = await p.stdout
        .transform(utf8.decoder)
        .transform(const LineSplitter())
        .map((l) => RegExp(r'LISTENING (\S+)').firstMatch(l)?.group(1))
        .firstWhere((m) => m != null)
        .timeout(const Duration(seconds: 20));
    return GoServer._(p, Uri.parse(addr!), dir);
  }

  Future<void> stop() async {
    _process.kill();
    await _dir.delete(recursive: true);
  }
}
