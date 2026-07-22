import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/result.dart';
import '../models/chunk.dart';
import '../models/history.dart';
import '../transport/gateway_client.dart';
import 'gateway.dart';

/// Wraps the history RPCs. Resolves the client fresh on each call so that
/// reconnects are transparent — callers never hold a closed client.
///
/// Every method returns a [Result]: a missing client or a failed RPC surfaces
/// as [Error] so the UI can render the failure instead of crashing.
class HistoryApi {
  HistoryApi(this._clientOf);
  final GatewayClient? Function() _clientOf;

  /// Runs [body] against the current client, mapping a null client or any
  /// thrown error to [Error].
  Future<Result<T>> _guard<T>(Future<T> Function(GatewayClient c) body) async {
    final c = _clientOf();
    if (c == null) return Result.error(StateError('not connected'));
    try {
      return Result.ok(await body(c));
    } catch (e) {
      return Result.error(e);
    }
  }

  Future<Result<List<HistoryProject>>> projects() => _guard((c) async {
        final result = await c.call('sessions.historyProjects');
        return (result as List)
            .map((e) => HistoryProject.fromJson(e as Map<String, dynamic>))
            .toList();
      });

  Future<Result<HistorySessionPage>> sessions({
    String? nodeId,
    required String projectDir,
    required int limit,
    required int offset,
  }) =>
      _guard((c) async {
        // The gateway requires node_id to route history reads; fail loudly here
        // rather than omit it and get back a cryptic "requires node_id".
        if (nodeId == null || nodeId.isEmpty) {
          throw ArgumentError('history sessions require a node_id');
        }
        final params = <String, dynamic>{
          'node_id': nodeId,
          'project_dir': projectDir,
          'limit': limit,
          'offset': offset,
        };
        final result = await c.call('sessions.historySessions', params);
        return HistorySessionPage.fromJson(result as Map<String, dynamic>);
      });

  Future<Result<List<Chunk>>> transcript({
    String? nodeId,
    required String transcriptPath,
    String? agentId,
    String? agent,
  }) =>
      _guard((c) async {
        // See sessions(): node_id is mandatory for gateway-routed history reads.
        if (nodeId == null || nodeId.isEmpty) {
          throw ArgumentError('history transcript requires a node_id');
        }
        final params = <String, dynamic>{
          'node_id': nodeId,
          'transcript_path': transcriptPath,
          if ((agentId ?? '').isNotEmpty) 'agent_id': agentId,
          if ((agent ?? '').isNotEmpty) 'agent': agent,
        };
        final result = await c.call('sessions.historyTranscript', params);
        final map = result as Map<String, dynamic>;
        return (map['chunks'] as List)
            .map((e) => Chunk.fromJson(e as Map<String, dynamic>))
            .toList();
      });
}

final historyApiProvider = Provider<HistoryApi>(
  (ref) => HistoryApi(() => ref.read(gatewayProvider)?.client),
);
