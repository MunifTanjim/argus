import 'dart:convert';

import 'package:argus/data/session_repository.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/grouping.dart';
import 'package:argus/ui/blocked_card.dart';
import 'package:argus/ui/session_card.dart';
import 'package:argus/ui/session_detail_screen.dart';
import 'package:argus/ui/session_sections_list.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../support/fake_session_repository.dart';

class _Repo extends FakeSessionRepository {}

class _Pushes extends NavigatorObserver {
  final ids = <String?>[];
  @override
  void didPush(Route<dynamic> route, Route<dynamic>? previous) =>
      ids.add(sessionDetailRouteId(route));
}

Session _s(Map<String, dynamic>? interaction, {String id = 'mac:%1'}) =>
    Session.fromJson(
      jsonDecode(
            jsonEncode({
              'id': id,
              'agent': 'claude',
              'status': interaction == null ? 'working' : 'awaiting_input',
              'source': 'hooked',
              'tmux': {
                'server': 'argus',
                'pane_id': '%1',
                'session_name': 's',
                'window_index': 0,
                'current_path': '/p',
              },
              'repo': 'argus',
              'interaction': ?interaction,
            }),
          )
          as Map<String, dynamic>,
    );

Map<String, dynamic> _opt(String label, String value, {bool reject = false}) =>
    {'label': label, 'value': value, 'reject': reject};

final _permission = {
  'kind': 'permission',
  'tool_name': 'Bash',
  'tool_input': '{"command":"flutter test"}',
  'message': 'Claude wants to run a command',
  'request_id': 'r1',
  'options': [
    _opt('Allow', 'allow'),
    _opt('Always', 'always'),
    _opt('Deny', 'deny', reject: true),
  ],
};

Future<_Pushes> _pump(
  WidgetTester tester,
  Widget child,
  SessionRepository repo,
) async {
  final pushes = _Pushes();
  await tester.pumpWidget(
    ProviderScope(
      overrides: [sessionRepositoryProvider.overrideWithValue(repo)],
      child: MaterialApp(
        navigatorObservers: [pushes],
        home: Scaffold(body: child),
      ),
    ),
  );
  pushes.ids.clear();
  return pushes;
}

Map<String, dynamic> _question(String text, List<String> options) => {
      'question': text,
      'options': options,
    };

Finder _inKindLine(String label) => find.textContaining(label);

void main() {
  group('kind line', () {
    testWidgets('a permission names the tool', (tester) async {
      await _pump(tester, BlockedCard(session: _s(_permission)), _Repo());
      expect(_inKindLine('Permission: Bash'), findsOneWidget);
      expect(find.byIcon(Icons.shield_outlined), findsOneWidget);
      expect(find.text('flutter test'), findsOneWidget);
    });

    testWidgets('a question shows its text and up to 3 options', (tester) async {
      await _pump(
        tester,
        BlockedCard(
          session: _s({
            'kind': 'question',
            'questions': [
              _question('Which database?', ['Postgres', 'SQLite', 'MySQL', 'Mongo', 'Redis']),
            ],
          }),
        ),
        _Repo(),
      );
      expect(_inKindLine('Question'), findsOneWidget);
      expect(find.textContaining('1 of'), findsNothing);
      expect(find.byIcon(Icons.help_outline), findsOneWidget);
      expect(find.text('Which database?'), findsOneWidget);
      expect(find.text('Postgres'), findsOneWidget);
      expect(find.text('MySQL'), findsOneWidget);
      expect(find.text('Mongo'), findsNothing);
      expect(find.text('+2'), findsOneWidget);
    });

    testWidgets('more than one question counts them', (tester) async {
      await _pump(
        tester,
        BlockedCard(
          session: _s({
            'kind': 'question',
            'questions': [
              _question('First?', ['A', 'B']),
              _question('Second?', ['C']),
            ],
          }),
        ),
        _Repo(),
      );
      expect(_inKindLine('Question · 1 of 2'), findsOneWidget);
      expect(find.text('First?'), findsOneWidget);
      expect(find.text('Second?'), findsNothing);
    });

    testWidgets('a plan shows its first 3 non-blank lines', (tester) async {
      await _pump(
        tester,
        BlockedCard(
          session: _s({
            'kind': 'plan',
            'plan': '## Migrate\n\n1. Add columns\n\n2. Backfill\n3. Drop old',
          }),
        ),
        _Repo(),
      );
      expect(_inKindLine('Plan review'), findsOneWidget);
      expect(find.byIcon(Icons.description_outlined), findsOneWidget);
      expect(find.text('## Migrate\n1. Add columns\n2. Backfill'), findsOneWidget);
      expect(find.textContaining('Drop old'), findsNothing);
    });

    testWidgets('a question keeps its message above the preview', (tester) async {
      await _pump(
        tester,
        BlockedCard(
          session: _s({
            'kind': 'question',
            'message': 'Claude has a question',
            'questions': [_question('Which one?', ['A'])],
          }),
        ),
        _Repo(),
      );
      expect(
        tester.getTopLeft(find.text('Claude has a question')).dy,
        lessThan(tester.getTopLeft(find.text('Which one?')).dy),
      );
    });

    testWidgets('a tap on an option chip opens the session', (tester) async {
      final pushes = await _pump(
        tester,
        BlockedCard(
          session: _s({
            'kind': 'question',
            'questions': [_question('Which one?', ['Alpha'])],
          }),
        ),
        _Repo(),
      );
      await tester.tap(find.text('Alpha'));
      await tester.pump();
      expect(pushes.ids, ['mac:%1']);
    });
  });

  testWidgets('a permission card shows the command and no buttons', (
    tester,
  ) async {
    await _pump(tester, BlockedCard(session: _s(_permission)), _Repo());
    expect(find.byType(FilledButton), findsNothing);
    expect(find.byType(OutlinedButton), findsNothing);
    expect(find.text('flutter test'), findsOneWidget);
  });

  testWidgets('a tap on the command preview opens the session', (
    tester,
  ) async {
    final pushes = await _pump(
      tester,
      BlockedCard(session: _s(_permission)),
      _Repo(),
    );
    await tester.tap(find.text('flutter test'));
    await tester.pump();
    expect(pushes.ids, ['mac:%1']);
  });

  testWidgets('a tap on the card body opens the session', (tester) async {
    final pushes = await _pump(
      tester,
      BlockedCard(session: _s(_permission)),
      _Repo(),
    );
    await tester.tap(find.text('Claude wants to run a command'));
    await tester.pump();
    expect(pushes.ids, ['mac:%1']);
  });

  testWidgets('blocked session renders as BlockedCard in every grouping', (
    tester,
  ) async {
    final blocked = _s(_permission);
    final other = _s(null, id: 'mac:%2');
    for (final by in GroupBy.values) {
      await _pump(
        tester,
        SessionSectionsList(sessions: [blocked, other], groupBy: by),
        _Repo(),
      );
      expect(find.byType(BlockedCard), findsOneWidget, reason: '$by');
      expect(find.byType(SessionCard), findsOneWidget, reason: '$by');
    }
  });

  testWidgets('a long prompt message is cut to 4 lines', (tester) async {
    final message = List.filled(20, 'line').join('\n');
    await _pump(
      tester,
      BlockedCard(session: _s({..._permission, 'message': message})),
      _Repo(),
    );
    final text = tester.widget<Text>(find.text(message));
    expect(text.maxLines, 4);
    expect(text.overflow, TextOverflow.ellipsis);
  });
}
