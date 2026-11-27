import 'dart:math';
import 'dart:typed_data';

const _encoding = '0123456789ABCDEFGHJKMNPQRSTVWXYZ';
final _random = Random.secure();
int _lastTime = -1;
Uint8List _lastRandom = Uint8List(10);

/// Returns a ULID (48-bit milliseconds + 80 random bits, Crockford base32).
/// IDs from one isolate increase monotonically, so the outbox's key order
/// is the order mutations were made.
String ulid([int? now]) {
  var time = now ?? DateTime.now().millisecondsSinceEpoch;
  Uint8List random;
  if (time <= _lastTime) {
    time = _lastTime;
    random = Uint8List.fromList(_lastRandom);
    for (var i = random.length - 1; i >= 0; i--) {
      random[i] = (random[i] + 1) & 0xff;
      if (random[i] != 0) break;
    }
  } else {
    random = Uint8List.fromList(List.generate(10, (_) => _random.nextInt(256)));
    _lastTime = time;
  }
  _lastRandom = random;
  return _encodeTime(time) + _encodeRandom(random);
}

String _encodeTime(int ms) {
  final out = StringBuffer();
  final chars = List.filled(10, '');
  for (var i = 9; i >= 0; i--) {
    chars[i] = _encoding[ms % 32];
    ms ~/= 32;
  }
  out.writeAll(chars);
  return out.toString();
}

String _encodeRandom(Uint8List bytes) {
  final out = StringBuffer();
  var buffer = 0;
  var bits = 0;
  for (final b in bytes) {
    buffer = (buffer << 8) | b;
    bits += 8;
    while (bits >= 5) {
      bits -= 5;
      out.write(_encoding[(buffer >> bits) & 31]);
    }
    buffer &= (1 << bits) - 1;
  }
  return out.toString();
}
