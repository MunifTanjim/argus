import 'dart:convert';

import 'package:argus/data/session_repository.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/appearance.dart';
import 'package:argus/state/grouping.dart';
import 'package:argus/ui/blocked_card.dart';
import 'package:argus/ui/session_card.dart';
import 'package:argus/ui/session_detail_screen.dart';
import 'package:argus/ui/session_sections_list.dart';
import 'package:argus/ui/theme.dart';
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

Session _s(
  Map<String, dynamic>? interaction, {
  String id = 'mac:%1',
  Map<String, dynamic> extra = const {},
}) =>
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
              ...extra,
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

    testWidgets('a question hides a message that repeats the label', (
      tester,
    ) async {
      await _pump(
        tester,
        BlockedCard(
          session: _s({
            'kind': 'question',
            'message': ' questions ',
            'questions': [_question('Which one?', ['A'])],
          }),
        ),
        _Repo(),
      );
      expect(find.textContaining('uestions'), findsNothing);
      expect(find.text('Which one?'), findsOneWidget);
    });

    testWidgets('a question hides a message that repeats its text', (
      tester,
    ) async {
      await _pump(
        tester,
        BlockedCard(
          session: _s({
            'kind': 'question',
            'message': 'Which one?',
            'questions': [_question('which one?', ['A'])],
          }),
        ),
        _Repo(),
      );
      expect(find.text('Which one?'), findsNothing);
      expect(find.text('which one?'), findsOneWidget);
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

  group('status style', () {
    Future<ArgusTokens> pumpStyle(
      WidgetTester tester,
      StatusStyle style,
    ) async {
      await tester.pumpWidget(
        ProviderScope(
          overrides: [sessionRepositoryProvider.overrideWithValue(_Repo())],
          child: MaterialApp(
            theme: buildArgusTheme(prefs: AppearancePrefs(statusStyle: style)),
            home: Scaffold(
              body: BlockedCard(
                session: _s({
                  'kind': 'question',
                  'request_id': 'r1',
                  'questions': [
                    _question('Which one?', ['A', 'B']),
                  ],
                }),
              ),
            ),
          ),
        ),
      );
      return tester.element(find.byType(BlockedCard)).tokens;
    }

    Material card(WidgetTester tester) => tester.widget<Material>(
      find
          .descendant(
            of: find.byType(BlockedCard),
            matching: find.byType(Material),
          )
          .first,
    );

    testWidgets('stripe: a plain card with a stripe in the blocked color', (
      tester,
    ) async {
      final t = await pumpStyle(tester, StatusStyle.stripe);
      final stripe = tester.widget<Container>(
        find.byKey(const Key('status-stripe')),
      );
      expect(stripe.color, t.blocked);
      expect(card(tester).color, t.surface);
    });

    testWidgets('dot: a plain card with the glyph and no stripe', (
      tester,
    ) async {
      final t = await pumpStyle(tester, StatusStyle.dot);
      expect(find.byKey(const Key('status-stripe')), findsNothing);
      expect(card(tester).color, t.surface);
    });

    testWidgets('filled: the tinted card', (tester) async {
      final t = await pumpStyle(tester, StatusStyle.filled);
      expect(find.byKey(const Key('status-stripe')), findsNothing);
      expect(card(tester).color, isNot(t.surface));
    });

    for (final style in StatusStyle.values) {
      testWidgets('$style: the mark is the waiting glyph', (tester) async {
        await pumpStyle(tester, style);
        expect(find.text('◆'), findsOneWidget);
        expect(find.text('●'), findsNothing);
      });
    }
  });


  testWidgets('the mark matches a waiting session card\'s mark', (
    tester,
  ) async {
    await _pump(
      tester,
      Column(
        children: [
          BlockedCard(session: _s(_permission)),
          SessionCard(session: _s(null, id: 'mac:%2')),
        ],
      ),
      _Repo(),
    );
    final marks = find.descendant(
      of: find.byType(StatusMark),
      matching: find.byType(Text),
    );
    expect(marks, findsNWidgets(2));
    expect(
      tester.getSize(marks.at(0)).height,
      tester.getSize(marks.at(1)).height,
    );
  });

  testWidgets('the node name is right of the time', (tester) async {
    await _pump(
      tester,
      BlockedCard(
        showNode: true,
        session: _s(
          _permission,
          extra: {
            'node_label': 'macbook',
            'status_since': DateTime.now()
                .subtract(const Duration(minutes: 5))
                .toUtc()
                .toIso8601String(),
          },
        ),
      ),
      _Repo(),
    );
    final node = tester.getRect(find.text('macbook'));
    final time = tester.getRect(find.text('5m ago'));
    expect(time.right, lessThan(node.left));
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
