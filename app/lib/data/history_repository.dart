import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/result.dart';
import '../models/chunk.dart';
import '../models/history.dart';
import '../state/history.dart';

/// Single entry point for reading session history. The UI depends on this
/// abstraction rather than on the transport-level [HistoryApi], so screens can
/// be tested against a lightweight fake and the data source can change without
/// touching the UI.
abstract class HistoryRepository {
  Future<Result<List<HistoryProject>>> projects();

  Future<Result<HistorySessionPage>> sessions({
    String? nodeId,
    required String projectDir,
    required int limit,
    required int offset,
  });

  Future<Result<List<Chunk>>> transcript({
    String? nodeId,
    required String transcriptPath,
    String? agentId,
    String? agent,
  });
}

/// [HistoryRepository] backed by the gateway over JSON-RPC via [HistoryApi].
class HistoryRepositoryRemote implements HistoryRepository {
  HistoryRepositoryRemote(this._api);
  final HistoryApi _api;

  @override
  Future<Result<List<HistoryProject>>> projects() => _api.projects();

  @override
  Future<Result<HistorySessionPage>> sessions({
    String? nodeId,
    required String projectDir,
    required int limit,
    required int offset,
  }) =>
      _api.sessions(
        nodeId: nodeId,
        projectDir: projectDir,
        limit: limit,
        offset: offset,
      );

  @override
  Future<Result<List<Chunk>>> transcript({
    String? nodeId,
    required String transcriptPath,
    String? agentId,
    String? agent,
  }) =>
      _api.transcript(
          nodeId: nodeId,
          transcriptPath: transcriptPath,
          agentId: agentId,
          agent: agent);
}

final historyRepositoryProvider = Provider<HistoryRepository>(
  (ref) => HistoryRepositoryRemote(ref.read(historyApiProvider)),
);
