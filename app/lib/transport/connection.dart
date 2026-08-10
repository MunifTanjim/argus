import 'dart:async';

import 'gateway_client.dart';
import 'jsonrpc.dart';
import 'rpc_client.dart';

abstract class RpcLink {
  /// Must be a broadcast stream; ConnectionManager attaches two listeners
  /// (data bridge + link-loss watcher).
  Stream<RpcMessage> get incoming;
  void send(String frame);
  Future<void> close();
}

enum ConnState { disconnected, connecting, connected, reconnecting, failed }

/// A connect() failure that retrying cannot fix (e.g. a changed host key —
/// possible MITM). The manager stops redialing and surfaces [message] instead
/// of looping silently in backoff, so the user can act on it.
abstract class FatalConnectError implements Exception {
  String get message;
}

class ConnectionManager {
  ConnectionManager({
    required this.connect,
    this.clientFactory,
    this.onConnected,
    this.baseBackoff = const Duration(seconds: 1),
    this.maxBackoff = const Duration(seconds: 30),
    this.dialTimeout = const Duration(seconds: 15),
    this.keepaliveInterval = const Duration(seconds: 20),
    this.keepaliveTimeout = const Duration(seconds: 10),
  });

  final Future<RpcLink> Function() connect;
  final FutureOr<GatewayClient> Function(Stream<RpcMessage>, void Function(String))? clientFactory;
  final Future<void> Function(GatewayClient)? onConnected;
  final Duration baseBackoff;
  final Duration maxBackoff;

  /// Caps how long a single dial (connect + onConnected handshake) may take.
  /// Without it, a half-open socket or a stalled handshake parks the state
  /// machine in [ConnState.reconnecting] until the OS TCP timeout fires.
  final Duration dialTimeout;

  /// How often to ping the gateway once connected, and how long to wait for
  /// the pong before treating the link as dead. Detects silently-dropped
  /// connections (e.g. mobile network switches) far faster than TCP alone.
  final Duration keepaliveInterval;
  final Duration keepaliveTimeout;

  final _states = StreamController<ConnState>.broadcast();
  ConnState _state = ConnState.disconnected;
  // Set alongside ConnState.failed; the message for the user (e.g. the MITM
  // warning). Cleared whenever a new dial starts.
  String? _failureMessage;
  GatewayClient? _client;
  RpcLink? _link;
  StreamController<RpcMessage>? _bridge;
  StreamSubscription<RpcMessage>? _linkSub;
  Timer? _retryTimer;
  Timer? _keepaliveTimer;
  int _attempt = 0;
  bool _running = false;
  // Bumped on every teardown/redial so a stale in-flight dial (e.g. a hung
  // connect that finally resolves) can detect it has been superseded and bail
  // out instead of clobbering the live link.
  int _gen = 0;

  Stream<ConnState> get states => _states.stream;
  ConnState get state => _state;
  String? get failureMessage => _failureMessage;
  GatewayClient? get client => _client;

  void start() {
    if (_running) return;
    _running = true;
    _attempt = 0;
    _dial();
  }

  Future<void> stop() async {
    _running = false;
    _gen++;
    _retryTimer?.cancel();
    await _teardownLink();
    _setState(ConnState.disconnected);
  }

  /// Abandon the current link (or a hung dial) and reconnect immediately.
  /// Used to recover instantly on app resume / network change instead of
  /// waiting out a stale backoff timer or a stuck connect.
  void reconnectNow() {
    if (!_running) return;
    _retryTimer?.cancel();
    _teardownLink();
    _attempt = 0;
    _dial();
  }

  void _setState(ConnState s) {
    _state = s;
    if (!_states.isClosed) _states.add(s);
  }

  Future<void> _dial() async {
    if (!_running) return;
    final gen = ++_gen;
    _failureMessage = null;
    _setState(_attempt == 0 ? ConnState.connecting : ConnState.reconnecting);
    try {
      final link = await connect().timeout(dialTimeout);
      if (!_running || gen != _gen) {
        await link.close();
        return;
      }
      _link = link;
      // Wrap the incoming stream so RpcClient sees a clean stream while
      // ConnectionManager's own subscription handles errors for reconnect.
      final bridge = StreamController<RpcMessage>();
      _bridge = bridge;
      _linkSub = link.incoming.listen(
        bridge.add,
        onError: (_) {
          final sub = _linkSub;
          _linkSub = null;
          sub?.cancel();
          bridge.close();
          _onLinkLost();
        },
        onDone: () {
          if (_linkSub == null) return; // already handled by onError
          final sub = _linkSub;
          _linkSub = null;
          sub?.cancel();
          bridge.close();
          _onLinkLost();
        },
      );
      final factory = clientFactory ??
          (Stream<RpcMessage> incoming, void Function(String) send) =>
              RpcClient(incoming: incoming, sendFrame: send);
      final client = await Future.sync(() => factory(bridge.stream, link.send)).timeout(dialTimeout);
      _client = client;
      if (onConnected != null) await onConnected!(client).timeout(dialTimeout);
      if (!_running || gen != _gen) return;
      _attempt = 0;
      _setState(ConnState.connected);
      _startKeepalive(gen);
    } on FatalConnectError catch (e) {
      if (gen != _gen) return; // superseded by a newer dial
      // Retrying can't fix this (e.g. changed host key). Stop and surface it
      // instead of an indefinite silent reconnect loop.
      await _teardownLink();
      _failureMessage = e.message;
      _setState(ConnState.failed);
    } catch (_) {
      if (gen != _gen) return; // superseded by a newer dial
      _onLinkLost();
    }
  }

  void _startKeepalive(int gen) {
    _keepaliveTimer?.cancel();
    _keepaliveTimer = Timer.periodic(keepaliveInterval, (_) async {
      final client = _client;
      if (!_running || gen != _gen || client == null) return;
      try {
        await client.call('ping').timeout(keepaliveTimeout);
      } catch (_) {
        if (_running && gen == _gen) _onLinkLost();
      }
    });
  }

  void _onLinkLost() {
    if (!_running) return;
    _teardownLink();
    _attempt++;
    final delay = _backoff(_attempt);
    _setState(ConnState.reconnecting);
    _retryTimer?.cancel();
    _retryTimer = Timer(delay, _dial);
  }

  Duration _backoff(int attempt) {
    final ms = baseBackoff.inMilliseconds * (1 << (attempt - 1).clamp(0, 16));
    return Duration(
        milliseconds: ms.clamp(baseBackoff.inMilliseconds, maxBackoff.inMilliseconds));
  }

  Future<void> _teardownLink() async {
    _gen++;
    _keepaliveTimer?.cancel();
    _keepaliveTimer = null;
    // Snapshot before any await: a redial may reassign these fields while we
    // are suspended, and we must tear down the link we owned, not the new one.
    final sub = _linkSub;
    final bridge = _bridge;
    final client = _client;
    final link = _link;
    _linkSub = null;
    _bridge = null;
    _client = null;
    _link = null;
    await sub?.cancel();
    await bridge?.close();
    await client?.close();
    await link?.close();
  }
}
