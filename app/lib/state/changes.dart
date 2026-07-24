import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/changes.dart';
import '../transport/gateway_client.dart';
import 'gateway.dart';

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
