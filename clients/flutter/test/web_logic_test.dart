// Pure logic that must behave the same on web (dart2js/dart2wasm number
// semantics differ from native). Runs natively and with --platform chrome.
import 'package:flutter_test/flutter_test.dart';
import 'package:golaravel/golaravel.dart';

void main() {
  test('ulid encodes the full 48-bit time', () {
    final id = ulid(1700000000000);
    expect(id.substring(0, 10), '01HF7YAT00');
    expect(id, matches(RegExp(r'^[0-7][0-9A-HJKMNP-TV-Z]{25}$')));
    final a = ulid(), b = ulid();
    expect(b.compareTo(a) > 0, isTrue);
  });

  test('hlc formats 15-digit walls', () {
    var t = 1764000000123;
    final c = Clock('n', null, () => t);
    expect(c.now(), '001764000000123:00000:n');
    expect(c.now(), '001764000000123:00001:n');
    t++;
    expect(c.now(), '001764000000124:00000:n');
  });

  test('applyLocal counters', () {
    final m = Mutation(id: 'x', entity: 'p', entityId: '1', op: Op.update, hlc: '', at: 0, inc: {'stock': -2});
    expect(applyLocal({'stock': 10}, m), {'stock': 8});
  });

  test('ECDH + HKDF + AES-GCM round trip', () {
    final a = DeviceKeyPair.generate(), b = DeviceKeyPair.generate();
    final k = a.deriveKey(b.publicKey, 'dev-1');
    expect(k, b.deriveKey(a.publicKey, 'dev-1'));
    final ad = aad(Direction.res, Endpoint.pull, 'acme', 'dev-1');
    expect(openEnvelope(k, sealEnvelope(k, 'kid', 'dev-1', ad, 'héllo'), ad), 'héllo');
  });
}
