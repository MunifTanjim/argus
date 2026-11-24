import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/result.dart';
import '../models/workflow.dart';
import '../state/gateway.dart';
import '../transport/gateway_client.dart';

/// Reads a workflow run's phases and agents, from a live session or from a
/// past session's transcript.
abstract class WorkflowRepository {
  Future<Result<WorkflowView>> live(
    String sessionId,
    String runId, {
    String? agentId,
  });

  Future<Result<WorkflowView>> history({
    String? nodeId,
    String? agent,
    required String transcriptPath,
    required String runId,
    String? agentId,
  });
}

class WorkflowRepositoryRemote implements WorkflowRepository {
  WorkflowRepositoryRemote(this._clientOf);
  final GatewayClient? Function() _clientOf;

  Future<Result<WorkflowView>> _call(
    String method,
    Map<String, dynamic> params,
  ) async {
    final c = _clientOf();
    if (c == null) return Result.error(StateError('not connected'));
    try {
      final result = await c.call(method, params);
      return Result.ok(WorkflowView.fromJson(result as Map<String, dynamic>));
    } catch (e) {
      return Result.error(e);
    }
  }

  @override
  Future<Result<WorkflowView>> live(
    String sessionId,
    String runId, {
    String? agentId,
  }) => _call('sessions.workflow', {
    'session_id': sessionId,
    if ((agentId ?? '').isNotEmpty) 'agent_id': agentId,
    'run_id': runId,
  });

  @override
  Future<Result<WorkflowView>> history({
    String? nodeId,
    String? agent,
    required String transcriptPath,
    required String runId,
    String? agentId,
  }) {
    // node_id is mandatory for gateway-routed history reads.
    if (nodeId == null || nodeId.isEmpty) {
      return Future.value(
        Result.error(ArgumentError('history workflow requires a node_id')),
      );
    }
    return _call('sessions.historyWorkflow', {
      'node_id': nodeId,
      if ((agent ?? '').isNotEmpty) 'agent': agent,
      'transcript_path': transcriptPath,
      if ((agentId ?? '').isNotEmpty) 'agent_id': agentId,
      'run_id': runId,
    });
  }
}

final workflowRepositoryProvider = Provider<WorkflowRepository>(
  (ref) => WorkflowRepositoryRemote(() => ref.read(gatewayProvider)?.client),
);
