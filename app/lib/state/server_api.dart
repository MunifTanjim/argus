import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../transport/gateway_client.dart';
import '../transport/jsonrpc.dart';
import 'gateway.dart';

class UpgradeCheck {
  const UpgradeCheck({required this.current, required this.latest});
  final String current;
  final String latest;

  bool get available => latest.isNotEmpty && latest != current;
}

/// The binary was swapped to [to], but the restart that runs it failed.
class RestartFailed implements Exception {
  const RestartFailed(this.to, this.error);
  final String to;
  final Object error;

  @override
  String toString() => 'upgraded to $to, restart failed: $error';
}

/// The server.* upgrade calls. A null [nodeId] targets the gateway.
class ServerApi {
  ServerApi(this._clientOf);

  final GatewayClient? Function() _clientOf;

  GatewayClient get _client =>
      _clientOf() ?? (throw StateError('not connected'));

  Map<String, dynamic> _target(String? nodeId) =>
      {'node_id': ?nodeId};

  /// Null when the target predates in-place upgrade.
  Future<UpgradeCheck?> checkUpgrade(String? nodeId) async {
    try {
      final r = ((await _client.call('server.checkUpgrade', _target(nodeId))) as Map)
          .cast<String, dynamic>();
      return UpgradeCheck(
        current: r['current'] as String? ?? '',
        latest: r['latest'] as String? ?? '',
      );
    } on RpcError catch (e) {
      if (e.isMethodNotFound) return null;
      rethrow;
    }
  }

  /// Returns (from, to); throws [RestartFailed] when only the restart failed.
  /// The restart closes the connection, which can lose the reply, so only an
  /// error reply counts as a failed restart.
  Future<(String, String)> upgrade(String? nodeId) async {
    final r = ((await _client.call('server.upgrade', _target(nodeId))) as Map)
        .cast<String, dynamic>();
    final from = r['from'] as String? ?? '';
    final to = r['to'] as String? ?? '';
    if (to != from) {
      try {
        await _client.call('server.restart', _target(nodeId));
      } on RpcError catch (e) {
        throw RestartFailed(to, e);
      } catch (_) {}
    }
    return (from, to);
  }
}

final serverApiProvider = Provider<ServerApi>(
  (ref) => ServerApi(() => ref.read(gatewayProvider)?.client),
);
