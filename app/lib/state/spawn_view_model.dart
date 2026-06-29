import '../core/command.dart';
import '../data/session_repository.dart';

/// Parameters for spawning a new session.
class SpawnRequest {
  const SpawnRequest({
    this.nodeId,
    this.cwd,
    required this.prompt,
  });

  final String? nodeId;
  final String? cwd;
  final String prompt;
}

/// View model for the spawn dialog. Exposes the spawn action as a [Command] so
/// the view reflects running/error state without hand-rolled flags.
class SpawnViewModel {
  SpawnViewModel(this._repo);
  final SessionRepository _repo;

  late final Command1<void, SpawnRequest> spawn = Command1(
    (r) => _repo.spawn(nodeId: r.nodeId, cwd: r.cwd, prompt: r.prompt),
  );

  void dispose() => spawn.dispose();
}
