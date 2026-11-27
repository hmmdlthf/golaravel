// Where a device's encryption key lives. The key is never stored in the
// sync database:
//
//   Android, iOS, macOS, Windows, Linux  the platform keystore, through
//                                        flutter_secure_storage (Keychain,
//                                        Android Keystore, DPAPI, libsecret)
//   web                                  a non-extractable WebCrypto key in
//                                        IndexedDB: page script can use it but
//                                        never read it (like @golaravel/offline)

import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/foundation.dart' show visibleForTesting;
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

import 'crypto.dart';
import 'key_vault_stub.dart' if (dart.library.js_interop) 'key_vault_web.dart' as platform;

/// Encrypts and decrypts envelopes with one device's key.
abstract interface class SessionCipher {
  Future<Map<String, Object?>> seal(String kid, String deviceId, Uint8List aad, String plaintext);

  /// Throws [EnvelopeError] if the envelope was tampered with.
  Future<String> open(Object? envelope, Uint8List aad);
}

/// A freshly generated key pair, before the server's key is known.
abstract interface class PendingDeviceKey {
  /// The uncompressed P-256 public point, base64url.
  String get publicKey;

  /// Derives the shared AES-256-GCM key (ECDH, then HKDF salted with the device id).
  Future<SessionCipher> derive(String serverPublicKey, String deviceId);
}

/// Creates, stores and loads device keys.
abstract interface class DeviceKeyVault {
  Future<PendingDeviceKey> generate();
  Future<SessionCipher?> load(String name);
  Future<void> save(String name, SessionCipher cipher);
  Future<void> delete(String name);

  /// Wraps raw key bytes (from an older version that kept them in the database).
  Future<SessionCipher> importRaw(Uint8List key);
}

/// The vault for this platform: [WebCryptoKeyVault]-style non-extractable
/// keys on web, [NativeKeyVault] with the platform keystore elsewhere.
DeviceKeyVault platformKeyVault() => platform.platformKeyVault();

/// Stores secrets; [NativeKeyVault] keeps keys in one of these.
abstract interface class SecretStore {
  Future<String?> read(String key);
  Future<void> write(String key, String value);
  Future<void> delete(String key);
}

/// The platform keystore, through flutter_secure_storage. On Linux this
/// needs libsecret (and a running keyring) on the device.
class SecureStorageSecretStore implements SecretStore {
  SecureStorageSecretStore([FlutterSecureStorage? storage]) : _s = storage ?? const FlutterSecureStorage();

  final FlutterSecureStorage _s;

  @override
  Future<String?> read(String key) => _s.read(key: key);

  @override
  Future<void> write(String key, String value) => _s.write(key: key, value: value);

  @override
  Future<void> delete(String key) => _s.delete(key: key);
}

/// Keeps secrets in memory only. For tests.
class MemorySecretStore implements SecretStore {
  final Map<String, String> values = {};

  @override
  Future<String?> read(String key) async => values[key];

  @override
  Future<void> write(String key, String value) async => values[key] = value;

  @override
  Future<void> delete(String key) async => values.remove(key);
}

/// Pure-Dart keys (pointycastle) kept in a [SecretStore], the platform
/// keystore by default.
class NativeKeyVault implements DeviceKeyVault {
  NativeKeyVault([SecretStore? secrets]) : _secrets = secrets ?? SecureStorageSecretStore();

  final SecretStore _secrets;

  @override
  Future<PendingDeviceKey> generate() async => _NativePending(DeviceKeyPair.generate());

  @override
  Future<SessionCipher?> load(String name) async {
    final b64 = await _secrets.read(name);
    return b64 == null ? null : NativeCipher(fromBase64Url(b64));
  }

  @override
  Future<void> save(String name, SessionCipher cipher) {
    if (cipher is! NativeCipher) throw ArgumentError('NativeKeyVault stores only its own keys');
    return _secrets.write(name, toBase64Url(cipher._key));
  }

  @override
  Future<void> delete(String name) => _secrets.delete(name);

  @override
  Future<SessionCipher> importRaw(Uint8List key) async => NativeCipher(Uint8List.fromList(key));
}

class _NativePending implements PendingDeviceKey {
  _NativePending(this._pair);

  final DeviceKeyPair _pair;

  @override
  String get publicKey => _pair.publicKey;

  @override
  Future<SessionCipher> derive(String serverPublicKey, String deviceId) async =>
      NativeCipher(_pair.deriveKey(serverPublicKey, deviceId));
}

/// AES-256-GCM with a key held in Dart memory (pointycastle).
class NativeCipher implements SessionCipher {
  NativeCipher(this._key);

  final Uint8List _key;

  @override
  Future<Map<String, Object?>> seal(String kid, String deviceId, Uint8List aad, String plaintext) async =>
      sealEnvelope(_key, kid, deviceId, aad, plaintext);

  @override
  Future<String> open(Object? envelope, Uint8List aad) async => openEnvelope(_key, envelope, aad);
}

/// For tests: whether [cipher] holds exactly [key].
@visibleForTesting
bool cipherHasKey(SessionCipher cipher, Uint8List key) =>
    cipher is NativeCipher && base64.encode(cipher._key) == base64.encode(key);

