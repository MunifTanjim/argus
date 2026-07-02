import 'package:flutter/foundation.dart';

import '../pairing/pairing_uri.dart';
import 'connection.dart';
import 'jsonrpc.dart';
import 'ssh_gateway.dart';
import 'ssh_hostkey_store.dart';
import 'ssh_key_store.dart';
import 'ssh_tunnel.dart';
import 'ws_link.dart';

/// An [RpcLink] that stands up an [SshTunnel], then runs a [WebSocketRpcLink]
/// over the tunnel's local loopback port. Closing it tears down both, so
/// ConnectionManager's redial gets a fresh SSH connection each time.
class SshWebSocketRpcLink implements RpcLink {
  @visibleForTesting
  SshWebSocketRpcLink.raw(this._inner, this._closeTunnel);

  final RpcLink _inner;
  final Future<void> Function() _closeTunnel;

  static Future<SshWebSocketRpcLink> connect(
    GatewayCredentials creds,
    SshKey key,
    HostKeyStore hostKeys, {
    Duration timeout = const Duration(seconds: 15),
    bool pinHostKey = true,
  }) async {
    final cfg = parseSshGatewayUrl(creds.url);
    final tunnel = await SshTunnel.open(cfg, key, hostKeys,
        timeout: timeout, pinHostKey: pinHostKey);
    try {
      final inner = await WebSocketRpcLink.connect(
        GatewayCredentials(tunnel.localUrl, creds.token),
        timeout: timeout,
      );
      return SshWebSocketRpcLink.raw(inner, tunnel.close);
    } catch (_) {
      await tunnel.close();
      rethrow;
    }
  }

  @override
  Stream<RpcMessage> get incoming => _inner.incoming;
  @override
  void send(String frame) => _inner.send(frame);
  @override
  Future<void> close() async {
    await _inner.close();
    await _closeTunnel();
  }
}
