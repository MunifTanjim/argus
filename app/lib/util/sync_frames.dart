import 'dart:async';

const _begin = [0x1b, 0x5b, 0x3f, 0x32, 0x30, 0x32, 0x36, 0x68]; // ESC[?2026h
const _end = [0x1b, 0x5b, 0x3f, 0x32, 0x30, 0x32, 0x36, 0x6c]; // ESC[?2026l

/// Holds a synchronized update (mode 2026) until it ends, so the emulator gets
/// a redraw whole even when it arrives split across `terminal.output` chunks.
/// tmux redraws the whole pane for each synchronized update, and an emulator
/// that paints between the chunks shows the half-drawn screen.
///
/// An update that never ends is released after [timeout].
class SyncFrames {
  SyncFrames(this._write, {this.timeout = const Duration(seconds: 1)});

  final void Function(List<int> bytes) _write;
  final Duration timeout;

  List<int> _pending = [];
  bool _holding = false;
  Timer? _timer;

  void add(List<int> bytes) {
    _pending = [..._pending, ...bytes];
    while (_pending.isNotEmpty) {
      if (_holding) {
        final i = _indexOf(_pending, _end);
        if (i < 0) return;
        _release(i + _end.length);
        _holding = false;
        _timer?.cancel();
        continue;
      }
      final i = _indexOf(_pending, _begin);
      if (i >= 0) {
        _release(i + _begin.length);
        _holding = true;
        _timer?.cancel();
        _timer = Timer(timeout, flush);
        continue;
      }
      // A chunk can end inside ESC[?2026h: keep that tail for the next chunk.
      final tail = _prefixTail(_pending, _begin);
      _release(_pending.length - tail);
      if (tail > 0) {
        _timer?.cancel();
        _timer = Timer(timeout, flush);
      }
      return;
    }
  }

  /// Writes everything held.
  void flush() {
    _timer?.cancel();
    _holding = false;
    _release(_pending.length);
  }

  /// Drops everything held, for a new attach.
  void reset() {
    _timer?.cancel();
    _holding = false;
    _pending = [];
  }

  void dispose() => _timer?.cancel();

  void _release(int n) {
    if (n == 0) return;
    final out = _pending.sublist(0, n);
    _pending = _pending.sublist(n);
    _write(out);
  }
}

int _indexOf(List<int> b, List<int> seq) {
  for (var i = 0; i + seq.length <= b.length; i++) {
    var j = 0;
    while (j < seq.length && b[i + j] == seq[j]) {
      j++;
    }
    if (j == seq.length) return i;
  }
  return -1;
}

// The length of the longest tail of b that starts seq.
int _prefixTail(List<int> b, List<int> seq) {
  for (var n = seq.length - 1; n > 0; n--) {
    if (n > b.length) continue;
    var j = 0;
    while (j < n && b[b.length - n + j] == seq[j]) {
      j++;
    }
    if (j == n) return n;
  }
  return 0;
}
