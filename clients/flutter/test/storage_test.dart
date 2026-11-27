import 'package:flutter_test/flutter_test.dart';
import 'package:golaravel/golaravel.dart';
import 'package:shared_preferences_platform_interface/in_memory_shared_preferences_async.dart';
import 'package:shared_preferences_platform_interface/shared_preferences_async_platform_interface.dart';

void main() {
  test('SharedPreferencesFeatureStorage round-trips', () async {
    SharedPreferencesAsyncPlatform.instance = InMemorySharedPreferencesAsync.empty();
    final s = SharedPreferencesFeatureStorage();
    expect(await s.read('k'), isNull);
    await s.write('k', '{"features":{}}');
    expect(await s.read('k'), '{"features":{}}');
  });
}
