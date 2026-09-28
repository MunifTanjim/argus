import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/changes.dart';
import '../models/workspace_files.dart';
import '../transport/gateway_client.dart';
import 'gateway.dart';
import 'workspace.dart';

/// Wraps the changed-files RPCs. Resolves the client fresh on each call so
/// reconnects are transparent; a missing client throws (as does a failed RPC),
/// surfacing to the caller — a [FutureProvider]'s error state or a screen's
/// try/catch.
class ChangesApi {
  ChangesApi(this._clientOf);
  final GatewayClient? Function() _clientOf;

  GatewayClient get _client => _clientOf() ?? (throw StateError('not connected'));

  static List<ChangedFile> _filesOf(Map<String, dynamic> res) =>
      (res['files'] as List? ?? const [])
          .map((e) => ChangedFile.fromJson(e as Map<String, dynamic>))
          .toList();

  Future<List<ChangedFile>> changedFiles(String sessionId) async =>
      _filesOf(await _client.call('sessions.changedFiles', {
        'session_id': sessionId,
      }) as Map<String, dynamic>);

  Future<FileDiff> fileDiff(
    String sessionId,
    String path, {
    String? origPath,
    String? rev,
  }) async =>
      FileDiff.fromJson(await _client.call('sessions.fileDiff', {
        'session_id': sessionId,
        'path': path,
        if ((origPath ?? '').isNotEmpty) 'orig_path': origPath,
        if ((rev ?? '').isNotEmpty) 'rev': rev,
      }) as Map<String, dynamic>);

  Future<CommitList> commits(String sessionId) async =>
      CommitList.fromJson(await _client.call('sessions.commits', {
        'session_id': sessionId,
      }) as Map<String, dynamic>);

  Future<List<ChangedFile>> commitFiles(String sessionId, String sha) async =>
      _filesOf(await _client.call('sessions.commitFiles', {
        'session_id': sessionId,
        'sha': sha,
      }) as Map<String, dynamic>);
}

final changesApiProvider = Provider<ChangesApi>(
  (ref) => ChangesApi(() => ref.read(gatewayProvider)?.client),
);

final changedFilesProvider =
    FutureProvider.autoDispose.family<List<ChangedFile>, String>(
  (ref, sessionId) {
    ref.watch(connStateProvider); // refetch on (re)connect
    return ref.read(changesApiProvider).changedFiles(sessionId);
  },
);

final commitsProvider =
    FutureProvider.autoDispose.family<CommitList, String>(
  (ref, sessionId) {
    ref.watch(connStateProvider);
    return ref.read(changesApiProvider).commits(sessionId);
  },
);

final commitFilesProvider = FutureProvider.autoDispose
    .family<List<ChangedFile>, (String, String)>(
  (ref, key) {
    ref.watch(connStateProvider);
    return ref.read(changesApiProvider).commitFiles(key.$1, key.$2);
  },
);

/// Where a Changes view reads from: a live session's working directory, or a
/// workspace (uncommitted, or against its target branch).
sealed class ChangesSource {
  const ChangesSource();
}

final class SessionChangesSource extends ChangesSource {
  const SessionChangesSource(this.sessionId);
  final String sessionId;

  @override
  bool operator ==(Object other) =>
      other is SessionChangesSource && other.sessionId == sessionId;

  @override
  int get hashCode => sessionId.hashCode;
}

final class WorkspaceChangesSource extends ChangesSource {
  const WorkspaceChangesSource(this.workspaceId, this.against);
  final String workspaceId;
  final String against; // '' = uncommitted; 'target' = against the target branch

  @override
  bool operator ==(Object other) =>
      other is WorkspaceChangesSource &&
      other.workspaceId == workspaceId &&
      other.against == against;

  @override
  int get hashCode => Object.hash(workspaceId, against);
}

// The source providers delegate to the per-kind providers, so an invalidation
// or a test override of those reaches every view.
final sourceFilesProvider = FutureProvider.autoDispose
    .family<List<ChangedFile>, ChangesSource>((ref, src) => switch (src) {
          SessionChangesSource(:final sessionId) =>
            ref.watch(changedFilesProvider(sessionId).future),
          WorkspaceChangesSource(:final workspaceId, :final against) => ref
              .watch(workspaceChangedFilesProvider((workspaceId, against)).future),
        });

final sourceCommitsProvider = FutureProvider.autoDispose
    .family<CommitList, ChangesSource>((ref, src) => switch (src) {
          SessionChangesSource(:final sessionId) =>
            ref.watch(commitsProvider(sessionId).future),
          WorkspaceChangesSource(:final workspaceId) =>
            ref.watch(workspaceCommitsProvider(workspaceId).future),
        });

final sourceCommitFilesProvider = FutureProvider.autoDispose
    .family<List<ChangedFile>, (ChangesSource, String)>(
        (ref, key) => switch (key.$1) {
              SessionChangesSource(:final sessionId) =>
                ref.watch(commitFilesProvider((sessionId, key.$2)).future),
              WorkspaceChangesSource(:final workspaceId) => ref.watch(
                  workspaceCommitFilesProvider((workspaceId, key.$2)).future),
            });

void refreshChanges(WidgetRef ref, ChangesSource source) {
  switch (source) {
    case SessionChangesSource(:final sessionId):
      ref.invalidate(changedFilesProvider(sessionId));
      ref.invalidate(commitsProvider(sessionId));
    case WorkspaceChangesSource(:final workspaceId, :final against):
      ref.invalidate(workspaceChangedFilesProvider((workspaceId, against)));
      ref.invalidate(workspaceCommitsProvider(workspaceId));
  }
}

sealed class DiffContent {
  const DiffContent();
}

/// Old and new file content; the app computes the diff.
final class FullDiff extends DiffContent {
  const FullDiff(this.diff);
  final FileDiff diff;
}

/// A unified diff text computed by the node.
final class UnifiedDiff extends DiffContent {
  const UnifiedDiff(this.diff);
  final WorkspaceDiff diff;
}

Future<DiffContent> fetchDiff(
  ChangesApi changes,
  WorkspaceApi workspace,
  ChangesSource source,
  ChangedFile file, {
  String? rev,
}) async =>
    switch (source) {
      SessionChangesSource(:final sessionId) => FullDiff(await changes
          .fileDiff(sessionId, file.path, origPath: file.origPath, rev: rev)),
      WorkspaceChangesSource(:final workspaceId, :final against) =>
        UnifiedDiff(await workspace.diff(workspaceId, file.path,
            against: against, origPath: file.origPath, rev: rev)),
    };
