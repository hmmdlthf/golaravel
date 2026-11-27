import 'package:shared_preferences/shared_preferences.dart';

/// Where [FeatureClient] keeps the last known features between app starts.
abstract interface class FeatureStorage {
  Future<String?> read(String key);
  Future<void> write(String key, String value);
}

/// Keeps nothing across restarts. Useful in tests.
class MemoryFeatureStorage implements FeatureStorage {
  final Map<String, String> values = {};

  @override
  Future<String?> read(String key) async => values[key];

  @override
  Future<void> write(String key, String value) async => values[key] = value;
}

/// Stores features with `shared_preferences`. This is the default on
/// Android, iOS, desktop and web.
class SharedPreferencesFeatureStorage implements FeatureStorage {
  SharedPreferencesFeatureStorage([SharedPreferencesAsync? prefs]) : _prefs = prefs ?? SharedPreferencesAsync();

  final SharedPreferencesAsync _prefs;

  @override
  Future<String?> read(String key) => _prefs.getString(key);

  @override
  Future<void> write(String key, String value) => _prefs.setString(key, value);
}
