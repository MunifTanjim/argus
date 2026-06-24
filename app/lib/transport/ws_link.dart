import 'dart:async';
import 'dart:convert';
import 'dart:io';

import '../pairing/pairing_uri.dart';
import 'connection.dart';
import 'jsonrpc.dart';

/// Appends the implicit /client route to a gateway base URL. The route is
/// determined by role (consumer client), never typed by the user, so the paired
/// URL is a base (scheme://host[:port], no path) — mirroring the TUI client's
/// hub-url resolver. A non-empty path is an error (clean break, no subpath mount).
String resolveClientUrl(String base) {
  final u = Uri.parse(base);
  if (u.scheme != 'ws' && u.scheme != 'wss') {
    throw FormatException('gateway url must be ws:// or wss://', base);
  }
  if (u.host.isEmpty) {
    throw FormatException('gateway url has no host', base);
  }
  if (u.path.isNotEmpty && u.path != '/') {
    throw FormatException(
        'gateway url takes no path (the /client route is implicit)', base);
  }
  return u.replace(path: '/client').toString();
}

class WebSocketRpcLink implements RpcLink {
  WebSocketRpcLink._(this._socket) {
    _socket.listen(
      _onData,
      onError: _controller.addError,
      onDone: close,
      cancelOnError: false,
    );
  }

  final WebSocket _socket;
  final _controller = StreamController<RpcMessage>.broadcast();
  final _buf = StringBuffer();

  static Future<WebSocketRpcLink> connect(
    GatewayCredentials c, {
    Duration timeout = const Duration(seconds: 10),
  }) async {
    // Bound the connect: a half-open socket (common on mobile network
    // transitions) would otherwise hang on the OS TCP timeout, parking the
    // app in "Reconnecting…". On timeout this throws and the caller retries.
    final socket = await WebSocket.connect(
      resolveClientUrl(c.url),
      headers: {'Authorization': 'Bearer ${c.token}'},
    ).timeout(timeout);
    return WebSocketRpcLink._(socket);
  }

  void _onData(dynamic data) {
    _buf.write(data is List<int> ? utf8.decode(data) : data as String);
    final parts = _buf.toString().split('\n');
    _buf.clear();
    _buf.write(parts.removeLast()); // trailing partial (or '')
    for (final line in parts) {
      if (line.trim().isEmpty) continue;
      _controller.add(RpcMessage.fromJson(
          jsonDecode(line) as Map<String, dynamic>));
    }
  }

  @override
  Stream<RpcMessage> get incoming => _controller.stream;
  @override
  void send(String frame) => _socket.add(frame);
  @override
  Future<void> close() async {
    await _socket.close();
    if (!_controller.isClosed) await _controller.close();
  }
}
