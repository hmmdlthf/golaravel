import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'package:http/http.dart' as http;

import '../errors.dart';
import '../features.dart' show HeadersBuilder;

const deviceHeader = 'X-Sync-Device';

class Transport {
  Transport({required this.url, required this.client, required this.timeout, this.headers});

  final Uri url;
  final http.Client client;
  final Duration timeout;
  final HeadersBuilder? headers;

  Future<Object?> call(String method, String path, {String? device, Object? body}) async {
    final h = <String, String>{
      if (headers != null) ...await headers!(),
      'Accept': 'application/json',
      if (device != null) deviceHeader: device,
      if (body != null) 'Content-Type': 'application/json',
    };
    final base = url.toString().replaceAll(RegExp(r'/+$'), '');
    final req = http.Request(method, Uri.parse('$base$path'))..headers.addAll(h);
    if (body != null) req.body = jsonEncode(body);
    http.Response resp;
    try {
      resp = await http.Response.fromStream(await client.send(req).timeout(timeout)).timeout(timeout);
    } catch (err) {
      throw GoLaravelNetworkError(err);
    }
    if (resp.statusCode == 204) return null;
    Object? json;
    try {
      json = resp.bodyBytes.isEmpty ? null : jsonDecode(utf8.decode(resp.bodyBytes));
    } catch (_) {
      json = null;
    }
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      final e = json is Map ? json : const {};
      final ra = int.tryParse(resp.headers['retry-after'] ?? '');
      throw GoLaravelHttpError(resp.statusCode, e['error'] as String? ?? 'http_error', e['message'] as String? ?? '',
          ra != null && ra > 0 ? ra : null);
    }
    return json;
  }
}

/// Full-jitter exponential backoff: random(0, min(max, initial * 2^attempt)).
Duration backoffDelay(int attempt, Duration initial, Duration max, [Random? random]) {
  final capMs = min(max.inMilliseconds, initial.inMilliseconds * pow(2, min(attempt, 30)).toInt());
  final r = (random ?? Random()).nextDouble();
  return Duration(milliseconds: max2(min(250, capMs), (r * capMs).round()));
}

int max2(int a, int b) => a > b ? a : b;
