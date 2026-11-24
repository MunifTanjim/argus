import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/respond_params.dart';
import 'package:argus/ui/desktop_interaction.dart';

Session _session(Map<String, dynamic> interaction, {String agent = 'claude'}) =>
    Session.fromJson(
      jsonDecode(
            jsonEncode({
              'id': 'mac:%1',
              'agent': agent,
              'status': 'awaiting',
              'source': 'hooked',
              'tmux': {
                'server': 'argus',
                'pane_id': '%1',
                'session_name': 's',
                'window_index': 0,
                'current_path': '/p',
              },
              'interaction': interaction,
            }),
          )
          as Map<String, dynamic>,
    );

Map<String, dynamic> _ix(
  List<Map<String, dynamic>> questions, {
  String requestId = 'r1',
  bool allowUnanswered = false,
  bool cancelInterrupts = false,
}) => {
  'kind': 'question',
  'request_id': requestId,
  'allow_unanswered': allowUnanswered,
  'cancel_interrupts': cancelInterrupts,
  'questions': questions,
};

Map<String, dynamic> _q(
  String header,
  String question, {
  List<String> options = const ['a', 'b'],
  List<String> previews = const [],
  bool multi = false,
  bool notes = false,
}) => {
  'header': header,
  'question': question,
  'options': options,
  'option_previews': previews,
  'multi_select': multi,
  'allow_notes': notes,
};

Widget _host(
  Session s,
  List<Map<String, dynamic>> sent, {
  double width = 900,
}) => MaterialApp(
  home: Scaffold(
    body: Align(
      alignment: Alignment.topLeft,
      child: SizedBox(
        width: width,
        child: SingleChildScrollView(
          child: DesktopQuestions(
            session: s,
            interaction: s.interaction!,
            busy: false,
            onRespond: sent.add,
          ),
        ),
      ),
    ),
  ),
);

Future<List<Map<String, dynamic>>> _pump(
  WidgetTester tester,
  Map<String, dynamic> ix, {
  double width = 900,
  String agent = 'claude',
}) async {
  debugDefaultTargetPlatformOverride = TargetPlatform.macOS;
  addTearDown(() => debugDefaultTargetPlatformOverride = null);
  tester.view.physicalSize = const Size(1200, 1600);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  final sent = <Map<String, dynamic>>[];
  await tester.pumpWidget(
    _host(_session(ix, agent: agent), sent, width: width),
  );
  await tester.pumpAndSettle();
  return sent;
}

Future<void> _key(WidgetTester tester, LogicalKeyboardKey k) async {
  await tester.sendKeyEvent(k);
  await tester.pumpAndSettle();
}

Finder _field() => find.descendant(
  of: find.byType(DesktopQuestions),
  matching: find.byType(TextField),
);

bool _fieldFocused(WidgetTester tester) => tester
    .widget<EditableText>(
      find.descendant(of: _field(), matching: find.byType(EditableText)),
    )
    .focusNode
    .hasPrimaryFocus;

void main() {
  testWidgets('down then Enter on a single question sends that option', (
    tester,
  ) async {
    final sent = await _pump(tester, _ix([_q('Pick', 'Which one?')]));
    expect(find.text('Claude is asking'), findsOneWidget);
    expect(find.text('Pick'), findsOneWidget);
    expect(find.text(otherLabel), findsOneWidget);
    await _key(tester, LogicalKeyboardKey.arrowDown);
    await _key(tester, LogicalKeyboardKey.enter);
    expect(sent.single['answers'], {'Which one?': 'b'});
    expect(sent.single['request_id'], 'r1');
  });

  testWidgets(
    'multi question: Enter advances tabs; tab bar marks answered with ✓; Submit tab lists answers',
    (tester) async {
      final sent = await _pump(
        tester,
        _ix([_q('H1', 'First?'), _q('', 'Second?')]),
      );
      expect(find.text('H1'), findsOneWidget);
      expect(find.text('Q2'), findsOneWidget);
      expect(find.text('Submit'), findsOneWidget);
      expect(find.text('Claude is asking'), findsNothing);
      await _key(tester, LogicalKeyboardKey.enter);
      expect(find.text('✓ H1'), findsOneWidget);
      expect(find.text('Second?'), findsOneWidget);
      await _key(tester, LogicalKeyboardKey.arrowRight);
      expect(find.text('Review answers'), findsOneWidget);
      expect(find.text('H1: a', findRichText: true), findsOneWidget);
      expect(
        find.text('Q2: (not answered)', findRichText: true),
        findsOneWidget,
      );
      expect(sent, isEmpty);
    },
  );

  testWidgets('a narrow panel shows Question i/n instead of the tab bar', (
    tester,
  ) async {
    await _pump(
      tester,
      _ix([
        _q('A very long header one', 'First?'),
        _q('Another very long header', 'Second?'),
      ]),
      width: 200,
    );
    expect(find.text('Question 1/2'), findsOneWidget);
    expect(find.text('A very long header one'), findsNothing);
  });

  testWidgets('Submit tab Enter sends all answers; down + Enter sends cancel', (
    tester,
  ) async {
    final sent = await _pump(
      tester,
      _ix([_q('H1', 'First?'), _q('H2', 'Second?')]),
    );
    await _key(tester, LogicalKeyboardKey.enter);
    await _key(tester, LogicalKeyboardKey.arrowDown);
    await _key(tester, LogicalKeyboardKey.enter);
    expect(find.text('Review answers'), findsOneWidget);
    await _key(tester, LogicalKeyboardKey.enter);
    expect(sent.single['answers'], {'First?': 'a', 'Second?': 'b'});

    sent.clear();
    await _key(tester, LogicalKeyboardKey.arrowDown);
    expect(find.text('Cancel'), findsOneWidget);
    await _key(tester, LogicalKeyboardKey.enter);
    expect(sent.single, interruptRespond(sessionId: 'mac:%1', requestId: 'r1'));
  });

  testWidgets('unanswered confirm shows inline and Proceed sends', (
    tester,
  ) async {
    final sent = await _pump(
      tester,
      _ix(
        [_q('H1', 'First?'), _q('H2', 'Second?')],
        allowUnanswered: true,
        cancelInterrupts: true,
      ),
    );
    await _key(tester, LogicalKeyboardKey.enter);
    await _key(tester, LogicalKeyboardKey.arrowRight);
    expect(find.text('Interrupt'), findsOneWidget);
    await _key(tester, LogicalKeyboardKey.enter);
    expect(find.text('Submit with unanswered questions?'), findsOneWidget);
    expect(find.text('1 unanswered question'), findsOneWidget);
    expect(find.text('Proceed'), findsOneWidget);
    expect(find.text('Go back'), findsOneWidget);
    expect(sent, isEmpty);
    await _key(tester, LogicalKeyboardKey.enter);
    expect(sent.single['answers'], {'First?': 'a'});
    expect(sent.single['behavior'], 'allow');
  });

  testWidgets(
    'highlighting the other row shows a field; typed j k c n go into it; Enter commits the text',
    (tester) async {
      final sent = await _pump(tester, _ix([_q('Pick', 'Which one?')]));
      expect(_field(), findsNothing);
      await _key(tester, LogicalKeyboardKey.arrowDown);
      await _key(tester, LogicalKeyboardKey.arrowDown);
      expect(_field(), findsOneWidget);
      expect(find.text(otherLabel), findsNothing);
      expect(_fieldFocused(tester), isTrue);
      for (final k in [
        LogicalKeyboardKey.keyJ,
        LogicalKeyboardKey.keyK,
        LogicalKeyboardKey.keyC,
        LogicalKeyboardKey.keyN,
        LogicalKeyboardKey.arrowLeft,
      ]) {
        expect(await tester.sendKeyEvent(k), isFalse);
        await tester.pumpAndSettle();
      }
      expect(_field(), findsOneWidget);
      expect(sent, isEmpty);
      await tester.enterText(_field(), 'jkcn');
      await _key(tester, LogicalKeyboardKey.enter);
      expect(sent.single['answers'], {'Which one?': 'jkcn'});
    },
  );

  testWidgets('Enter on the empty other row does nothing', (tester) async {
    final sent = await _pump(tester, _ix([_q('Pick', 'Which one?')]));
    await _key(tester, LogicalKeyboardKey.arrowDown);
    await _key(tester, LogicalKeyboardKey.arrowDown);
    await _key(tester, LogicalKeyboardKey.enter);
    expect(sent, isEmpty);
    expect(_field(), findsOneWidget);
  });

  testWidgets(
    'n opens the note; typed text is kept after Enter; the note is sent',
    (tester) async {
      final sent = await _pump(
        tester,
        _ix([_q('H1', 'First?', notes: true), _q('H2', 'Second?')]),
      );
      expect(find.text(otherLabel), findsNothing);
      await _key(tester, LogicalKeyboardKey.keyN);
      expect(_field(), findsOneWidget);
      expect(_fieldFocused(tester), isTrue);
      await tester.enterText(_field(), 'be careful');
      await _key(tester, LogicalKeyboardKey.enter);
      expect(_field(), findsNothing);
      expect(find.text('Second?'), findsOneWidget);
      await _key(tester, LogicalKeyboardKey.arrowLeft);
      expect(find.textContaining('be careful'), findsOneWidget);
      await _key(tester, LogicalKeyboardKey.arrowRight);
      await _key(tester, LogicalKeyboardKey.arrowRight);
      expect(find.textContaining('note: be careful'), findsOneWidget);
      await _key(tester, LogicalKeyboardKey.enter);
      expect(sent.single['notes'], {'First?': 'be careful'});
      expect(sent.single['answers'], {'First?': 'a'});
    },
  );

  testWidgets('Space toggles multi-select rows; Enter on multi advances', (
    tester,
  ) async {
    final sent = await _pump(
      tester,
      _ix([_q('H1', 'First?', multi: true), _q('H2', 'Second?')]),
    );
    expect(find.byIcon(Icons.check_box_outline_blank), findsNWidgets(3));
    await _key(tester, LogicalKeyboardKey.space);
    await _key(tester, LogicalKeyboardKey.arrowDown);
    await _key(tester, LogicalKeyboardKey.space);
    expect(find.byIcon(Icons.check_box), findsNWidgets(2));
    await _key(tester, LogicalKeyboardKey.space);
    expect(find.byIcon(Icons.check_box), findsOneWidget);
    await _key(tester, LogicalKeyboardKey.enter);
    expect(find.text('Second?'), findsOneWidget);
    await _key(tester, LogicalKeyboardKey.enter);
    await _key(tester, LogicalKeyboardKey.enter);
    expect(sent.single['answers'], {
      'First?': ['a'],
      'Second?': 'a',
    });
  });

  testWidgets('c sends clarify (interrupt when cancelInterrupts)', (
    tester,
  ) async {
    var sent = await _pump(tester, _ix([_q('Pick', 'Which one?')]));
    expect(find.text('c · chat about this'), findsOneWidget);
    await _key(tester, LogicalKeyboardKey.keyC);
    expect(sent.single['question_action'], 'chat');

    sent = await _pump(
      tester,
      _ix([_q('Pick', 'Which one?')], requestId: 'r2', cancelInterrupts: true),
    );
    expect(find.text('c · interrupt'), findsOneWidget);
    await _key(tester, LogicalKeyboardKey.keyC);
    expect(sent.single, interruptRespond(sessionId: 'mac:%1', requestId: 'r2'));
  });

  testWidgets(
    'Esc with no field open is not handled; Esc with a field open closes it',
    (tester) async {
      await _pump(tester, _ix([_q('Pick', 'Which one?', notes: true)]));
      expect(await tester.sendKeyEvent(LogicalKeyboardKey.escape), isFalse);
      await _key(tester, LogicalKeyboardKey.keyN);
      expect(_field(), findsOneWidget);
      expect(await tester.sendKeyEvent(LogicalKeyboardKey.escape), isTrue);
      await tester.pumpAndSettle();
      expect(_field(), findsNothing);
      // Panel keys work again once the field is closed.
      expect(await tester.sendKeyEvent(LogicalKeyboardKey.keyJ), isTrue);
      await tester.pumpAndSettle();
      expect(await tester.sendKeyEvent(LogicalKeyboardKey.escape), isFalse);
    },
  );

  testWidgets('Esc closes the other-row field and j moves again', (
    tester,
  ) async {
    final sent = await _pump(tester, _ix([_q('Pick', 'Which one?')]));
    await _key(tester, LogicalKeyboardKey.arrowDown);
    await _key(tester, LogicalKeyboardKey.arrowDown);
    expect(_field(), findsOneWidget);
    expect(await tester.sendKeyEvent(LogicalKeyboardKey.escape), isTrue);
    await tester.pumpAndSettle();
    expect(_field(), findsNothing);
    await _key(tester, LogicalKeyboardKey.keyK);
    await _key(tester, LogicalKeyboardKey.enter);
    expect(sent.single['answers'], {'Which one?': 'b'});
  });

  testWidgets(
    'the highlighted option preview shows at the right at 900 px and under the options at 500 px',
    (tester) async {
      final ix = _ix([
        _q('Pick', 'Which one?', previews: ['preview-a', 'preview-b']),
      ]);
      await _pump(tester, ix);
      final box = find.byKey(const ValueKey('question-preview'));
      expect(box, findsOneWidget);
      expect(
        find.textContaining('preview-a', findRichText: true),
        findsWidgets,
      );
      final row = find.text('a');
      expect(
        tester.getTopLeft(box).dx,
        greaterThan(tester.getTopRight(row).dx),
      );
      await _key(tester, LogicalKeyboardKey.arrowDown);
      expect(
        find.textContaining('preview-b', findRichText: true),
        findsWidgets,
      );
      expect(
        find.textContaining('preview-a', findRichText: true),
        findsNothing,
      );

      await _pump(tester, ix, width: 500);
      expect(
        tester.getTopLeft(box).dy,
        greaterThan(tester.getBottomLeft(find.text(otherLabel)).dy),
      );
    },
  );

  testWidgets('a new requestId resets the cursor', (tester) async {
    final ix = _ix([_q('H1', 'First?'), _q('H2', 'Second?')]);
    final sent = await _pump(tester, ix);
    await _key(tester, LogicalKeyboardKey.enter);
    expect(find.text('✓ H1'), findsOneWidget);
    final next = _session({...ix, 'request_id': 'r9'});
    await tester.pumpWidget(_host(next, sent));
    await tester.pumpAndSettle();
    expect(find.text('✓ H1'), findsNothing);
    expect(find.text('First?'), findsOneWidget);
  });

  testWidgets('clicking an option row commits it', (tester) async {
    final sent = await _pump(tester, _ix([_q('Pick', 'Which one?')]));
    await tester.tap(find.text('b'));
    await tester.pumpAndSettle();
    expect(sent.single['answers'], {'Which one?': 'b'});
  });

  testWidgets('keys are ignored while busy', (tester) async {
    debugDefaultTargetPlatformOverride = TargetPlatform.macOS;
    addTearDown(() => debugDefaultTargetPlatformOverride = null);
    final sent = <Map<String, dynamic>>[];
    final s = _session(_ix([_q('Pick', 'Which one?')]));
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: DesktopQuestions(
            session: s,
            interaction: s.interaction!,
            busy: true,
            onRespond: sent.add,
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(await tester.sendKeyEvent(LogicalKeyboardKey.enter), isFalse);
    expect(sent, isEmpty);
  });
}
