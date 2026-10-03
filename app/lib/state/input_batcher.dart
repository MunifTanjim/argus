import 'dart:async';

/// Joins keystrokes into fewer sends: it sends when no input has come for
/// [quiet], or at once when [maxBytes] are waiting. Bytes keep their order.
class InputBatcher {
  InputBatcher(
    this.send, {
    this.quiet = const Duration(milliseconds: 16),
    this.maxBytes = 4096,
  });

  final void Function(List<int> bytes) send;
  final Duration quiet;
  final int maxBytes;

  final _buf = <int>[];
  Timer? _timer;

  void add(List<int> bytes) {
    if (bytes.isEmpty) return;
    _buf.addAll(bytes);
    if (_buf.length >= maxBytes) {
      flush();
      return;
    }
    _timer?.cancel();
    _timer = Timer(quiet, flush);
  }

  void flush() {
    _timer?.cancel();
    _timer = null;
    if (_buf.isEmpty) return;
    final out = List<int>.of(_buf);
    _buf.clear();
    send(out);
  }
}
