import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/enums.dart';
import '../models/session.dart';
import 'respond_params.dart';

/// Unsent respond-sheet input for one session. The sheet is a modal the user
/// must dismiss to read the transcript behind it, so the draft cannot live in
/// the sheet's State — it would die on every dismissal.
class RespondDraft {
  String text = ''; // idle reply, or the reject reason
  bool denying = false; // the reject free-text field is open
  List<QuestionDraft> questions = const [];
  String _fingerprint = '';

  /// Drops the draft when the pending prompt is no longer the one it was typed
  /// for, so a reason meant for one permission request never lands on the next.
  void syncTo(Interaction ix) {
    final fp = _fingerprintOf(ix);
    if (fp == _fingerprint) return;
    _fingerprint = fp;
    text = '';
    denying = false;
    questions = List.generate(ix.questions.length, (_) => QuestionDraft());
  }

  /// Called after a successful send. Blanking the fingerprint forces the next
  /// open to re-run [syncTo], which rebuilds [questions] at the right length.
  void clear() {
    _fingerprint = '';
    text = '';
    denying = false;
    questions = const [];
  }
}

/// Idle carries no prompt identity, only the session's readiness to take input.
/// Its message also varies between notifications, so keying on the message would
/// drop a reply draft for a cosmetic change.
String _fingerprintOf(Interaction ix) {
  if (ix.kind == InteractionKind.idle) return 'idle';
  return [
    ix.kind.name,
    ix.toolName ?? '',
    ix.toolInput ?? '',
    ix.plan ?? '',
    ix.message ?? '',
    for (final q in ix.questions)
      '${q.header}|${q.question}|${q.multiSelect}|${q.options.join(',')}',
    for (final o in ix.options) o.value,
  ].join('\u0000');
}

// ponytail: not auto-disposed, so one small draft object survives per session id
// seen this run. Add isAutoDispose plus a keepAlive link to the session list if
// that ever grows.
final respondDraftProvider = Provider.family<RespondDraft, String>(
  (ref, sessionId) => RespondDraft(),
);
