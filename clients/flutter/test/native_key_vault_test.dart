import 'package:flutter_test/flutter_test.dart';
import 'package:golaravel/golaravel.dart';
import 'package:golaravel/src/sync/key_vault.dart';

void main() {
  test('platformKeyVault uses the platform keystore off the web', () {
    expect(platformKeyVault(), isA<NativeKeyVault>());
  }, testOn: '!browser');

  test('NativeKeyVault stores keys only in its secret store', () async {
    final secrets = MemorySecretStore();
    final vault = NativeKeyVault(secrets);
    final server = DeviceKeyPair.generate();
    final pending = await vault.generate();
    final cipher = await pending.derive(server.publicKey, 'dev-1');
    await vault.save('device', cipher);
    expect(secrets.values.keys, ['device']);

    final loaded = (await vault.load('device'))!;
    expect(cipherHasKey(loaded, server.deriveKey(pending.publicKey, 'dev-1')), isTrue);
    await vault.delete('device');
    expect(await vault.load('device'), isNull);
    expect(secrets.values, isEmpty);
  });
}
