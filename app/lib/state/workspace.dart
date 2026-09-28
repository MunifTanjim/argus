import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/changes.dart';
import '../models/workspace_files.dart';
import '../transport/gateway_client.dart';
import 'gateway.dart';

/// Workspace ids are composite; the E2E client routes them to their node.
class WorkspaceApi {
  WorkspaceApi(this._clientOf);
  final GatewayClient? Function() _clientOf;

  GatewayClient get _client =>
      _clientOf() ?? (throw StateError('not connected'));

  Future<Map<String, dynamic>> _call(
    String method,
    Map<String, dynamic> params,
  ) async =>
      (await _client.call(method, params) as Map?)?.cast<String, dynamic>() ??
      const {};

  static List<ChangedFile> _filesOf(Map<String, dynamic> res) =>
      (res['files'] as List? ?? const [])
          .map((e) => ChangedFile.fromJson(e as Map<String, dynamic>))
          .toList();

  Future<List<ChangedFile>> changedFiles(
    String workspaceId, {
    String against = '',
  }) async {
    return _filesOf(
      await _call('workspace.changedFiles', {
        'workspace_id': workspaceId,
        if (against.isNotEmpty) 'against': against,
      }),
    );
  }

  Future<WorkspaceDiff> diff(
    String workspaceId,
    String path, {
    String against = '',
    String? origPath,
    String? rev,
  }) async => WorkspaceDiff.fromJson(
    await _call('workspace.diff', {
      'workspace_id': workspaceId,
      'path': path,
      if (against.isNotEmpty) 'against': against,
      if ((origPath ?? '').isNotEmpty) 'orig_path': origPath,
      if ((rev ?? '').isNotEmpty) 'rev': rev,
    }),
  );

  Future<CommitList> commits(String workspaceId) async => CommitList.fromJson(
    await _call('workspace.commits', {'workspace_id': workspaceId}),
  );

  Future<List<ChangedFile>> commitFiles(String workspaceId, String sha) async =>
      _filesOf(
        await _call('workspace.commitFiles', {
          'workspace_id': workspaceId,
          'sha': sha,
        }),
      );

  Future<DirListing> listDir(String workspaceId, String path) async =>
      DirListing.fromJson(
        await _call('workspace.listDir', {
          'workspace_id': workspaceId,
          'path': path,
        }),
      );

  Future<FileContent> readFile(String workspaceId, String path) async =>
      FileContent.fromJson(
        await _call('workspace.readFile', {
          'workspace_id': workspaceId,
          'path': path,
        }),
      );
}

final workspaceApiProvider = Provider<WorkspaceApi>(
  (ref) => WorkspaceApi(() => ref.read(gatewayProvider)?.client),
);

final workspaceChangedFilesProvider = FutureProvider.autoDispose
    .family<List<ChangedFile>, (String, String)>((ref, key) {
      ref.watch(connStateProvider); // refetch on (re)connect
      return ref
          .read(workspaceApiProvider)
          .changedFiles(key.$1, against: key.$2);
    });

final workspaceCommitsProvider = FutureProvider.autoDispose
    .family<CommitList, String>((ref, workspaceId) {
      ref.watch(connStateProvider);
      return ref.read(workspaceApiProvider).commits(workspaceId);
    });

final workspaceCommitFilesProvider = FutureProvider.autoDispose
    .family<List<ChangedFile>, (String, String)>((ref, key) {
      ref.watch(connStateProvider);
      return ref.read(workspaceApiProvider).commitFiles(key.$1, key.$2);
    });

final workspaceDirProvider = FutureProvider.autoDispose
    .family<DirListing, (String, String)>((ref, key) {
      ref.watch(connStateProvider);
      return ref.read(workspaceApiProvider).listDir(key.$1, key.$2);
    });

final workspaceFileProvider = FutureProvider.autoDispose
    .family<FileContent, (String, String)>((ref, key) {
      ref.watch(connStateProvider);
      return ref.read(workspaceApiProvider).readFile(key.$1, key.$2);
    });
