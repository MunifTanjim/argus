import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_riverpod/legacy.dart';

import '../models/project_sources.dart';
import '../models/session.dart';
import '../transport/gateway_client.dart';
import '../transport/jsonrpc.dart';
import 'gateway.dart';
import 'project_tree.dart';

/// Wraps the project and workspace management RPCs. Ids are composite; the
/// E2E client routes them to their node.
class ProjectsApi {
  ProjectsApi(this._clientOf);
  final GatewayClient? Function() _clientOf;

  GatewayClient get _client =>
      _clientOf() ?? (throw StateError('not connected'));

  Future<Object?> _call(String method, Map<String, dynamic> params) =>
      _client.call(method, params);

  Future<void> rename(String projectId, String name) =>
      _call('project.rename', {'project_id': projectId, 'name': name});

  Future<void> setPinned(String projectId, bool value) =>
      _call('project.setPinned', {'project_id': projectId, 'value': value});

  Future<void> setHidden(String projectId, bool value) =>
      _call('project.setHidden', {'project_id': projectId, 'value': value});

  Future<void> forget(String projectId) =>
      _call('project.forget', {'project_id': projectId});

  Future<List<BranchInfo>> branches(String projectId) async =>
      parseBranches(await _call('project.branches', {'project_id': projectId}));

  Future<SourceList<PrInfo>> prs(String projectId) async =>
      parsePrs(await _call('project.prs', {'project_id': projectId}));

  Future<SourceList<IssueInfo>> issues(String projectId) async =>
      parseIssues(await _call('project.issues', {'project_id': projectId}));

  Future<CreateResult> create(String projectId, CreateRequest req) async {
    final res = await _call('workspace.create', req.params(projectId));
    return CreateResult.fromJson(
      (res as Map?)?.cast<String, dynamic>() ?? const {},
    );
  }

  Future<String> remove(String workspaceId, {bool force = false}) async {
    final res = await _call('workspace.remove', {
      'workspace_id': workspaceId,
      if (force) 'force': true,
    });
    return res is Map ? res['warning'] as String? ?? '' : '';
  }

  Future<void> setTarget(String workspaceId, String branch) => _call(
    'workspace.setTarget',
    {'workspace_id': workspaceId, 'target_branch': branch},
  );

  Future<void> runSetup(String workspaceId) =>
      _call('workspace.runSetup', {'workspace_id': workspaceId});

  Future<String> setupLog(String workspaceId) async {
    final res = await _call('workspace.setupLog', {
      'workspace_id': workspaceId,
    });
    return res is Map ? res['output'] as String? ?? '' : '';
  }
}

final projectsApiProvider = Provider<ProjectsApi>(
  (ref) => ProjectsApi(() => ref.read(gatewayProvider)?.client),
);

final projectBranchesProvider = FutureProvider.autoDispose
    .family<List<BranchInfo>, String>((ref, projectId) {
      ref.watch(connStateProvider);
      return ref.read(projectsApiProvider).branches(projectId);
    });

final projectPrsProvider = FutureProvider.autoDispose
    .family<SourceList<PrInfo>, String>((ref, projectId) {
      ref.watch(connStateProvider);
      return ref.read(projectsApiProvider).prs(projectId);
    });

final projectIssuesProvider = FutureProvider.autoDispose
    .family<SourceList<IssueInfo>, String>((ref, projectId) {
      ref.watch(connStateProvider);
      return ref.read(projectsApiProvider).issues(projectId);
    });

String actionError(Object e) => switch (e) {
  RpcError(:final message) => message,
  StateError(:final message) => message,
  _ => '$e',
};

/// Workspaces with a remove call in flight, like the TUI removing set.
final removingProvider = StateProvider<Set<String>>((ref) => const {});

/// The TUI refusal for forget and remove while sessions still run.
String? liveGuard(
  String name,
  Iterable<Session> sessions,
  Iterable<String> workspaceIds,
) {
  final n = sumActivity(workspaceActivity(sessions), workspaceIds).live;
  if (n == 0) return null;
  final count = n == 1 ? '1 live session' : '$n live sessions';
  return '$name has $count · kill ${n == 1 ? 'it' : 'them'} first';
}
