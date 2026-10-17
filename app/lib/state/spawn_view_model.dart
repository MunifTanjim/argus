import '../core/command.dart';
import '../data/session_repository.dart';

/// Parameters for spawning a new session.
class SpawnRequest {
  const SpawnRequest({
    this.nodeId,
    this.cwd,
    this.agent,
    this.model,
    required this.prompt,
  });

  final String? nodeId;
  final String? cwd;
  final String? agent;
  final String? model;
  final String prompt;
}

/// View model for the spawn dialog. Exposes the spawn action as a [Command] so
/// the view reflects running/error state without hand-rolled flags.
class SpawnViewModel {
  SpawnViewModel(this._repo);
  final SessionRepository _repo;

  late final Command1<void, SpawnRequest> spawn = Command1(
    (r) => _repo.spawn(
      nodeId: r.nodeId,
      cwd: r.cwd,
      agent: r.agent,
      model: r.model,
      prompt: r.prompt,
    ),
  );

  void dispose() => spawn.dispose();
}
