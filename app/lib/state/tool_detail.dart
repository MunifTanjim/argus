import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/result.dart';
import '../models/chunk.dart';
import '../transport/rpc_client.dart';
import 'gateway.dart';

/// ToolDetailRef addresses the transcript a tool item belongs to, so the detail
/// view can fetch that item's heavy body on demand. A live session is addressed
/// by [sessionId]; a past session by [nodeId] + [transcriptPath]. [agentId]
/// selects a subagent trace within either source (empty = the main transcript).
class ToolDetailRef {
  final String? sessionId;
  final String? nodeId;
  final String? transcriptPath;
  final String? agentId;

  const ToolDetailRef({
    this.sessionId,
    this.nodeId,
    this.transcriptPath,
    this.agentId,
  });

  /// A live session transcript (optionally a subagent trace within it).
  const ToolDetailRef.live(this.sessionId, {this.agentId})
      : nodeId = null,
        transcriptPath = null;

  /// A past session's transcript on a specific node.
  const ToolDetailRef.history({
    required this.nodeId,
    required this.transcriptPath,
    this.agentId,
  }) : sessionId = null;

  bool get isHistory => (transcriptPath ?? '').isNotEmpty;

  /// A copy scoped to a subagent trace (its tool ids live in the subagent file).
  ToolDetailRef forAgent(String? agentId) => ToolDetailRef(
        sessionId: sessionId,
        nodeId: nodeId,
        transcriptPath: transcriptPath,
        agentId: agentId,
      );
}

/// Wraps the on-demand tool-body RPCs. Resolves the client fresh on each call so
/// reconnects are transparent; a missing client or failed RPC surfaces as
/// [Error] for the UI to render.
class ToolDetailApi {
  ToolDetailApi(this._clientOf);
  final RpcClient? Function() _clientOf;

  Future<Result<ToolDetail>> fetch(ToolDetailRef ref, String toolId) async {
    final c = _clientOf();
    if (c == null) return Result.error(StateError('not connected'));
    try {
      final agentId = ref.agentId;
      final Object result;
      if (ref.isHistory) {
        // node_id is mandatory for gateway-routed history reads.
        if ((ref.nodeId ?? '').isEmpty) {
          throw ArgumentError('history tool detail requires a node_id');
        }
        result = await c.call('sessions.historyToolDetail', {
          'node_id': ref.nodeId,
          'transcript_path': ref.transcriptPath,
          if ((agentId ?? '').isNotEmpty) 'agent_id': agentId,
          'tool_id': toolId,
        }) as Object;
      } else {
        result = await c.call('sessions.toolDetail', {
          'session_id': ref.sessionId,
          if ((agentId ?? '').isNotEmpty) 'agent_id': agentId,
          'tool_id': toolId,
        }) as Object;
      }
      return Result.ok(ToolDetail.fromJson(result as Map<String, dynamic>));
    } catch (e) {
      return Result.error(e);
    }
  }
}

final toolDetailApiProvider = Provider<ToolDetailApi>(
  (ref) => ToolDetailApi(() => ref.read(gatewayProvider)?.client),
);
