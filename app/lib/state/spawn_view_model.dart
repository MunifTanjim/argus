import '../core/command.dart';
import '../data/session_repository.dart';

/// Parameters for spawning a new session.
class SpawnRequest {
  const SpawnRequest({
    this.nodeId,
    required this.name,
    this.cwd,
    this.command,
  });

  final String? nodeId;
  final String name;
  final String? cwd;
  final String? command;
}

/// View model for the spawn dialog. Exposes the spawn action as a [Command] so
/// the view reflects running/error state without hand-rolled flags.
class SpawnViewModel {
  SpawnViewModel(this._repo);
  final SessionRepository _repo;

  late final Command1<void, SpawnRequest> spawn = Command1(
    (r) => _repo.spawn(
      nodeId: r.nodeId,
      name: r.name,
      cwd: r.cwd,
      command: r.command,
    ),
  );

  void dispose() => spawn.dispose();
}
