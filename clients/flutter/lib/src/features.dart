// Client for pkg/features' public endpoint (Manager.Handler): the tenant's
// Public features, cached locally so an offline device still knows which
// features are on. Mirrors FeatureClient in @golaravel/offline.

import 'dart:async';
import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:http/http.dart' as http;

import 'errors.dart';
import 'storage.dart';

/// Returns headers for each request, e.g. auth and tenant.
typedef HeadersBuilder = FutureOr<Map<String, String>> Function();

/// The tenant's public features, kept up to date in the background.
///
/// ```dart
/// final features = await FeatureClient.open(
///   url: Uri.parse('https://pos.example.com/api/features'),
///   headers: () async => {'Authorization': 'Bearer ${await token()}'},
///   storageKey: 'features:$tenantId',
/// );
/// if (features.enabled('invoices.recurring')) showRecurring();
///
/// // Rebuild widgets when features change:
/// ListenableBuilder(listenable: features, builder: (context, _) => ...);
/// ```
///
/// Reads are synchronous and never wait for the network. They answer from
/// the last known values, cached across app starts, until the server is
/// reachable. A feature the client has never seen is off.
///
/// It is a [ChangeNotifier]: listeners run when the features change. The
/// [changes] and [errors] streams report the same events.
class FeatureClient extends ChangeNotifier with WidgetsBindingObserver {
  FeatureClient._({
    required this.url,
    required HeadersBuilder? headers,
    required http.Client? client,
    required FeatureStorage? storage,
    required String? storageKey,
    required this.pollInterval,
    required this.timeout,
    required DateTime Function() now,
  })  : _headers = headers,
        _client = client ?? http.Client(),
        _ownsClient = client == null,
        _storage = storage,
        storageKey = storageKey ?? 'golaravel-features:$url',
        _now = now;

  /// Loads the cached features, then (with [autoStart]) refreshes in the
  /// background, polls every [pollInterval] and refreshes when the app
  /// comes back to the foreground.
  ///
  /// [storage] defaults to [SharedPreferencesFeatureStorage]. Pass
  /// [MemoryFeatureStorage] to keep nothing between app starts. Include the
  /// tenant in [storageKey] so tenants never share a cache.
  static Future<FeatureClient> open({
    required Uri url,
    HeadersBuilder? headers,
    http.Client? client,
    FeatureStorage? storage,
    String? storageKey,
    Duration pollInterval = const Duration(seconds: 60),
    Duration timeout = const Duration(seconds: 30),
    bool autoStart = true,
    DateTime Function()? now,
  }) async {
    final c = FeatureClient._(
      url: url,
      headers: headers,
      client: client,
      storage: storage ?? SharedPreferencesFeatureStorage(),
      storageKey: storageKey,
      pollInterval: pollInterval,
      timeout: timeout,
      now: now ?? DateTime.now,
    );
    await c._loadCache();
    if (autoStart) c.start();
    return c;
  }

  final Uri url;
  final String storageKey;

  /// How often to check for changes. [Duration.zero] disables polling.
  final Duration pollInterval;
  final Duration timeout;

  final HeadersBuilder? _headers;
  final http.Client _client;
  final bool _ownsClient;
  final FeatureStorage? _storage;
  final DateTime Function() _now;

  Map<String, Object?> _features = const {};
  String? _etag;
  DateTime? _fetchedAt;
  Future<bool>? _inflight;
  Timer? _timer;
  bool _observing = false;
  bool _closed = false;
  final _changes = StreamController<Map<String, Object?>>.broadcast();
  final _errors = StreamController<Object>.broadcast();

  /// True only when the feature's value is exactly `true`.
  bool enabled(String key) => _features[key] == true;

  /// The feature's value, or [fallback] when unknown or of another type.
  T value<T>(String key, T fallback) {
    final v = _features[key];
    return v is T ? v : fallback;
  }

  /// Every known feature (an unmodifiable copy).
  Map<String, Object?> get all => Map.unmodifiable(_copy(_features));

  /// When the server last confirmed the current values; null if never.
  DateTime? get fetchedAt => _fetchedAt;

  /// Fires with every feature after a change.
  Stream<Map<String, Object?>> get changes => _changes.stream;

  /// Fires when a refresh fails; the last known features stay in use.
  Stream<Object> get errors => _errors.stream;

  /// Starts polling and refreshing when the app comes back to the
  /// foreground. Called by [open] unless `autoStart` is false.
  void start() {
    if (_closed) return;
    if (!_observing) {
      try {
        WidgetsBinding.instance.addObserver(this);
        _observing = true;
      } catch (_) {
        // No Flutter binding (pure Dart tests): polling still works.
      }
    }
    unawaited(refresh().catchError((_) => false));
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) unawaited(refresh().catchError((_) => false));
  }

  /// Asks the server for the current features. Completes with true when
  /// they changed. Throws [GoLaravelNetworkError] or [GoLaravelHttpError] on
  /// failure (also sent to [errors]); the last known features stay in use.
  /// Concurrent calls share one request.
  Future<bool> refresh() {
    if (_closed) return Future.value(false);
    return _inflight ??= _fetch().whenComplete(() {
      _inflight = null;
      _schedule();
    });
  }

  /// Stops polling and lifecycle listening. Cached values stay readable.
  void close() {
    if (_closed) return;
    _closed = true;
    _timer?.cancel();
    if (_observing) WidgetsBinding.instance.removeObserver(this);
    if (_ownsClient) _client.close();
    unawaited(_changes.close());
    unawaited(_errors.close());
  }

  @override
  void dispose() {
    close();
    super.dispose();
  }

  void _schedule() {
    _timer?.cancel();
    if (_closed || pollInterval <= Duration.zero) return;
    _timer = Timer(pollInterval, () => unawaited(refresh().catchError((_) => false)));
  }

  Future<bool> _fetch() async {
    try {
      return await _request();
    } catch (err) {
      if (!_errors.isClosed) _errors.add(err);
      rethrow;
    }
  }

  Future<bool> _request() async {
    final headers = <String, String>{
      if (_headers != null) ...await _headers(),
      'Accept': 'application/json',
      if (_etag != null) 'If-None-Match': _etag!,
    };
    http.Response resp;
    try {
      resp = await _client.get(url, headers: headers).timeout(timeout);
    } catch (err) {
      throw GoLaravelNetworkError(err);
    }
    if (resp.statusCode == 304) {
      _fetchedAt = _now();
      await _saveCache();
      return false;
    }
    Object? json;
    try {
      json = jsonDecode(utf8.decode(resp.bodyBytes));
    } catch (_) {
      json = null;
    }
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      final e = json is Map ? json : const {};
      throw GoLaravelHttpError(
          resp.statusCode, e['error'] as String? ?? 'http_error', e['message'] as String? ?? '');
    }
    final features = json is Map ? json['features'] : null;
    if (features is! Map) {
      throw GoLaravelHttpError(resp.statusCode, 'bad_response', 'features endpoint returned no features object');
    }
    final next = features.cast<String, Object?>();
    final changed = jsonEncode(next) != jsonEncode(_features);
    _features = next;
    _etag = resp.headers['etag'];
    _fetchedAt = _now();
    await _saveCache();
    if (changed && !_closed) {
      _changes.add(all);
      notifyListeners();
    }
    return changed;
  }

  Future<void> _loadCache() async {
    final storage = _storage;
    if (storage == null) return;
    try {
      final raw = await storage.read(storageKey);
      if (raw == null) return;
      final s = jsonDecode(raw);
      if (s is Map && s['features'] is Map) {
        _features = (s['features'] as Map).cast<String, Object?>();
        _etag = s['etag'] as String?;
        final at = s['fetchedAt'];
        _fetchedAt = at is int ? DateTime.fromMillisecondsSinceEpoch(at) : null;
      }
    } catch (_) {
      // Unreadable cache: start empty.
    }
  }

  Future<void> _saveCache() async {
    final storage = _storage;
    if (storage == null) return;
    try {
      await storage.write(
          storageKey,
          jsonEncode({
            'features': _features,
            if (_etag != null) 'etag': _etag,
            if (_fetchedAt != null) 'fetchedAt': _fetchedAt!.millisecondsSinceEpoch,
          }));
    } catch (_) {
      // Storage full or blocked: keep working from memory.
    }
  }

  static Map<String, Object?> _copy(Map<String, Object?> m) =>
      (jsonDecode(jsonEncode(m)) as Map).cast<String, Object?>();
}
