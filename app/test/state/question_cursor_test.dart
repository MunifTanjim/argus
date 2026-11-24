import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/question_cursor.dart';
import 'package:argus/state/respond_params.dart';

int _n = 0;

QuestionSpec q({
  List<String> options = const ['a', 'b'],
  bool multi = false,
  bool notes = false,
}) => QuestionSpec(
  question: 'Q${_n++}',
  options: options,
  multiSelect: multi,
  allowNotes: notes,
);

void main() {
  test('single question: Enter commits the highlight and returns submit', () {
    final c = QuestionCursor([q()], allowUnanswered: false);
    c.down();
    expect(c.enter(), CursorOutcome.submit);
    expect(c.drafts[0].chosen, 1);
  });

  test(
    'multi question: Enter commits and moves to the next tab, then the Submit tab',
    () {
      final c = QuestionCursor([
        q(),
        q(options: ['x', 'y']),
      ], allowUnanswered: false);
      expect(c.isMulti, isTrue);
      expect(c.maxTab, 2);
      expect(c.enter(), CursorOutcome.none);
      expect(c.drafts[0].chosen, 0);
      expect(c.tab, 1);
      expect(c.enter(), CursorOutcome.none);
      expect(c.tab, 2);
      expect(c.onSubmitTab, isTrue);
      expect(c.enter(), CursorOutcome.submit);
    },
  );

  test('Enter on an empty other row commits nothing and returns none', () {
    final c = QuestionCursor([q()], allowUnanswered: false);
    c.down();
    c.down();
    expect(c.otherActive(0), isTrue);
    expect(c.enter(), CursorOutcome.none);
    expect(c.drafts[0].chosen, -1);

    final u = QuestionCursor([q()], allowUnanswered: true);
    u.down();
    u.down();
    expect(u.enter(), CursorOutcome.none);
    expect(u.confirming, isTrue);
    expect(u.drafts[0].chosen, -1);

    final m = QuestionCursor([q(), q()], allowUnanswered: true);
    m.down();
    m.down();
    expect(m.enter(), CursorOutcome.none);
    expect(m.tab, 0);

    c.drafts[0].custom = ' hi ';
    expect(c.enter(), CursorOutcome.submit);
    expect(c.drafts[0].chosen, 2);
  });

  test('Space toggles the highlighted row on multi-select only', () {
    final c = QuestionCursor([q(multi: true), q()], allowUnanswered: false);
    c.toggle();
    expect(c.drafts[0].toggles, {0});
    c.toggle();
    expect(c.drafts[0].toggles, isEmpty);
    c.nextTab();
    c.toggle();
    expect(c.drafts[1].toggles, isEmpty);
    expect(c.drafts[1].chosen, -1);
  });

  test(
    'left on the Submit tab goes to the last question; right stops at the Submit tab',
    () {
      final c = QuestionCursor([q(), q()], allowUnanswered: false);
      c.prevTab();
      expect(c.tab, 0);
      c.nextTab();
      c.nextTab();
      c.nextTab();
      expect(c.tab, 2);
      c.prevTab();
      expect(c.tab, 1);

      final s = QuestionCursor([q()], allowUnanswered: false);
      s.nextTab();
      expect(s.tab, 0);
    },
  );

  test('up/down clamp highlight', () {
    final c = QuestionCursor([q()], allowUnanswered: false);
    c.up();
    expect(c.highlight[0], 0);
    for (var i = 0; i < 5; i++) {
      c.down();
    }
    expect(c.highlight[0], 2);
    final n = QuestionCursor([q(notes: true)], allowUnanswered: false);
    expect(n.labels(0), ['a', 'b']);
    for (var i = 0; i < 5; i++) {
      n.down();
    }
    expect(n.highlight[0], 1);
    expect(n.otherActive(0), isFalse);
  });

  test('Submit tab: down then Enter returns cancel', () {
    final c = QuestionCursor([q(), q()], allowUnanswered: false);
    c.tab = 2;
    c.down();
    expect(c.submitSel, 1);
    c.down();
    expect(c.submitSel, 1);
    expect(c.enter(), CursorOutcome.cancel);
    c.up();
    c.up();
    expect(c.submitSel, 0);
  });

  test(
    'Submit with unanswered questions and allowUnanswered opens confirm; Go back closes it; Proceed returns submit',
    () {
      final c = QuestionCursor([q(), q()], allowUnanswered: true);
      c.drafts[0].chosen = 0;
      c.tab = 2;
      expect(c.enter(), CursorOutcome.none);
      expect(c.confirming, isTrue);
      expect(c.submitSel, 0);
      c.down();
      expect(c.enter(), CursorOutcome.none);
      expect(c.confirming, isFalse);
      c.submitSel = 0;
      c.enter();
      expect(c.confirming, isTrue);
      expect(c.enter(), CursorOutcome.submit);

      final all = QuestionCursor([q(), q()], allowUnanswered: true);
      all.drafts[0].chosen = 0;
      all.drafts[1].chosen = 1;
      all.tab = 2;
      expect(all.enter(), CursorOutcome.submit);

      final strict = QuestionCursor([q(), q()], allowUnanswered: false);
      strict.tab = 2;
      expect(strict.enter(), CursorOutcome.submit);
    },
  );

  test(
    'answered() reflects committed choice and toggles, never the highlight',
    () {
      final c = QuestionCursor([q(), q(multi: true)], allowUnanswered: false);
      c.down();
      expect(c.answered(0), isFalse);
      c.enter();
      expect(c.answered(0), isTrue);
      expect(c.answered(1), isFalse);
      c.toggle();
      expect(c.answered(1), isTrue);
      expect(unansweredCount(c.questions, c.drafts), 0);
    },
  );

  test('labels() omits otherLabel for allowNotes questions', () {
    final c = QuestionCursor([q(), q(notes: true)], allowUnanswered: false);
    expect(c.labels(0), ['a', 'b', otherLabel]);
    expect(c.labels(1), ['a', 'b']);
  });

  test('tapOption on single-select commits like enter; on multi toggles', () {
    final s = QuestionCursor([q()], allowUnanswered: false);
    expect(s.tapOption(0, 1), CursorOutcome.submit);
    expect(s.highlight[0], 1);
    expect(s.drafts[0].chosen, 1);

    final m = QuestionCursor([q(), q(multi: true)], allowUnanswered: false);
    expect(m.tapOption(0, 1), CursorOutcome.none);
    expect(m.tab, 1);
    expect(m.tapOption(1, 0), CursorOutcome.none);
    expect(m.tapOption(1, 1), CursorOutcome.none);
    expect(m.drafts[1].toggles, {0, 1});
    m.tapOption(1, 0);
    expect(m.drafts[1].toggles, {1});
    expect(m.tab, 1);
  });

  test(
    'single question with allowUnanswered opens confirm when unanswered, like the TUI submitChecked',
    () {
      final m = QuestionCursor([q(multi: true)], allowUnanswered: true);
      expect(m.enter(), CursorOutcome.none);
      expect(m.confirming, isTrue);
      expect(m.submitSel, 0);
      m.down();
      expect(m.enter(), CursorOutcome.none);
      expect(m.confirming, isFalse);
      m.toggle();
      expect(m.enter(), CursorOutcome.submit);

      final t = QuestionCursor([q(multi: true)], allowUnanswered: true);
      expect(t.tapOption(0, 0), CursorOutcome.none);
      expect(t.confirming, isFalse);
      t.drafts[0].toggles.clear();
      expect(t.enter(), CursorOutcome.none);
      expect(t.confirming, isTrue);
      expect(t.tapOption(0, 0), CursorOutcome.none);
      expect(t.enter(), CursorOutcome.submit);

      final s = QuestionCursor([q()], allowUnanswered: true);
      expect(s.tapOption(0, 1), CursorOutcome.submit);
    },
  );

  test('a notes question with no options does not crash on move or tap', () {
    final c = QuestionCursor([q(options: [], notes: true)],
        allowUnanswered: true);
    expect(c.labels(0), isEmpty);
    c.down();
    c.up();
    expect(c.highlight[0], 0);
    expect(c.otherActive(0), isFalse);
    expect(c.tapOption(0, 3), CursorOutcome.none);
    expect(c.highlight[0], 0);
    expect(c.confirming, isTrue);
  });

  test('on multi-select the other row is toggled exactly while custom text is set', () {
    final c = QuestionCursor([q(multi: true), q()], allowUnanswered: false);
    final oi = otherIndex(c.questions[0]);
    c.setCustom(0, 'mine');
    expect(c.drafts[0].custom, 'mine');
    expect(c.drafts[0].toggles, {oi});
    expect(questionAnswer(c.questions[0], c.drafts[0]), ['mine']);
    c.down();
    c.down();
    c.toggle(); // the text decides, not Space
    expect(c.drafts[0].toggles, {oi});
    c.setCustom(0, '  ');
    expect(c.drafts[0].toggles, isEmpty);
    c.toggle();
    expect(c.drafts[0].toggles, isEmpty);
    c.setCustom(1, 'x'); // single-select: no toggles
    expect(c.drafts[1].custom, 'x');
    expect(c.drafts[1].toggles, isEmpty);
  });
}
