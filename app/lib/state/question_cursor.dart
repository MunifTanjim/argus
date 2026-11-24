import '../models/session.dart';
import 'respond_params.dart';

enum CursorOutcome { none, submit, cancel, chat }

/// Keyboard cursor over an agent's question list, mirroring the TUI's
/// question flow (tabs per question, a Submit tab when there are several).
class QuestionCursor {
  QuestionCursor(this.questions, {required this.allowUnanswered})
    : drafts = [for (final _ in questions) QuestionDraft()],
      highlight = List.filled(questions.length, 0);

  final List<QuestionSpec> questions;
  final bool allowUnanswered;
  final List<QuestionDraft> drafts;
  int tab = 0;
  final List<int> highlight;
  int submitSel = 0; // 0 = Submit/Proceed, 1 = Cancel/Go back
  bool confirming = false;

  bool get isMulti => questions.length > 1;
  bool get onSubmitTab => isMulti && tab == questions.length;
  int get maxTab => isMulti ? questions.length : questions.length - 1;

  List<String> labels(int q) => questions[q].allowNotes
      ? [...questions[q].options]
      : [...questions[q].options, otherLabel];

  bool otherActive(int q) => highlight[q] == otherIndex(questions[q]);

  /// Committed answer only; the highlight never counts.
  bool answered(int q) => unansweredCount([questions[q]], [drafts[q]]) == 0;

  void up() => _move(-1);
  void down() => _move(1);

  void _move(int d) {
    if (confirming || onSubmitTab) {
      submitSel = (submitSel + d).clamp(0, 1);
    } else {
      highlight[tab] = (highlight[tab] + d).clamp(0, _lastRow(tab));
    }
  }

  int _lastRow(int q) => labels(q).isEmpty ? 0 : labels(q).length - 1;

  void prevTab() {
    if (confirming) return;
    tab = tab > 0 ? tab - 1 : 0;
  }

  void nextTab() {
    if (confirming) return;
    tab = tab < maxTab ? tab + 1 : maxTab;
  }

  void toggle() {
    if (confirming || onSubmitTab || !questions[tab].multiSelect) return;
    final t = drafts[tab].toggles;
    if (!t.remove(highlight[tab])) t.add(highlight[tab]);
  }

  CursorOutcome enter() {
    if (confirming) {
      if (submitSel == 0) return CursorOutcome.submit;
      confirming = false;
      return CursorOutcome.none;
    }
    if (onSubmitTab) {
      if (submitSel == 1) return CursorOutcome.cancel;
      return _submitChecked();
    }
    final q = questions[tab];
    if (!q.multiSelect) {
      if (otherActive(tab) && drafts[tab].custom.trim().isEmpty) {
        return allowUnanswered && !isMulti
            ? _submitChecked()
            : CursorOutcome.none;
      }
      drafts[tab].chosen = highlight[tab];
    }
    if (!isMulti) return _submitChecked();
    tab = tab + 1 > questions.length ? questions.length : tab + 1;
    return CursorOutcome.none;
  }

  /// A click on row [i] of question [q]: highlights it, then commits (single)
  /// or toggles (multi). Returns the outcome so a single-question tap can submit.
  CursorOutcome tapOption(int q, int i) {
    if (confirming) return CursorOutcome.none;
    tab = q;
    highlight[q] = i.clamp(0, _lastRow(q));
    if (questions[q].multiSelect) {
      toggle();
      return CursorOutcome.none;
    }
    return enter();
  }

  /// Submits, first asking for confirmation when questions that may be left
  /// unanswered are (the TUI's submitChecked).
  CursorOutcome _submitChecked() {
    if (allowUnanswered && unansweredCount(questions, drafts) > 0) {
      confirming = true;
      submitSel = 0;
      return CursorOutcome.none;
    }
    return CursorOutcome.submit;
  }
}
