// The web key vault: WebCrypto keys that cannot be exported, kept in
// IndexedDB, and wire-compatible with the pure-Dart (native) cipher, which
// e2e_test.dart checks against the Go server. Runs with --platform chrome.
@TestOn('browser')
library;

import 'dart:js_interop';

import 'package:flutter_test/flutter_test.dart';
import 'package:golaravel/golaravel.dart';
import 'package:golaravel/src/sync/crypto.dart';
import 'package:golaravel/src/sync/key_vault_web.dart' hide platformKeyVault;
import 'package:web/web.dart' as web;

void main() {
  final ad = aad(Direction.req, Endpoint.push, 'acme', 'dev-1');

  test('platformKeyVault is the WebCrypto vault on web', () {
    expect(platformKeyVault(), isA<WebCryptoKeyVault>());
  });

  test('derives the same key as the server side and interoperates with the native cipher', () async {
    final vault = WebCryptoKeyVault(databaseName: 'golaravel-keys-${DateTime.now().microsecondsSinceEpoch}');
    final device = await vault.generate();
    final server = DeviceKeyPair.generate(); // pure Dart, stands in for the Go server
    expect(fromBase64Url(device.publicKey), hasLength(65));

    final web = await device.derive(server.publicKey, 'dev-1');
    final serverKey = server.deriveKey(device.publicKey, 'dev-1');

    final fromWeb = await web.seal('k1', 'dev-1', ad, '{"x":1}');
    expect(openEnvelope(serverKey, fromWeb, ad), '{"x":1}');
    final fromServer = sealEnvelope(serverKey, 'k1', 'dev-1', ad, '{"y":"é"}');
    expect(await web.open(fromServer, ad), '{"y":"é"}');

    await expectLater(web.open(fromServer, aad(Direction.req, Endpoint.pull, 'acme', 'dev-1')),
        throwsA(isA<EnvelopeError>()));
  });

  test('keys persist in IndexedDB, cannot be exported, and can be deleted', () async {
    final name = 'golaravel-keys-${DateTime.now().microsecondsSinceEpoch}';
    final server = DeviceKeyPair.generate();
    final pending = await WebCryptoKeyVault(databaseName: name).generate();
    final cipher = await pending.derive(server.publicKey, 'dev-1') as WebCipher;
    await WebCryptoKeyVault(databaseName: name).save('device', cipher);

    // A new vault instance (a page reload) loads the same key.
    final loaded = await WebCryptoKeyVault(databaseName: name).load('device');
    expect(loaded, isNotNull);
    final env = await loaded!.seal('k1', 'dev-1', ad, 'hello');
    expect(openEnvelope(server.deriveKey(pending.publicKey, 'dev-1'), env, ad), 'hello');

    // Page script can use the key but not read it.
    expect((loaded as WebCipher).key.extractable, isFalse);
    await expectLater(web.window.crypto.subtle.exportKey('raw', loaded.key).toDart, throwsA(anything));

    await WebCryptoKeyVault(databaseName: name).delete('device');
    expect(await WebCryptoKeyVault(databaseName: name).load('device'), isNull);
  });

  test('importRaw migrates an old raw key into a non-extractable one', () async {
    final raw = DeviceKeyPair.generate().deriveKey(DeviceKeyPair.generate().publicKey, 'dev-1');
    final cipher = await WebCryptoKeyVault().importRaw(raw) as WebCipher;
    expect(cipher.key.extractable, isFalse);
    expect(openEnvelope(raw, await cipher.seal('k1', 'dev-1', ad, 'm'), ad), 'm');
  });
}
