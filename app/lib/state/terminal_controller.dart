import 'dart:async';
import 'dart:convert';
import 'dart:developer' as developer;

import '../models/enums.dart';
import '../transport/jsonrpc.dart';
import '../transport/rpc_client.dart';
import '../util/id.dart';

/// An open terminal attach the UI drives and disposes.
abstract class TerminalSession {
  /// Sends raw PTY bytes (base64-encoded on the wire) to the attached terminal.
  void send(List<int> data);

  /// Resizes the remote PTY. No-op when unchanged or non-positive.
  void resize(int cols, int rows);

  /// Closes the attach on the node (best-effort) and stops listening.
  void dispose();
}

/// Owns one live terminal attach: mints a term id, subscribes to `terminal.*`
/// pushes filtered by that id, and issues open/input/resize/close.
class TerminalAttach implements TerminalSession {
  TerminalAttach({
    required this.client,
    required this.sessionId,
    required int cols,
    required int rows,
    required this.onData,
    this.onExited,
    this.onError,
  }) {
    _cols = cols;
    _rows = rows;
  }

  final RpcClient client;
  final String sessionId;

  /// Raw output bytes from the PTY; the view writes them to the emulator.
  final void Function(List<int> data) onData;

  /// The attach ended node-side (PTY exited, mirror died, or booted because the
  /// session was opened elsewhere — see [TerminalExitReason]).
  final void Function(TerminalExitReason reason)? onExited;

  /// terminal.open failed.
  final void Function(Object error)? onError;

  // Unique per attach so a fresh open doesn't collide with the outgoing one in
  // the gateway term table or node mirror (the node boots the prior viewer).
  final String termId = newHexId();
  int _cols = 0;
  int _rows = 0;
  StreamSubscription<RpcMessage>? _sub;
  bool _disposed = false;

  void start() {
    _sub = client.notifications.listen(_onNotify);
    client.call('terminal.open', {
      'term_id': termId,
      'session_id': sessionId,
      'cols': _cols,
      'rows': _rows,
    }).catchError((Object e) {
      if (_disposed) return null;
      onError?.call(e);
      // The open never succeeded: stop listening so a late/orphaned output can't
      // be processed and the subscription doesn't linger past the failure.
      dispose();
      return null;
    });
  }

  @override
  void send(List<int> data) {
    if (data.isEmpty || _disposed) return;
    client.call('terminal.input', {
      'term_id': termId,
      'data': base64Encode(data),
    }).catchError(_logCallError('terminal.input'));
  }

  @override
  void resize(int cols, int rows) {
    if (_disposed || cols <= 0 || rows <= 0) return;
    if (cols == _cols && rows == _rows) return;
    _cols = cols;
    _rows = rows;
    client.call('terminal.resize', {
      'term_id': termId,
      'cols': cols,
      'rows': rows,
    }).catchError(_logCallError('terminal.resize'));
  }

  void _onNotify(RpcMessage m) {
    final p = m.params;
    if (p is! Map || p['term_id'] != termId) return;
    switch (m.method) {
      case 'terminal.output':
        final data = p['data'];
        if (data is String) onData(base64Decode(data));
      case 'terminal.exited':
        // The node already tore the term down, so stop listening now — don't rely
        // on the UI's dispose() (e.g. a pop that can't happen on a root route) to
        // release the subscription. No terminal.close: the term is already gone.
        _stopListening();
        onExited?.call(terminalExitReasonFromWire(p['reason'] as String?));
    }
  }

  // Best-effort call failures (input/resize/close) are non-fatal, but log them so
  // a "typing into a void" symptom is diagnosable rather than silent.
  Object? Function(Object) _logCallError(String method) => (Object e) {
        developer.log('$method failed', name: 'terminal', error: e);
        return null;
      };

  // Marks the attach disposed and cancels the subscription, without issuing
  // terminal.close. Safe to call repeatedly.
  void _stopListening() {
    if (_disposed) return;
    _disposed = true;
    _sub?.cancel();
  }

  @override
  void dispose() {
    if (_disposed) return; // already stopped (e.g. on exit); nothing to close
    _stopListening();
    client
        .call('terminal.close', {'term_id': termId}).catchError(_logCallError('terminal.close'));
  }
}
