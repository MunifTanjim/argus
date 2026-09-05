import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart' show visibleForTesting;

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
      'gateway url takes no path (the /client route is implicit)',
      base,
    );
  }
  return u.replace(path: '/client').toString();
}

class WebSocketRpcLink implements RpcLink {
  WebSocketRpcLink._(WebSocket socket)
    : _closeSocket = (() => socket.close()),
      _sendFrame = socket.add {
    socket.listen(
      _onData,
      onError: _controller.addError,
      onDone: close,
      cancelOnError: false,
    );
  }

  /// Testing seam: exercises [_onData] and [close] against an injected stream
  /// without a real socket. [closeHook] is called by [close].
  @visibleForTesting
  WebSocketRpcLink.fromStream(
    Stream<dynamic> frames, {
    Future<void> Function()? closeHook,
  }) : _closeSocket = closeHook ?? (() async {}),
       _sendFrame = _noop {
    frames.listen(
      _onData,
      onError: _controller.addError,
      onDone: close,
      cancelOnError: false,
    );
  }

  static void _noop(dynamic _) {}

  final Future<void> Function() _closeSocket;
  final void Function(dynamic) _sendFrame;
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
    // A frame buffered before close() can still be delivered after the controller
    // is closed; dropping it here avoids "add after close" on teardown.
    if (_controller.isClosed) return;
    _buf.write(data is List<int> ? utf8.decode(data) : data as String);
    final parts = _buf.toString().split('\n');
    _buf.clear();
    _buf.write(parts.removeLast()); // trailing partial (or '')
    for (final line in parts) {
      if (line.trim().isEmpty) continue;
      if (_controller.isClosed) return;
      _controller.add(
        RpcMessage.fromJson(jsonDecode(line) as Map<String, dynamic>),
      );
    }
  }

  @override
  Stream<RpcMessage> get incoming => _controller.stream;
  @override
  void send(String frame) => _sendFrame(frame);
  @override
  Future<void> close() async {
    await _closeSocket();
    if (!_controller.isClosed) await _controller.close();
  }
}
