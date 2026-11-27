// Web: WebCrypto keys that page script can use but never read, stored as
// CryptoKey objects in IndexedDB, exactly like @golaravel/offline.

import 'dart:async';
import 'dart:convert';
import 'dart:js_interop';
import 'dart:js_interop_unsafe';
import 'dart:typed_data';

import 'package:web/web.dart' as web;

import 'crypto.dart';
import 'key_vault.dart';

DeviceKeyVault platformKeyVault() => WebCryptoKeyVault();

const _info = 'golaravel-offline/1 aes-256-gcm';

web.SubtleCrypto get _subtle => web.window.crypto.subtle;

JSObject _obj(Map<String, JSAny?> fields) {
  final o = JSObject();
  fields.forEach((k, v) => o.setProperty(k.toJS, v));
  return o;
}

JSArray<JSString> _usages(List<String> u) => [for (final s in u) s.toJS].toJS;

Future<T> _await<T extends JSAny?>(JSPromise<JSAny?> p) async => (await p.toDart) as T;

/// The result of generateKey for an asymmetric algorithm.
extension type _KeyPair._(JSObject _) implements JSObject {
  external web.CryptoKey get publicKey;
  external web.CryptoKey get privateKey;
}

/// Non-extractable WebCrypto keys in IndexedDB.
class WebCryptoKeyVault implements DeviceKeyVault {
  WebCryptoKeyVault({this.databaseName = 'golaravel-offline-keys'});

  /// IndexedDB database holding the keys.
  final String databaseName;
  static const _store = 'keys';

  @override
  Future<PendingDeviceKey> generate() async {
    final pair = await _await<_KeyPair>(
        _subtle.generateKey(_obj({'name': 'ECDH'.toJS, 'namedCurve': 'P-256'.toJS}), false, _usages(['deriveBits'])));
    final raw = await _await<JSArrayBuffer>(_subtle.exportKey('raw', pair.publicKey));
    return _WebPending(pair.privateKey, toBase64Url(raw.toDart.asUint8List()));
  }

  @override
  Future<SessionCipher?> load(String name) async {
    final db = await _open();
    try {
      final key = await _request(db.transaction(_store.toJS, 'readonly').objectStore(_store).get(name.toJS));
      return key == null ? null : WebCipher(key as web.CryptoKey);
    } finally {
      db.close();
    }
  }

  @override
  Future<void> save(String name, SessionCipher cipher) async {
    if (cipher is! WebCipher) throw ArgumentError('WebCryptoKeyVault stores only its own keys');
    final db = await _open();
    try {
      final tx = db.transaction(_store.toJS, 'readwrite');
      await _request(tx.objectStore(_store).put(cipher.key, name.toJS));
      await _complete(tx);
    } finally {
      db.close();
    }
  }

  @override
  Future<void> delete(String name) async {
    final db = await _open();
    try {
      final tx = db.transaction(_store.toJS, 'readwrite');
      await _request(tx.objectStore(_store).delete(name.toJS));
      await _complete(tx);
    } finally {
      db.close();
    }
  }

  @override
  Future<SessionCipher> importRaw(Uint8List key) async => WebCipher(await _await<web.CryptoKey>(
      _subtle.importKey('raw', key.toJS, _obj({'name': 'AES-GCM'.toJS}), false, _usages(['encrypt', 'decrypt']))));

  Future<web.IDBDatabase> _open() {
    final c = Completer<web.IDBDatabase>();
    final r = web.window.indexedDB.open(databaseName, 1);
    r.onupgradeneeded = ((web.Event _) {
      (r.result as web.IDBDatabase).createObjectStore(_store);
    }).toJS;
    r.onsuccess = ((web.Event _) => c.complete(r.result as web.IDBDatabase)).toJS;
    r.onerror = ((web.Event _) => c.completeError(StateError('IndexedDB $databaseName: ${r.error?.message}'))).toJS;
    r.onblocked = ((web.Event _) => c.completeError(StateError('IndexedDB $databaseName is blocked'))).toJS;
    return c.future;
  }

  static Future<JSAny?> _request(web.IDBRequest r) {
    final c = Completer<JSAny?>();
    r.onsuccess = ((web.Event _) => c.complete(r.result)).toJS;
    r.onerror = ((web.Event _) => c.completeError(StateError('IndexedDB: ${r.error?.message}'))).toJS;
    return c.future;
  }

  static Future<void> _complete(web.IDBTransaction tx) {
    final c = Completer<void>();
    tx.oncomplete = ((web.Event _) => c.complete()).toJS;
    tx.onerror = ((web.Event _) => c.completeError(StateError('IndexedDB: ${tx.error?.message}'))).toJS;
    tx.onabort = ((web.Event _) => c.completeError(StateError('IndexedDB transaction aborted'))).toJS;
    return c.future;
  }
}

class _WebPending implements PendingDeviceKey {
  _WebPending(this._private, this.publicKey);

  final web.CryptoKey _private;

  @override
  final String publicKey;

  @override
  Future<SessionCipher> derive(String serverPublicKey, String deviceId) async {
    final server = await _await<web.CryptoKey>(_subtle.importKey('raw', fromBase64Url(serverPublicKey).toJS,
        _obj({'name': 'ECDH'.toJS, 'namedCurve': 'P-256'.toJS}), false, _usages(const [])));
    final secret = await _await<JSArrayBuffer>(
        _subtle.deriveBits(_obj({'name': 'ECDH'.toJS, 'public': server}), _private, 256));
    final hkdf = await _await<web.CryptoKey>(
        _subtle.importKey('raw', secret, 'HKDF'.toJS, false, _usages(['deriveKey'])));
    final key = await _await<web.CryptoKey>(_subtle.deriveKey(
      _obj({
        'name': 'HKDF'.toJS,
        'hash': 'SHA-256'.toJS,
        'salt': utf8.encode(deviceId).toJS,
        'info': utf8.encode(_info).toJS,
      }),
      hkdf,
      _obj({'name': 'AES-GCM'.toJS, 'length': 256.toJS}),
      false,
      _usages(['encrypt', 'decrypt']),
    ));
    return WebCipher(key);
  }
}

/// AES-256-GCM with a non-extractable WebCrypto key.
class WebCipher implements SessionCipher {
  WebCipher(this.key);

  final web.CryptoKey key;

  @override
  Future<Map<String, Object?>> seal(String kid, String deviceId, Uint8List aad, String plaintext) async {
    final iv = Uint8List(12);
    web.window.crypto.getRandomValues(iv.toJS);
    final ct = await _await<JSArrayBuffer>(_subtle.encrypt(
        _obj({'name': 'AES-GCM'.toJS, 'iv': iv.toJS, 'additionalData': aad.toJS}), key, utf8.encode(plaintext).toJS));
    return {'v': 1, 'kid': kid, 'device_id': deviceId, 'iv': toBase64Url(iv), 'ct': toBase64Url(ct.toDart.asUint8List())};
  }

  @override
  Future<String> open(Object? env, Uint8List aad) async {
    if (env is! Map || env['v'] != 1 || env['iv'] is! String || env['ct'] is! String) throw EnvelopeError();
    try {
      final pt = await _await<JSArrayBuffer>(_subtle.decrypt(
          _obj({'name': 'AES-GCM'.toJS, 'iv': fromBase64Url(env['iv'] as String).toJS, 'additionalData': aad.toJS}),
          key,
          fromBase64Url(env['ct'] as String).toJS));
      return utf8.decode(pt.toDart.asUint8List());
    } catch (_) {
      throw EnvelopeError();
    }
  }
}
