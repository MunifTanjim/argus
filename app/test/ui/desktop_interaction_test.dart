import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/respond_params.dart';
import 'package:argus/ui/desktop_interaction.dart';
import 'package:argus/ui/interaction_panel.dart';

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
        child: DesktopQuestions(
          session: s,
          interaction: s.interaction!,
          busy: false,
          onRespond: sent.add,
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
    await tester.pump(); // the busy progress bar never settles
    expect(await tester.sendKeyEvent(LogicalKeyboardKey.enter), isFalse);
    expect(sent, isEmpty);
  });

  testWidgets('Space with the custom field open does not toggle a multi row', (
    tester,
  ) async {
    await _pump(tester, _ix([_q('Pick', 'Which one?', multi: true)]));
    await _key(tester, LogicalKeyboardKey.arrowDown);
    await _key(tester, LogicalKeyboardKey.arrowDown);
    expect(_field(), findsOneWidget);
    expect(await tester.sendKeyEvent(LogicalKeyboardKey.space), isFalse);
    await tester.pumpAndSettle();
    expect(find.byIcon(Icons.check_box), findsNothing);
  });

  testWidgets('a new requestId with the custom field open does not throw', (
    tester,
  ) async {
    final ix = _ix([_q('Pick', 'Which one?')]);
    final sent = await _pump(tester, ix);
    await _key(tester, LogicalKeyboardKey.arrowDown);
    await _key(tester, LogicalKeyboardKey.arrowDown);
    await tester.enterText(_field(), 'typing');
    await tester.pumpWidget(_host(_session({...ix, 'request_id': 'r9'}), sent));
    expect(tester.takeException(), isNull);
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    expect(_field(), findsNothing);
  });

  testWidgets('typing a custom answer marks the tab answered', (tester) async {
    await _pump(
      tester,
      _ix([_q('H1', 'First?', multi: true), _q('H2', 'Second?')]),
    );
    await tester.tap(find.text(otherLabel));
    await tester.pumpAndSettle();
    expect(find.text('✓ H1'), findsNothing);
    await tester.enterText(_field(), 'mine');
    await tester.pump();
    expect(find.text('✓ H1'), findsOneWidget);
  });

  group('scrolling and focus', () {
    final longText = List.generate(80, (i) => 'line $i').join('\n\n');

    Future<void> pumpShort(
      WidgetTester tester,
      Widget w, {
      bool settle = true,
    }) async {
      debugDefaultTargetPlatformOverride = TargetPlatform.macOS;
      addTearDown(() => debugDefaultTargetPlatformOverride = null);
      tester.view.physicalSize = const Size(1200, 400);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(body: Align(alignment: Alignment.topLeft, child: w)),
        ),
      );
      // A busy progress bar never settles.
      settle ? await tester.pumpAndSettle() : await tester.pump();
    }

    DesktopQuestions questions(Session s, {bool busy = false}) =>
        DesktopQuestions(
          session: s,
          interaction: s.interaction!,
          busy: busy,
          onRespond: (_) {},
        );

    testWidgets('a long question scrolls and its options stay reachable', (
      tester,
    ) async {
      final s = _session(_ix([_q('Pick', longText)]));
      await pumpShort(tester, questions(s));
      expect(tester.takeException(), isNull);
      expect(
        find.descendant(
          of: find.byType(DesktopQuestions),
          matching: find.byType(SingleChildScrollView),
        ),
        findsOneWidget,
      );
      await tester.drag(
        find.byType(SingleChildScrollView),
        const Offset(0, -20000),
      );
      await tester.pumpAndSettle();
      expect(tester.getBottomLeft(find.text('b')).dy, lessThanOrEqualTo(400));
    });

    testWidgets('the Submit tab controls stay visible under a long summary', (
      tester,
    ) async {
      final s = _session(
        _ix([_q('One', longText), _q('Two', 'second')]),
      );
      await pumpShort(tester, questions(s));
      await tester.sendKeyEvent(LogicalKeyboardKey.arrowRight);
      await tester.sendKeyEvent(LogicalKeyboardKey.arrowRight);
      await tester.pumpAndSettle();
      expect(tester.takeException(), isNull);
      expect(find.text('Review answers'), findsOneWidget);
      expect(
        tester.getBottomLeft(find.text('Cancel')).dy,
        lessThanOrEqualTo(400),
      );
    });

    testWidgets('busy shows the progress bar', (tester) async {
      final s = _session(_ix([_q('Pick', 'Which one?')]));
      await pumpShort(tester, questions(s, busy: true), settle: false);
      expect(find.byType(LinearProgressIndicator), findsOneWidget);
    });

    testWidgets('a long plan scrolls and the options stay reachable', (
      tester,
    ) async {
      final s = _session({
        'kind': 'plan',
        'request_id': 'p1',
        'plan': longText,
        'options': [
          {'label': 'Yes', 'value': 'allow'},
          {'label': 'No', 'value': 'deny', 'reject': true},
        ],
      });
      await pumpShort(
        tester,
        DesktopDecision(
          session: s,
          interaction: s.interaction!,
          kind: 'plan',
          detail: decisionDetail(s, s.interaction!, 'plan'),
          busy: false,
          onRespond: (_) {},
        ),
      );
      expect(tester.takeException(), isNull);
      expect(tester.getBottomLeft(find.text('No')).dy, lessThanOrEqualTo(400));
    });

    testWidgets('busy decision shows the progress bar', (tester) async {
      final s = _session({'kind': 'permission', 'request_id': 'd1'});
      await pumpShort(
        tester,
        DesktopDecision(
          session: s,
          interaction: s.interaction!,
          kind: 'permission',
          detail: const [],
          busy: true,
          onRespond: (_) {},
        ),
        settle: false,
      );
      expect(find.byType(LinearProgressIndicator), findsOneWidget);
    });

    String? focusedLabel() => FocusManager.instance.primaryFocus?.debugLabel;

    // A text field outside the panel that holds focus until the panel asks.
    Future<ValueNotifier<Session?>> pumpSwappable(
      WidgetTester tester,
      Session? first,
      Widget Function(Session s) panel,
    ) async {
      debugDefaultTargetPlatformOverride = TargetPlatform.macOS;
      addTearDown(() => debugDefaultTargetPlatformOverride = null);
      tester.view.physicalSize = const Size(1200, 1600);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      final current = ValueNotifier<Session?>(first);
      addTearDown(current.dispose);
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: ValueListenableBuilder<Session?>(
              valueListenable: current,
              builder: (_, s, _) => Column(
                children: [
                  const TextField(key: ValueKey('outside')),
                  if (s != null) Expanded(child: panel(s)),
                ],
              ),
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();
      return current;
    }

    testWidgets('the question panel takes focus on first mount', (
      tester,
    ) async {
      final s = _session(_ix([_q('Pick', 'Which one?')]));
      final cur = await pumpSwappable(tester, null, questions);
      await tester.tap(find.byKey(const ValueKey('outside')));
      await tester.pump();
      expect(focusedLabel(), isNot('DesktopQuestions'));
      cur.value = s;
      await tester.pumpAndSettle();
      expect(focusedLabel(), 'DesktopQuestions');
    });

    testWidgets('a new request id returns focus to the question panel', (
      tester,
    ) async {
      final ix = _ix([_q('Pick', 'Which one?')]);
      final cur = await pumpSwappable(tester, _session(ix), questions);
      await tester.tap(find.byKey(const ValueKey('outside')));
      await tester.pump();
      expect(focusedLabel(), isNot('DesktopQuestions'));
      cur.value = _session({...ix, 'request_id': 'r2'});
      await tester.pumpAndSettle();
      expect(focusedLabel(), 'DesktopQuestions');
    });

    DesktopDecision decision(Session s, String kind) => DesktopDecision(
      session: s,
      interaction: s.interaction!,
      kind: kind,
      detail: const [],
      busy: false,
      onRespond: (_) {},
    );

    testWidgets('the decision panel takes focus on first mount', (
      tester,
    ) async {
      final s = _session({'kind': 'permission', 'request_id': 'd1'});
      final cur = await pumpSwappable(
        tester,
        null,
        (s) => decision(s, 'permission'),
      );
      await tester.tap(find.byKey(const ValueKey('outside')));
      await tester.pump();
      cur.value = s;
      await tester.pumpAndSettle();
      expect(focusedLabel(), 'DesktopDecision');
    });

    testWidgets('a new request id returns focus to the decision panel', (
      tester,
    ) async {
      final cur = await pumpSwappable(
        tester,
        _session({'kind': 'permission', 'request_id': 'd1'}),
        (s) => decision(s, 'permission'),
      );
      await tester.tap(find.byKey(const ValueKey('outside')));
      await tester.pump();
      cur.value = _session({'kind': 'permission', 'request_id': 'd2'});
      await tester.pumpAndSettle();
      expect(focusedLabel(), 'DesktopDecision');
    });

    testWidgets('a new kind returns focus to the decision panel', (
      tester,
    ) async {
      var kind = 'permission';
      final cur = await pumpSwappable(
        tester,
        _session({'kind': 'permission', 'request_id': 'd1'}),
        (s) => decision(s, kind),
      );
      await tester.tap(find.byKey(const ValueKey('outside')));
      await tester.pump();
      kind = 'plan';
      cur.value = _session({'kind': 'permission', 'request_id': 'd1'});
      await tester.pumpAndSettle();
      expect(focusedLabel(), 'DesktopDecision');
    });

    testWidgets('a new request id with the reason field open refocuses the panel', (
      tester,
    ) async {
      final cur = await pumpSwappable(
        tester,
        _session({'kind': 'permission', 'request_id': 'd1'}),
        (s) => decision(s, 'permission'),
      );
      await _key(tester, LogicalKeyboardKey.arrowDown);
      await _key(tester, LogicalKeyboardKey.enter);
      expect(find.text('Reason (optional)'), findsOneWidget);
      cur.value = _session({'kind': 'permission', 'request_id': 'd2'});
      await tester.pumpAndSettle();
      expect(find.text('Reason (optional)'), findsNothing);
      expect(focusedLabel(), 'DesktopDecision');
    });
  });

  group('DesktopDecision', () {
    Map<String, dynamic> dix({
      String kind = 'permission',
      List<Map<String, dynamic>>? options,
      String? plan,
      String? toolInput,
    }) => {
      'kind': kind,
      'request_id': 'd1',
      'tool_name': 'Bash',
      'message': 'Needs approval',
      'tool_input': ?toolInput,
      'plan': ?plan,
      'options': ?options,
    };

    final threeOptions = [
      {'label': 'Yes', 'value': 'allow'},
      {'label': 'Yes, always', 'value': 'always'},
      {
        'label': 'No',
        'value': 'deny',
        'reject': true,
        'placeholder': 'Tell why',
      },
    ];

    Future<List<Map<String, dynamic>>> pumpDecision(
      WidgetTester tester,
      Map<String, dynamic> ix, {
      String kind = 'permission',
    }) async {
      debugDefaultTargetPlatformOverride = TargetPlatform.macOS;
      addTearDown(() => debugDefaultTargetPlatformOverride = null);
      tester.view.physicalSize = const Size(1200, 1600);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      final sent = <Map<String, dynamic>>[];
      final s = _session(ix);
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: DesktopDecision(
              session: s,
              interaction: s.interaction!,
              kind: kind,
              detail: decisionDetail(s, s.interaction!, kind),
              busy: false,
              onRespond: sent.add,
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();
      return sent;
    }

    Finder reasonField() => find.descendant(
      of: find.byType(DesktopDecision),
      matching: find.byType(TextField),
    );

    testWidgets("down + Enter sends the second option's value", (tester) async {
      final sent = await pumpDecision(tester, dix(options: threeOptions));
      await _key(tester, LogicalKeyboardKey.arrowDown);
      await _key(tester, LogicalKeyboardKey.enter);
      expect(
        sent.single,
        optionRespond(
          sessionId: 'mac:%1',
          kind: 'permission',
          value: 'always',
          requestId: 'd1',
        ),
      );
    });

    testWidgets('the default Allow/Deny list applies when options are empty', (
      tester,
    ) async {
      final sent = await pumpDecision(tester, dix());
      expect(find.text('Allow'), findsOneWidget);
      await _key(tester, LogicalKeyboardKey.keyJ);
      await _key(tester, LogicalKeyboardKey.keyK);
      await _key(tester, LogicalKeyboardKey.enter);
      expect(sent.single['option_value'], 'allow');
    });

    testWidgets(
      'Enter on the reject option opens the reason field; typed text + Enter sends reject with reason',
      (tester) async {
        final sent = await pumpDecision(tester, dix(options: threeOptions));
        await _key(tester, LogicalKeyboardKey.arrowDown);
        await _key(tester, LogicalKeyboardKey.arrowDown);
        await _key(tester, LogicalKeyboardKey.enter);
        expect(sent, isEmpty);
        expect(reasonField(), findsOneWidget);
        expect(find.text('Tell why'), findsOneWidget);
        await tester.enterText(reasonField(), 'too risky');
        await _key(tester, LogicalKeyboardKey.enter);
        expect(
          sent.single,
          optionRespond(
            sessionId: 'mac:%1',
            kind: 'permission',
            value: 'deny',
            reason: 'too risky',
            requestId: 'd1',
          ),
        );
      },
    );

    testWidgets('Enter in the reason field sends the reject option picked', (
      tester,
    ) async {
      final sent = await pumpDecision(
        tester,
        dix(
          options: [
            ...threeOptions,
            {'label': 'Never', 'value': 'never', 'reject': true},
          ],
        ),
      );
      await _key(tester, LogicalKeyboardKey.arrowDown);
      await _key(tester, LogicalKeyboardKey.arrowDown);
      await _key(tester, LogicalKeyboardKey.arrowDown);
      await _key(tester, LogicalKeyboardKey.enter);
      await tester.enterText(reasonField(), 'nope');
      await _key(tester, LogicalKeyboardKey.enter);
      expect(sent.single['option_value'], 'never');
      expect(sent.single['reason'], 'nope');
    });

    testWidgets('a shorter option list under the same request id does not throw', (
      tester,
    ) async {
      final sent = <Map<String, dynamic>>[];
      Widget host(Session s) => MaterialApp(
        home: Scaffold(
          body: DesktopDecision(
            session: s,
            interaction: s.interaction!,
            kind: 'permission',
            detail: const [],
            busy: false,
            onRespond: sent.add,
          ),
        ),
      );
      debugDefaultTargetPlatformOverride = TargetPlatform.macOS;
      addTearDown(() => debugDefaultTargetPlatformOverride = null);
      await tester.pumpWidget(host(_session(dix(options: threeOptions))));
      await tester.pumpAndSettle();
      await _key(tester, LogicalKeyboardKey.arrowDown);
      await _key(tester, LogicalKeyboardKey.arrowDown);
      await tester.pumpWidget(
        host(
          _session(
            dix(
              options: [
                {'label': 'Yes', 'value': 'allow'},
                {'label': 'Later', 'value': 'later'},
              ],
            ),
          ),
        ),
      );
      expect(tester.takeException(), isNull);
      await _key(tester, LogicalKeyboardKey.enter);
      expect(sent.single['option_value'], 'later');
    });

    testWidgets('Esc closes the reason field', (tester) async {
      await pumpDecision(tester, dix());
      expect(await tester.sendKeyEvent(LogicalKeyboardKey.escape), isFalse);
      await _key(tester, LogicalKeyboardKey.arrowDown);
      await _key(tester, LogicalKeyboardKey.enter);
      expect(reasonField(), findsOneWidget);
      expect(find.text('Reason (optional)'), findsOneWidget);
      expect(await tester.sendKeyEvent(LogicalKeyboardKey.escape), isTrue);
      await tester.pumpAndSettle();
      expect(reasonField(), findsNothing);
      expect(await tester.sendKeyEvent(LogicalKeyboardKey.escape), isFalse);
    });

    testWidgets('clicking an option sends it', (tester) async {
      final sent = await pumpDecision(tester, dix(options: threeOptions));
      await tester.tap(find.text('Yes, always'));
      await tester.pumpAndSettle();
      expect(sent.single['option_value'], 'always');
    });

    testWidgets('the plan text and tool detail still render above the list', (
      tester,
    ) async {
      await pumpDecision(
        tester,
        dix(
          kind: 'plan',
          plan: 'Step one of the plan',
          toolInput: '{"command":"ls -la"}',
          options: threeOptions,
        ),
        kind: 'plan',
      );
      expect(find.text('Plan review'), findsOneWidget);
      expect(find.text('Needs approval', findRichText: true), findsOneWidget);
      expect(find.textContaining('ls -la', findRichText: true), findsWidgets);
      final plan = find.text('Step one of the plan', findRichText: true);
      expect(plan, findsOneWidget);
      expect(
        tester.getBottomLeft(plan).dy,
        lessThan(tester.getTopLeft(find.text('Yes')).dy),
      );
    });

    testWidgets('keys are ignored while busy', (tester) async {
      debugDefaultTargetPlatformOverride = TargetPlatform.macOS;
      addTearDown(() => debugDefaultTargetPlatformOverride = null);
      final sent = <Map<String, dynamic>>[];
      final s = _session(dix());
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: DesktopDecision(
              session: s,
              interaction: s.interaction!,
              kind: 'permission',
              detail: const [],
              busy: true,
              onRespond: sent.add,
            ),
          ),
        ),
      );
      await tester.pump(); // the busy progress bar never settles
      expect(await tester.sendKeyEvent(LogicalKeyboardKey.enter), isFalse);
      expect(sent, isEmpty);
    });
  });
}
