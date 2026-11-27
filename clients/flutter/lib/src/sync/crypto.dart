// Payload encryption, matching pkg/offline/crypto.go and @golaravel/offline:
//
//   secret = ECDH-P256(device private, server public)     32 bytes
//   key    = HKDF-SHA256(secret, salt = device id, info = INFO) -> AES-256-GCM
//
// Pure Dart (pointycastle), so it runs on every Flutter platform.

import 'dart:convert';
import 'dart:math';
import 'dart:typed_data';

import 'package:pointycastle/export.dart';

const _info = 'golaravel-offline/1 aes-256-gcm';

String toBase64Url(List<int> bytes) => base64Url.encode(bytes).replaceAll('=', '');

Uint8List fromBase64Url(String s) => base64Url.decode(s.padRight((s.length + 3) ~/ 4 * 4, '='));

final _curve = ECCurve_secp256r1();

SecureRandom _secureRandom() {
  final r = Random.secure();
  return FortunaRandom()..seed(KeyParameter(Uint8List.fromList(List.generate(32, (_) => r.nextInt(256)))));
}

/// A device's ECDH key pair. Only the derived AES key is kept afterwards.
class DeviceKeyPair {
  DeviceKeyPair._(this._private, this.publicKey);

  final ECPrivateKey _private;

  /// The uncompressed public point, base64url (what the server expects).
  final String publicKey;

  static DeviceKeyPair generate() {
    final gen = ECKeyGenerator()..init(ParametersWithRandom(ECKeyGeneratorParameters(_curve), _secureRandom()));
    final pair = gen.generateKeyPair();
    return DeviceKeyPair._(pair.privateKey, toBase64Url(pair.publicKey.Q!.getEncoded(false)));
  }

  /// The per-device AES-256-GCM key shared with the server.
  Uint8List deriveKey(String serverPublicKey, String deviceId) {
    final point = _curve.curve.decodePoint(fromBase64Url(serverPublicKey));
    if (point == null) throw const FormatException('invalid server public key');
    final secret = (ECDHBasicAgreement()..init(_private)).calculateAgreement(ECPublicKey(point, _curve));
    final hkdf = HKDFKeyDerivator(SHA256Digest())
      ..init(HkdfParameters(_bigIntTo32(secret), 32, utf8.encode(deviceId), utf8.encode(_info)));
    final out = Uint8List(32);
    hkdf.deriveKey(null, 0, out, 0);
    return out;
  }
}

Uint8List _bigIntTo32(BigInt v) {
  final out = Uint8List(32);
  var x = v;
  for (var i = 31; i >= 0; i--) {
    out[i] = (x & BigInt.from(0xff)).toInt();
    x = x >> 8;
  }
  return out;
}

enum Direction { req, res }

enum Endpoint { push, pull, snapshot }

/// Binds an envelope to its direction, endpoint, tenant and device.
Uint8List aad(Direction dir, Endpoint endpoint, String tenant, String deviceId) =>
    utf8.encode('golaravel-offline/1\n${dir.name}\n${endpoint.name}\n$tenant\n$deviceId');

class EnvelopeError implements Exception {
  @override
  String toString() => 'EnvelopeError: envelope does not decrypt';
}

/// Encrypts [plaintext] into an envelope.
Map<String, Object?> sealEnvelope(Uint8List key, String kid, String deviceId, Uint8List data, String plaintext) {
  final iv = _secureRandom().nextBytes(12);
  final gcm = GCMBlockCipher(AESEngine())..init(true, AEADParameters(KeyParameter(key), 128, iv, data));
  final ct = gcm.process(utf8.encode(plaintext));
  return {'v': 1, 'kid': kid, 'device_id': deviceId, 'iv': toBase64Url(iv), 'ct': toBase64Url(ct)};
}

/// Decrypts an envelope; throws [EnvelopeError] if it was tampered with.
String openEnvelope(Uint8List key, Object? env, Uint8List data) {
  if (env is! Map || env['v'] != 1 || env['iv'] is! String || env['ct'] is! String) throw EnvelopeError();
  try {
    final gcm = GCMBlockCipher(AESEngine())
      ..init(false, AEADParameters(KeyParameter(key), 128, fromBase64Url(env['iv'] as String), data));
    return utf8.decode(gcm.process(fromBase64Url(env['ct'] as String)));
  } catch (_) {
    throw EnvelopeError();
  }
}
