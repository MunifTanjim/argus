import 'dart:convert';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/core/result.dart';
import 'package:argus/data/session_repository.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/respond_params.dart';
import 'package:argus/ui/desktop_interaction.dart';
import 'package:argus/ui/interaction_panel.dart';
import 'package:argus/ui/theme.dart';
import '../support/fake_session_repository.dart';

class _RecordingControl extends FakeSessionRepository {
  final respondCalls = <Map<String, dynamic>>[];
  @override
  Future<Result<void>> respond(Map<String, dynamic> params) async {
    respondCalls.add(params);
    return const Result.ok(null);
  }
}

Session _session(Map<String, dynamic> interaction, {String agent = 'claude'}) =>
    Session.fromJson(jsonDecode(jsonEncode({
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
    })) as Map<String, dynamic>);

Future<void> _pumpPanel(
    WidgetTester tester, Session s, SessionRepository c) async {
  await tester.pumpWidget(ProviderScope(
    overrides: [sessionRepositoryProvider.overrideWithValue(c)],
    child: MaterialApp(
        theme: buildArgusTheme(),
        home: Scaffold(
            body: SizedBox(height: 600, child: InteractionPanel(session: s)))),
  ));
  await tester.pump();
}

void main() {
  testWidgets('permission allow sends option_value allow', (tester) async {
    final c = _RecordingControl();
    await _pumpPanel(
        tester, _session({'kind': 'permission', 'tool_name': 'Bash'}), c);
    await tester.tap(find.text('Allow'));
    await tester.pumpAndSettle();
    expect(c.respondCalls.single['option_value'], 'allow');
    expect(c.respondCalls.single['kind'], 'permission');
  });

  testWidgets('permission heading uses the tool display name',
      (tester) async {
    final c = _RecordingControl();
    await _pumpPanel(
        tester,
        _session({'kind': 'permission', 'tool_name': 'exec_command'},
            agent: 'codex'),
        c);
    expect(find.text('Permission requested · Exec Command'), findsOneWidget);
  });

  testWidgets('permission renders formatted tool detail, not raw JSON',
      (tester) async {
    final c = _RecordingControl();
    await _pumpPanel(
        tester,
        _session({
          'kind': 'permission',
          'tool_name': 'Bash',
          'tool_input': '{"command":"ls -la","description":"list files"}',
        }),
        c);
    await tester.pump();
    // The per-tool renderer formats the command as a bash code block instead of
    // dumping the JSON.
    expect(find.textContaining('ls -la'), findsOneWidget);
    expect(find.textContaining('list files'), findsOneWidget);
    expect(find.textContaining('"command"'), findsNothing);
  });

  testWidgets('permission deny reveals reason and sends option_value deny',
      (tester) async {
    final c = _RecordingControl();
    await _pumpPanel(
        tester, _session({'kind': 'permission', 'tool_name': 'Bash'}), c);
    await tester.tap(find.text('Deny'));
    await tester.pump();
    await tester.enterText(find.byType(TextField), 'too risky');
    await tester.tap(find.text('Send'));
    await tester.pumpAndSettle();
    expect(c.respondCalls.single['option_value'], 'deny');
    expect(c.respondCalls.single['reason'], 'too risky');
  });

  testWidgets('plan approve sends option_value allow with kind plan',
      (tester) async {
    final c = _RecordingControl();
    await _pumpPanel(tester, _session({'kind': 'plan', 'plan': '# Plan'}), c);
    await tester.tap(find.text('Allow'));
    await tester.pumpAndSettle();
    expect(c.respondCalls.single['kind'], 'plan');
    expect(c.respondCalls.single['option_value'], 'allow');
  });

  testWidgets('long plan scrolls without overflow', (tester) async {
    final c = _RecordingControl();
    final longPlan = List.generate(200, (i) => 'Step $i: do the thing').join('\n\n');
    await _pumpPanel(tester, _session({'kind': 'plan', 'plan': longPlan}), c);
    await tester.pumpAndSettle();
    // No RenderFlex overflow exception, the detail is scrollable, and the
    // action button stays rendered (pinned below the scroll area).
    expect(tester.takeException(), isNull);
    expect(find.byType(SingleChildScrollView), findsOneWidget);
    expect(find.text('Allow'), findsOneWidget);
  });

  testWidgets('permission renders server option buttons', (tester) async {
    final c = _RecordingControl();
    final s = _session({
      'kind': 'permission',
      'tool_name': 'Bash',
      'options': [
        {'label': 'Allow', 'value': 'allow'},
        {'label': 'Deny', 'value': 'deny', 'reject': true},
      ],
    });
    await _pumpPanel(tester, s, c);
    expect(find.text('Allow'), findsOneWidget);
    expect(find.text('Deny'), findsOneWidget);
    await tester.tap(find.text('Allow'));
    await tester.pumpAndSettle();
    expect(c.respondCalls.single['option_value'], 'allow');
    expect(c.respondCalls.single['kind'], 'permission');
  });

  testWidgets('a decision stays inline after it is sent', (tester) async {
    final c = _RecordingControl();
    await _pumpPanel(
        tester, _session({'kind': 'permission', 'tool_name': 'Bash'}), c);
    await tester.tap(find.text('Allow'));
    await tester.pumpAndSettle();
    expect(c.respondCalls, hasLength(1));
    expect(find.byType(InteractionPanel), findsOneWidget); // no route was popped
  });

  testWidgets('permission deny with blank reason omits reason key',
      (tester) async {
    final c = _RecordingControl();
    await _pumpPanel(
        tester, _session({'kind': 'permission', 'tool_name': 'Bash'}), c);
    await tester.tap(find.text('Deny'));
    await tester.pump();
    // leave the reason TextField empty
    await tester.tap(find.text('Send'));
    await tester.pumpAndSettle();
    expect(c.respondCalls.single['option_value'], 'deny');
    expect(c.respondCalls.single.containsKey('reason'), isFalse);
  });

  group('question', () {
    Session questionSession({bool multi = false}) => _session({
          'kind': 'question',
          'questions': [
            {
              'question': 'Pick one',
              'multi_select': multi,
              'options': ['A', 'B'],
            }
          ],
        });

    Session notesSession() => _session({
          'kind': 'question',
          'questions': [
            {
              'question': 'Pick a size',
              'allow_notes': true,
              'options': ['Small', 'Large', 'None of the above'],
            }
          ],
        });

    testWidgets('allow_notes hides type-your-own and sends a note',
        (tester) async {
      final c = _RecordingControl();
      await _pumpPanel(tester, notesSession(), c);
      expect(find.text(otherLabel), findsNothing);
      await tester.tap(find.text('Large'));
      await tester.pump();
      await tester.tap(find.text('Add note'));
      await tester.pump();
      await tester.enterText(
          find.widgetWithText(TextField, 'Note'), 'extra large');
      await tester.pump();
      await tester.tap(find.text('Submit'));
      await tester.pumpAndSettle();
      expect(c.respondCalls.single['answers'], {'Pick a size': 'Large'});
      expect(c.respondCalls.single['notes'], {'Pick a size': 'extra large'});
    });

    testWidgets('allow_notes question shows no custom answer field',
        (tester) async {
      final c = _RecordingControl();
      await _pumpPanel(tester, notesSession(), c);
      expect(find.widgetWithText(TextField, 'Your answer'), findsNothing);
    });

    testWidgets('clearing the note removes it', (tester) async {
      final c = _RecordingControl();
      await _pumpPanel(tester, notesSession(), c);
      await tester.tap(find.text('Small'));
      await tester.tap(find.text('Add note'));
      await tester.pump();
      await tester.enterText(find.widgetWithText(TextField, 'Note'), 'x');
      await tester.tap(find.byTooltip('Remove note'));
      await tester.pump();
      expect(find.widgetWithText(TextField, 'Note'), findsNothing);
      await tester.tap(find.text('Submit'));
      await tester.pumpAndSettle();
      expect(c.respondCalls.single.containsKey('notes'), isFalse);
    });

    testWidgets('questions without allow_notes show no note button',
        (tester) async {
      final c = _RecordingControl();
      await _pumpPanel(tester, questionSession(), c);
      expect(find.text('Add note'), findsNothing);
      expect(find.text(otherLabel), findsOneWidget);
    });

    testWidgets('cancel_interrupts questions offer interrupt instead of chat', (tester) async {
      final c = _RecordingControl();
      await _pumpPanel(tester, questionSession(), c);
      expect(find.text('Chat about this'), findsOneWidget);
      await _pumpPanel(
          tester,
          _session({
            'kind': 'question',
            'cancel_interrupts': true,
            'questions': [
              {
                'question': 'Pick one',
                'options': ['A', 'B'],
                'allow_notes': true,
              }
            ],
          }),
          c);
      expect(find.text('Chat about this'), findsNothing);
      await tester.tap(find.text('Interrupt'));
      await tester.pumpAndSettle();
      expect(c.respondCalls.single, {
        'session_id': 'mac:%1',
        'kind': 'question',
        'question_action': 'cancel',
      });
    });

    testWidgets('allow_unanswered submits after confirmation', (tester) async {
      final c = _RecordingControl();
      await _pumpPanel(
          tester,
          _session({
            'kind': 'question',
            'allow_unanswered': true,
            'questions': [
              {
                'question': 'Pick one',
                'options': ['A', 'B'],
              }
            ],
          }),
          c);
      await tester.tap(find.text('Submit'));
      await tester.pumpAndSettle();
      expect(find.text('Submit with unanswered questions?'), findsOneWidget);
      expect(find.text('1 unanswered question'), findsOneWidget);
      await tester.tap(find.text('Go back'));
      await tester.pumpAndSettle();
      expect(c.respondCalls, isEmpty);
      await tester.tap(find.text('Submit'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Proceed'));
      await tester.pumpAndSettle();
      expect(c.respondCalls.single, {
        'session_id': 'mac:%1',
        'kind': 'question',
        'behavior': 'allow',
      });
    });

    testWidgets('responds echo the request_id', (tester) async {
      final c = _RecordingControl();
      await _pumpPanel(
          tester,
          _session({
            'kind': 'question',
            'request_id': '7',
            'cancel_interrupts': true,
            'questions': [
              {
                'question': 'Pick one',
                'options': ['A', 'B'],
              }
            ],
          }),
          c);
      await tester.tap(find.text('A'));
      await tester.pump();
      await tester.tap(find.text('Submit'));
      await tester.pumpAndSettle();
      expect(c.respondCalls.single['request_id'], '7');
    });

    testWidgets('partial answers submit without confirmation unless allowed',
        (tester) async {
      final c = _RecordingControl();
      await _pumpPanel(
          tester,
          _session({
            'kind': 'question',
            'questions': [
              {
                'question': 'Pick one',
                'options': ['A', 'B'],
              },
              {
                'question': 'Pick two',
                'options': ['C', 'D'],
              }
            ],
          }),
          c);
      await tester.tap(find.text('A'));
      await tester.pump();
      await tester.tap(find.text('Submit'));
      await tester.pumpAndSettle();
      expect(find.text('Submit with unanswered questions?'), findsNothing);
      expect(c.respondCalls.single['answers'], {'Pick one': 'A'});
    });

    testWidgets('single-select submits chosen label', (tester) async {
      final c = _RecordingControl();
      await _pumpPanel(tester, questionSession(), c);
      await tester.tap(find.text('B'));
      await tester.pump();
      await tester.tap(find.text('Submit'));
      await tester.pumpAndSettle();
      expect(c.respondCalls.single['answers'], {'Pick one': 'B'});
    });

    testWidgets('submit disabled until an answer exists', (tester) async {
      final c = _RecordingControl();
      await _pumpPanel(tester, questionSession(), c);
      await tester.tap(find.text('Submit'));
      await tester.pumpAndSettle();
      expect(c.respondCalls, isEmpty);
    });

    testWidgets('multi-select collects toggled labels', (tester) async {
      final c = _RecordingControl();
      await _pumpPanel(tester, questionSession(multi: true), c);
      await tester.tap(find.text('A'));
      await tester.tap(find.text('B'));
      await tester.pump();
      await tester.tap(find.text('Submit'));
      await tester.pumpAndSettle();
      expect(c.respondCalls.single['answers'], {
        'Pick one': ['A', 'B']
      });
    });

    testWidgets('single-select renders descriptions and previews',
        (tester) async {
      final c = _RecordingControl();
      await _pumpPanel(
          tester,
          _session({
            'kind': 'question',
            'questions': [
              {
                'question': 'Pick one',
                'options': ['A', 'B'],
                'option_descriptions': ['does A', 'does B'],
                'option_previews': ['preview-alpha', 'preview-bravo'],
              }
            ],
          }),
          c);
      expect(find.text('does A'), findsOneWidget);
      expect(find.text('does B'), findsOneWidget);
      expect(find.textContaining('preview-alpha'), findsOneWidget);
      expect(find.textContaining('preview-bravo'), findsOneWidget);
    });
  });

  group('desktop keyboard panel', () {
    final question = {
      'kind': 'question',
      'request_id': 'q1',
      'questions': [
        {
          'header': 'Pick',
          'question': 'Which one?',
          'options': ['a', 'b'],
        },
      ],
    };

    void platform(TargetPlatform p) {
      debugDefaultTargetPlatformOverride = p;
      addTearDown(() => debugDefaultTargetPlatformOverride = null);
    }

    testWidgets('on Android the panel renders the current cards and buttons', (
      tester,
    ) async {
      platform(TargetPlatform.android);
      await _pumpPanel(tester, _session(question), _RecordingControl());
      expect(find.byType(DesktopQuestions), findsNothing);
      expect(find.text('Submit'), findsOneWidget);
      await _pumpPanel(
        tester,
        _session({'kind': 'permission', 'request_id': 'p1'}),
        _RecordingControl(),
      );
      expect(find.byType(DesktopDecision), findsNothing);
      expect(find.byType(FilledButton), findsOneWidget);
    });

    testWidgets('on macOS the panel renders DesktopQuestions', (tester) async {
      platform(TargetPlatform.macOS);
      await _pumpPanel(tester, _session(question), _RecordingControl());
      expect(find.byType(DesktopQuestions), findsOneWidget);
      expect(find.text('Submit'), findsNothing);
    });

    testWidgets('on macOS the panel renders DesktopDecision', (tester) async {
      platform(TargetPlatform.macOS);
      await _pumpPanel(
        tester,
        _session({'kind': 'permission', 'request_id': 'p1'}),
        _RecordingControl(),
      );
      expect(find.byType(DesktopDecision), findsOneWidget);
      expect(find.byType(FilledButton), findsNothing);
    });

    testWidgets(
      'on macOS the question panel has focus after it appears; j moves the highlight without typing',
      (tester) async {
        platform(TargetPlatform.macOS);
        final c = _RecordingControl();
        await _pumpPanel(tester, _session(question), c);
        await tester.pump();
        expect(FocusManager.instance.primaryFocus?.debugLabel,
            'DesktopQuestions');
        await tester.sendKeyEvent(LogicalKeyboardKey.keyJ);
        await tester.pump();
        expect(find.byType(TextField), findsNothing);
        await tester.sendKeyEvent(LogicalKeyboardKey.enter);
        await tester.pumpAndSettle();
        expect(c.respondCalls.single['answers'], {'Which one?': 'b'});
      },
    );

    testWidgets('on macOS a permission is answered from the keyboard', (
      tester,
    ) async {
      platform(TargetPlatform.macOS);
      final c = _RecordingControl();
      await _pumpPanel(
        tester,
        _session({'kind': 'permission', 'request_id': 'p1'}),
        c,
      );
      await tester.sendKeyEvent(LogicalKeyboardKey.enter);
      await tester.pumpAndSettle();
      expect(c.respondCalls.single['option_value'], 'allow');
    });

    final longText = List.generate(80, (i) => 'line $i').join('\n\n');

    testWidgets('a long question on macOS stays inside the panel bounds', (
      tester,
    ) async {
      platform(TargetPlatform.macOS);
      await _pumpPanel(
        tester,
        _session({
          ...question,
          'questions': [
            {
              'header': 'Pick',
              'question': longText,
              'options': ['a', 'b'],
            },
            {
              'header': 'Two',
              'question': 'second',
              'options': ['c'],
            },
          ],
        }),
        _RecordingControl(),
      );
      expect(tester.takeException(), isNull);
      expect(tester.getRect(find.byType(InteractionPanel)).bottom,
          lessThanOrEqualTo(600));
      await tester.sendKeyEvent(LogicalKeyboardKey.arrowRight);
      await tester.sendKeyEvent(LogicalKeyboardKey.arrowRight);
      await tester.pumpAndSettle();
      expect(tester.takeException(), isNull);
      expect(find.text('Review answers'), findsOneWidget);
      expect(tester.getBottomLeft(find.text('Cancel')).dy,
          lessThanOrEqualTo(600));
    });

    testWidgets('a long plan on macOS keeps the options inside the panel', (
      tester,
    ) async {
      platform(TargetPlatform.macOS);
      await _pumpPanel(
        tester,
        _session({'kind': 'plan', 'request_id': 'p1', 'plan': longText}),
        _RecordingControl(),
      );
      expect(tester.takeException(), isNull);
      expect(tester.getBottomLeft(find.text('Deny')).dy, lessThanOrEqualTo(600));
    });

    testWidgets('moving the highlight keeps the option in view', (tester) async {
      platform(TargetPlatform.macOS);
      await _pumpPanel(
        tester,
        _session({
          ...question,
          'questions': [
            {
              'header': 'Pick',
              'question': 'Which one?',
              'options': [for (var i = 0; i < 40; i++) 'option $i'],
            },
          ],
        }),
        _RecordingControl(),
      );
      for (var i = 0; i < 20; i++) {
        await tester.sendKeyEvent(LogicalKeyboardKey.arrowDown);
      }
      await tester.pumpAndSettle();
      final row = tester.getRect(find.text('option 20'));
      final view = tester.getRect(find.byType(SingleChildScrollView));
      expect(row.top, greaterThanOrEqualTo(view.top));
      expect(row.bottom, lessThanOrEqualTo(view.bottom));
    });
  });
}
