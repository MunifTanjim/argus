import 'dart:convert';

/// Drops the mouse button reports of a touch drag. With mouse tracking on, a
/// touch scroll reports wheel ticks and also a left-button press, drag, and
/// release; inside tmux copy mode that drag ends with "copy and leave". A tap
/// still passes. Other bytes before a press resolves send it early, and then
/// its motion and release pass too, so a program never sees a press without
/// its release.
class TouchDragFilter {
  static final _sgr = RegExp('\x1b\\[<(\\d+);\\d+;\\d+([Mm])');

  String? _pending;
  bool _dragging = false;
  bool _sent = false; // the held press went out early

  List<int> filter(List<int> bytes) {
    final s = latin1.decode(bytes, allowInvalid: true);
    final out = StringBuffer();
    var at = 0;
    for (final m in _sgr.allMatches(s)) {
      if (m.start > at) {
        _flush(out);
        out.write(s.substring(at, m.start));
      }
      _report(out, m.group(0)!, int.parse(m.group(1)!), m.group(2) == 'm');
      at = m.end;
    }
    if (at < s.length) {
      _flush(out);
      out.write(s.substring(at));
    }
    return latin1.encode(out.toString());
  }

  void _report(StringBuffer out, String seq, int code, bool release) {
    final wheel = code & 64 != 0;
    final motion = code & 32 != 0;
    if (wheel || _sent) {
      out.write(seq);
      if (release) _sent = false;
    } else if (release) {
      if (_pending != null && !_dragging) out..write(_pending)..write(seq);
      _pending = null;
      _dragging = false;
    } else if (motion) {
      if (_pending != null || _dragging) {
        _pending = null;
        _dragging = true;
      } else {
        out.write(seq);
      }
    } else {
      _flush(out);
      _pending = seq;
      _dragging = false;
    }
  }

  void _flush(StringBuffer out) {
    if (_pending == null) return;
    out.write(_pending);
    _pending = null;
    _sent = true;
  }

  /// [filter], keeping only mouse reports: for a screen where the keyboard
  /// does not type into the terminal.
  List<int> mouseOnly(List<int> bytes) {
    final s = latin1.decode(filter(bytes), allowInvalid: true);
    return latin1.encode(_sgr.allMatches(s).map((m) => m.group(0)).join());
  }
}
