import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/result.dart';
import '../models/entry.dart';
import '../transport/gateway_client.dart';
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
  final String? agent;

  const ToolDetailRef({
    this.sessionId,
    this.nodeId,
    this.transcriptPath,
    this.agentId,
    this.agent,
  });

  /// A live session transcript (optionally a subagent trace within it).
  const ToolDetailRef.live(this.sessionId, {this.agentId, this.agent})
      : nodeId = null,
        transcriptPath = null;

  /// A past session's transcript on a specific node.
  const ToolDetailRef.history({
    required this.nodeId,
    required this.transcriptPath,
    this.agentId,
    this.agent,
  }) : sessionId = null;

  bool get isHistory => (transcriptPath ?? '').isNotEmpty;

  /// A copy scoped to a subagent trace (its tool ids live in the subagent file).
  ToolDetailRef forAgent(String? agentId) => ToolDetailRef(
        sessionId: sessionId,
        nodeId: nodeId,
        transcriptPath: transcriptPath,
        agentId: agentId,
        agent: agent,
      );
}

/// Calls [live] for a live session or [history] for a past one, as [ref]
/// addresses it, adding [params] to the session/transcript keys.
Future<Object> callForRef(
  GatewayClient c,
  ToolDetailRef ref, {
  required String live,
  required String history,
  required Map<String, Object?> params,
}) async {
  final agentId = ref.agentId;
  if (ref.isHistory) {
    // node_id is mandatory for gateway-routed history reads.
    if ((ref.nodeId ?? '').isEmpty) {
      throw ArgumentError('$history requires a node_id');
    }
    return await c.call(history, {
          'node_id': ref.nodeId,
          'transcript_path': ref.transcriptPath,
          if ((agentId ?? '').isNotEmpty) 'agent_id': agentId,
          if ((ref.agent ?? '').isNotEmpty) 'agent': ref.agent,
          ...params,
        })
        as Object;
  }
  return await c.call(live, {
        'session_id': ref.sessionId,
        if ((agentId ?? '').isNotEmpty) 'agent_id': agentId,
        ...params,
      })
      as Object;
}

/// Wraps the on-demand tool-body RPCs. Resolves the client fresh on each call so
/// reconnects are transparent; a missing client or failed RPC surfaces as
/// [Error] for the UI to render.
class ToolDetailApi {
  ToolDetailApi(this._clientOf);
  final GatewayClient? Function() _clientOf;

  Future<Result<ToolDetail>> fetch(ToolDetailRef ref, String toolId) async {
    final c = _clientOf();
    if (c == null) return Result.error(StateError('not connected'));
    try {
      final result = await callForRef(
        c,
        ref,
        live: 'sessions.toolDetail',
        history: 'sessions.historyToolDetail',
        params: {'tool_id': toolId},
      );
      return Result.ok(ToolDetail.fromJson(result as Map<String, dynamic>));
    } catch (e) {
      return Result.error(e);
    }
  }
}

final toolDetailApiProvider = Provider<ToolDetailApi>(
  (ref) => ToolDetailApi(() => ref.read(gatewayProvider)?.client),
);
