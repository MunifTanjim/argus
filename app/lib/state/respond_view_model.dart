import '../core/command.dart';
import '../data/session_repository.dart';

/// Argument for the send-input command.
typedef InputArg = ({String sessionId, String text});

/// View model for the respond sheet. Both ways of answering an interaction
/// (echoing a decision/answer via [respond], or replying to an idle session via
/// [sendInput]) are [Command]s, so the sheet reflects in-flight and error state
/// without hand-rolled flags.
class RespondViewModel {
  RespondViewModel(this._repo);
  final SessionRepository _repo;

  late final Command1<void, Map<String, dynamic>> respond =
      Command1((params) => _repo.respond(params));

  late final Command1<void, InputArg> sendInput =
      Command1((a) => _repo.sendInput(a.sessionId, a.text));

  bool get running => respond.running || sendInput.running;

  void dispose() {
    respond.dispose();
    sendInput.dispose();
  }
}
