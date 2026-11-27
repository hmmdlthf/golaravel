/// Hybrid logical clock, encoded exactly like the server's:
/// "001732700000000:00000:node". The text form sorts in clock order.
class Hlc {
  const Hlc(this.wall, this.count, this.node);

  final int wall;
  final int count;
  final String node;

  static final _re = RegExp(r'^(\d{15}):(\d{5}):(.{1,64})$');

  static Hlc? parse(String s) {
    final m = _re.firstMatch(s);
    if (m == null) return null;
    return Hlc(int.parse(m.group(1)!), int.parse(m.group(2)!), m.group(3)!);
  }

  @override
  String toString() => '${wall.toString().padLeft(15, '0')}:${count.toString().padLeft(5, '0')}:$node';
}

class Clock {
  Clock(this.node, [String? last, int Function()? wallClock])
      : _wallClock = wallClock ?? (() => DateTime.now().millisecondsSinceEpoch),
        _last = (last == null ? null : Hlc.parse(last)) ?? Hlc(0, 0, node);

  static const _maxCount = 99999;

  final String node;
  final int Function() _wallClock;
  Hlc _last;

  /// A timestamp later than any issued or observed.
  String now() {
    final wall = _wallClock();
    if (wall > _last.wall) {
      _last = Hlc(wall, 0, node);
    } else if (_last.count >= _maxCount) {
      _last = Hlc(_last.wall + 1, 0, node);
    } else {
      _last = Hlc(_last.wall, _last.count + 1, node);
    }
    return _last.toString();
  }

  /// Merges a remote timestamp so later local ones sort after it.
  void observe(String? remote) {
    final r = remote == null ? null : Hlc.parse(remote);
    if (r == null) return;
    if (r.wall > _last.wall || (r.wall == _last.wall && r.count > _last.count)) {
      _last = Hlc(r.wall, r.count, node);
    }
  }

  /// The latest timestamp, to persist across restarts.
  String get current => _last.toString();
}
