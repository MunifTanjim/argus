import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/result.dart';
import '../models/workflow.dart';
import '../state/gateway.dart';
import '../state/tool_detail.dart';
import '../transport/gateway_client.dart';

/// Reads a workflow run's phases and agents, from a live session or from a
/// past session's transcript, as [ref] addresses it.
abstract class WorkflowRepository {
  Future<Result<WorkflowView>> fetch(ToolDetailRef ref, String runId);
}

class WorkflowRepositoryRemote implements WorkflowRepository {
  WorkflowRepositoryRemote(this._clientOf);
  final GatewayClient? Function() _clientOf;

  @override
  Future<Result<WorkflowView>> fetch(ToolDetailRef ref, String runId) async {
    final c = _clientOf();
    if (c == null) return Result.error(StateError('not connected'));
    try {
      final result = await callForRef(
        c,
        ref,
        live: 'sessions.workflow',
        history: 'sessions.historyWorkflow',
        params: {'run_id': runId},
      );
      return Result.ok(WorkflowView.fromJson(result as Map<String, dynamic>));
    } catch (e) {
      return Result.error(e);
    }
  }
}

final workflowRepositoryProvider = Provider<WorkflowRepository>(
  (ref) => WorkflowRepositoryRemote(() => ref.read(gatewayProvider)?.client),
);
