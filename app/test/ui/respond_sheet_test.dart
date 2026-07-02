import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/core/result.dart';
import 'package:argus/data/session_repository.dart';
import 'package:argus/models/session.dart';
import 'package:argus/ui/respond_sheet.dart';
import '../support/fake_session_repository.dart';

class _RecordingControl extends FakeSessionRepository {
  final respondCalls = <Map<String, dynamic>>[];
  final inputCalls = <List<String>>[];
  @override
  Future<Result<void>> respond(Map<String, dynamic> params) async {
    respondCalls.add(params);
    return const Result.ok(null);
  }

  @override
  Future<Result<void>> sendInput(String sessionId, String text) async {
    inputCalls.add([sessionId, text]);
    return const Result.ok(null);
  }
}

Session _session(Map<String, dynamic> interaction) =>
    Session.fromJson(jsonDecode(jsonEncode({
      'id': 'mac:%1',
      'tool': 't',
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

Future<void> _pumpSheet(
    WidgetTester tester, Session s, SessionRepository c) async {
  await tester.pumpWidget(ProviderScope(
    overrides: [sessionRepositoryProvider.overrideWithValue(c)],
    child: MaterialApp(home: Scaffold(body: RespondSheet(session: s))),
  ));
  await tester.pump();
}

void main() {
  testWidgets('permission allow sends option_value allow', (tester) async {
    final c = _RecordingControl();
    await _pumpSheet(
        tester, _session({'kind': 'permission', 'tool_name': 'Bash'}), c);
    await tester.tap(find.text('Allow'));
    await tester.pumpAndSettle();
    expect(c.respondCalls.single['option_value'], 'allow');
    expect(c.respondCalls.single['kind'], 'permission');
  });

  testWidgets('permission renders formatted tool detail, not raw JSON',
      (tester) async {
    final c = _RecordingControl();
    await _pumpSheet(
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
    await _pumpSheet(
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
    await _pumpSheet(tester, _session({'kind': 'plan', 'plan': '# Plan'}), c);
    await tester.tap(find.text('Allow'));
    await tester.pumpAndSettle();
    expect(c.respondCalls.single['kind'], 'plan');
    expect(c.respondCalls.single['option_value'], 'allow');
  });

  testWidgets('long plan scrolls without overflow', (tester) async {
    final c = _RecordingControl();
    final longPlan = List.generate(200, (i) => 'Step $i: do the thing').join('\n\n');
    await _pumpSheet(tester, _session({'kind': 'plan', 'plan': longPlan}), c);
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
    await _pumpSheet(tester, s, c);
    expect(find.text('Allow'), findsOneWidget);
    expect(find.text('Deny'), findsOneWidget);
    await tester.tap(find.text('Allow'));
    await tester.pumpAndSettle();
    expect(c.respondCalls.single['option_value'], 'allow');
    expect(c.respondCalls.single['kind'], 'permission');
  });

  testWidgets('idle reply sends input', (tester) async {
    final c = _RecordingControl();
    await _pumpSheet(tester, _session({'kind': 'idle'}), c);
    await tester.enterText(find.byType(TextField), 'next task');
    await tester.tap(find.text('Send'));
    await tester.pumpAndSettle();
    expect(c.inputCalls.single, ['mac:%1', 'next task']);
  });

  testWidgets('permission deny with blank reason omits reason key',
      (tester) async {
    final c = _RecordingControl();
    await _pumpSheet(
        tester, _session({'kind': 'permission', 'tool_name': 'Bash'}), c);
    await tester.tap(find.text('Deny'));
    await tester.pump();
    // leave the reason TextField empty
    await tester.tap(find.text('Send'));
    await tester.pumpAndSettle();
    expect(c.respondCalls.single['option_value'], 'deny');
    expect(c.respondCalls.single.containsKey('reason'), isFalse);
  });

  testWidgets('idle reply with blank text does not send', (tester) async {
    final c = _RecordingControl();
    await _pumpSheet(tester, _session({'kind': 'idle'}), c);
    // do not enter any text, tap Send immediately
    await tester.tap(find.text('Send'));
    await tester.pumpAndSettle();
    expect(c.inputCalls, isEmpty);
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

    testWidgets('single-select submits chosen label', (tester) async {
      final c = _RecordingControl();
      await _pumpSheet(tester, questionSession(), c);
      await tester.tap(find.text('B'));
      await tester.pump();
      await tester.tap(find.text('Submit'));
      await tester.pumpAndSettle();
      expect(c.respondCalls.single['answers'], {'Pick one': 'B'});
    });

    testWidgets('submit disabled until an answer exists', (tester) async {
      final c = _RecordingControl();
      await _pumpSheet(tester, questionSession(), c);
      await tester.tap(find.text('Submit'));
      await tester.pumpAndSettle();
      expect(c.respondCalls, isEmpty);
    });

    testWidgets('multi-select collects toggled labels', (tester) async {
      final c = _RecordingControl();
      await _pumpSheet(tester, questionSession(multi: true), c);
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
      await _pumpSheet(
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
}
