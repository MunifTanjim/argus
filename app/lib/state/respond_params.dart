import '../models/session.dart';

/// Synthetic last option exposing a free-text answer (matches the TUI).
const String otherLabel = '✎ type your own…';

/// Index of a question's "type your own" entry, or -1 when the question takes
/// notes instead (Codex).
int otherIndex(QuestionSpec q) => q.allowNotes ? -1 : q.options.length;

/// Ephemeral per-question draft captured by the respond sheet.
class QuestionDraft {
  int chosen = -1; // committed single-select option index; -1 = unanswered
  final Set<int> toggles = {}; // multi-select toggled option indices
  String custom = ''; // "type your own" text
  String note = ''; // free-text note (allow_notes questions)
  bool noteOpen = false; // note field shown
}

/// Trimmed, non-empty notes keyed by question text, for allow_notes questions.
Map<String, String> _collectNotes(
    List<QuestionSpec> questions, List<QuestionDraft> drafts) {
  final notes = <String, String>{};
  for (var i = 0; i < questions.length; i++) {
    final n = drafts[i].note.trim();
    if (questions[i].allowNotes && n.isNotEmpty) {
      notes[questions[i].question ?? ''] = n;
    }
  }
  return notes;
}

Map<String, dynamic> _requestId(String? requestId) =>
    (requestId ?? '').isEmpty ? const {} : {'request_id': requestId};

/// A server-built DecisionOption choice the user picked. The node maps [value]
/// to the hook decision; [reason] (reject free-text) is dropped when blank.
Map<String, dynamic> optionRespond({
  required String sessionId,
  required String kind,
  required String value,
  String? reason,
  String? requestId,
}) {
  final r = (reason ?? '').trim();
  return {
    'session_id': sessionId,
    'kind': kind,
    ..._requestId(requestId),
    'option_value': value,
    if (r.isNotEmpty) 'reason': r,
  };
}

/// One question's committed answer: `String` (single), `List<String>` (multi),
/// or the custom text. Null when unanswered. Mirrors the TUI's questionAnswer.
Object? _answer(QuestionSpec q, QuestionDraft d) {
  final oi = otherIndex(q);
  final custom = d.custom.trim();
  if (q.multiSelect) {
    final labels = <String>[];
    for (var i = 0; i < q.options.length; i++) {
      if (d.toggles.contains(i)) labels.add(q.options[i]);
    }
    if (d.toggles.contains(oi) && custom.isNotEmpty) labels.add(custom);
    return labels.isEmpty ? null : labels;
  }
  if (d.chosen < 0) return null;
  if (d.chosen == oi) return custom.isEmpty ? null : custom;
  if (d.chosen < q.options.length) return q.options[d.chosen];
  return null;
}

/// Collects committed answers across all questions, keyed by question text.
/// Unanswered questions are omitted.
Map<String, Object?> _collectAnswers(
    List<QuestionSpec> questions, List<QuestionDraft> drafts) {
  final answers = <String, Object?>{};
  for (var i = 0; i < questions.length; i++) {
    final a = _answer(questions[i], drafts[i]);
    if (a != null) answers[questions[i].question ?? ''] = a;
  }
  return answers;
}

/// AskUserQuestion answers across all questions. Returns null when nothing is
/// answered (a no-op submit).
Map<String, dynamic>? questionRespond({
  required String sessionId,
  required List<QuestionSpec> questions,
  required List<QuestionDraft> drafts,
  bool allowUnanswered = false,
  String? requestId,
}) {
  final answers = _collectAnswers(questions, drafts);
  if (answers.isEmpty && !allowUnanswered) return null;
  final notes = _collectNotes(questions, drafts);
  return {
    'session_id': sessionId,
    'kind': 'question',
    ..._requestId(requestId),
    'behavior': 'allow',
    if (answers.isNotEmpty) 'answers': answers,
    if (notes.isNotEmpty) 'notes': notes,
  };
}

int unansweredCount(List<QuestionSpec> questions, List<QuestionDraft> drafts) {
  final answers = _collectAnswers(questions, drafts);
  return questions.where((q) => !answers.containsKey(q.question)).length;
}

/// Interrupts the turn that asked the questions, sending no answers (Codex).
Map<String, dynamic> interruptRespond({
  required String sessionId,
  String? requestId,
}) =>
    {
      'session_id': sessionId,
      'kind': 'question',
      ..._requestId(requestId),
      'question_action': 'cancel',
    };

/// "Chat about this": reject the question prompt with a clarify request,
/// carrying any partial answers. Always valid — never returns null.
Map<String, dynamic> clarifyRespond({
  required String sessionId,
  required List<QuestionSpec> questions,
  required List<QuestionDraft> drafts,
  String? requestId,
}) {
  final answers = _collectAnswers(questions, drafts);
  return {
    'session_id': sessionId,
    'kind': 'question',
    ..._requestId(requestId),
    'question_action': 'chat',
    if (answers.isNotEmpty) 'answers': answers,
  };
}
