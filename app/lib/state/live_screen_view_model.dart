import '../core/command.dart';
import '../data/session_repository.dart';

/// View model for the live screen. The capture poll and the input actions are
/// [Command]s, so the capture's own running-guard replaces the hand-rolled
/// `_busy` flag and input errors carry through the command result.
class LiveScreenViewModel {
  LiveScreenViewModel(this._repo, this.sessionId);
  final SessionRepository _repo;
  final String sessionId;

  late final Command0<String> capture =
      Command0(() => _repo.capture(sessionId));

  late final Command1<void, List<String>> sendKeys =
      Command1((keys) => _repo.sendKeys(sessionId, keys));

  late final Command1<void, String> sendRaw =
      Command1((text) => _repo.sendRaw(sessionId, text));

  void dispose() {
    capture.dispose();
    sendKeys.dispose();
    sendRaw.dispose();
  }
}
