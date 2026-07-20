import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/tasks.dart';
import '../transport/rpc_client.dart';
import 'gateway.dart';

/// Wraps the sessions.tasks RPC. Resolves the client fresh on each call so
/// reconnects are transparent; a missing client throws, surfacing to the
/// FutureProvider's error state.
class TasksApi {
  TasksApi(this._clientOf);
  final RpcClient? Function() _clientOf;

  RpcClient get _client => _clientOf() ?? (throw StateError('not connected'));

  Future<List<Task>> tasks(String sessionId) async {
    final res = await _client.call('sessions.tasks', {
      'session_id': sessionId,
    }) as Map<String, dynamic>;
    return ((res['tasks'] as List?) ?? const [])
        .map((e) => Task.fromJson(e as Map<String, dynamic>))
        .toList();
  }
}

final tasksApiProvider = Provider<TasksApi>(
  (ref) => TasksApi(() => ref.read(gatewayProvider)?.client),
);

/// A session's current task list. Auto-fetches on first watch (open) and
/// refetches on (re)connect; invalidate it to pull again (manual refresh or a
/// tasks.changed push).
final tasksProvider = FutureProvider.autoDispose.family<List<Task>, String>(
  (ref, sessionId) {
    ref.watch(connStateProvider); // refetch on (re)connect
    return ref.read(tasksApiProvider).tasks(sessionId);
  },
);
