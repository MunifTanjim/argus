import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math' show Random;
import 'dart:typed_data';

import 'package:cbor/simple.dart' as cbor_codec;
import 'package:flutter/foundation.dart' show visibleForTesting;
import 'package:pointycastle/digests/sha1.dart';

import '../pairing/pairing_uri.dart';
import 'connection.dart';
import 'frame_reader.dart';
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

final _rng = Random.secure();

/// The Sec-WebSocket-Accept a server answers [key] with (RFC 6455).
@visibleForTesting
String webSocketAccept(String key) => base64.encode(
  SHA1Digest().process(
    Uint8List.fromList(
      utf8.encode('${key}258EAFA5-E914-47DA-95CA-C5AB0DC85B11'),
    ),
  ),
);

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
  final _reader = FrameReader();
  bool _failed = false;

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

  /// Connects to a node's direct endpoint: no credentials, no proxy, and no
  /// redirects, so the connection reaches exactly [url] or fails.
  static Future<WebSocketRpcLink> connectDirect(
    String url, {
    Duration timeout = const Duration(seconds: 10),
  }) async {
    final u = Uri.parse(url);
    if (u.scheme != 'ws' && u.scheme != 'wss') {
      throw FormatException('direct endpoint must be ws:// or wss://', url);
    }
    final http = HttpClient()
      ..findProxy = ((_) => 'DIRECT')
      ..connectionTimeout = timeout;
    try {
      final key = base64.encode(
        List<int>.generate(16, (_) => _rng.nextInt(256)),
      );
      final req = await http
          .openUrl(
            'GET',
            u.replace(scheme: u.scheme == 'wss' ? 'https' : 'http'),
          )
          .timeout(timeout);
      req.followRedirects = false;
      req.headers
        ..set(HttpHeaders.connectionHeader, 'Upgrade')
        ..set(HttpHeaders.upgradeHeader, 'websocket')
        ..set('Sec-WebSocket-Key', key)
        ..set('Sec-WebSocket-Version', '13');
      final res = await req.close().timeout(timeout);
      final accept = res.headers.value('Sec-WebSocket-Accept');
      if (res.statusCode != HttpStatus.switchingProtocols ||
          accept != webSocketAccept(key)) {
        (await res.detachSocket()).destroy();
        throw WebSocketException(
          'direct endpoint refused the upgrade',
          res.statusCode,
        );
      }
      final socket = await res.detachSocket();
      http.close();
      return WebSocketRpcLink._(
        WebSocket.fromUpgradedSocket(
          socket,
          serverSide: false,
          compression: CompressionOptions.compressionOff,
        ),
      );
    } catch (_) {
      // Ends any connection still being set up.
      http.close(force: true);
      rethrow;
    }
  }

  void _onData(dynamic data) {
    // A frame buffered before close() can still be delivered after the controller
    // is closed; dropping it here avoids "add after close" on teardown.
    if (_failed || _controller.isClosed) return;
    final List<WireFrame> frames;
    try {
      frames = _reader.add(
        data is List<int> ? data : utf8.encode(data as String),
      );
    } on FormatException catch (e) {
      _fail(e);
      return;
    }
    for (final f in frames) {
      if (_controller.isClosed) return;
      final RpcMessage m;
      try {
        m = switch (f) {
          JsonFrame() => RpcMessage.fromJson(
            jsonDecode(f.line) as Map<String, dynamic>,
          ),
          CborFrame() => RpcMessage.fromCbor(
            cbor_codec.cbor.decode(f.envelope) as Map,
          ),
        };
      } catch (e) {
        // Dropping a frame would desync the E2E nonces, so the link ends.
        _fail(e);
        return;
      }
      _controller.add(m);
    }
  }

  /// Reports the first decode failure and closes; later data is dropped.
  void _fail(Object e) {
    if (_failed) return;
    _failed = true;
    _controller.addError(e);
    close();
  }

  @override
  Stream<RpcMessage> get incoming => _controller.stream;
  @override
  void send(String frame) => _sendFrame(frame);
  @override
  void sendBytes(Uint8List frame) => _sendFrame(frame);
  @override
  Future<void> close() async {
    await _closeSocket();
    if (!_controller.isClosed) await _controller.close();
  }
}
