import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/terminal.dart';
import '../transport/gateway_client.dart';
import 'gateway.dart';

class TerminalsApi {
  TerminalsApi(this._clientOf);
  final GatewayClient? Function() _clientOf;

  GatewayClient get _client =>
      _clientOf() ?? (throw StateError('not connected'));

  /// A [workspaceId] is node-local; the terminal opens in that workspace.
  Future<NodeTerminal> create(String? nodeId, {String? workspaceId}) async {
    final r = await _client.call('terminal.create', {
      'node_id': ?nodeId,
      'workspace_id': ?workspaceId,
    });
    return NodeTerminal.fromJson((r as Map).cast<String, dynamic>());
  }

  Future<void> rename(String id, String name) =>
      _client.call('terminal.rename', {'terminal_id': id, 'name': name});

  Future<void> kill(String id) =>
      _client.call('terminal.kill', {'terminal_id': id});
}

final terminalsApiProvider = Provider<TerminalsApi>(
  (ref) => TerminalsApi(() => ref.read(gatewayProvider)?.client),
);
