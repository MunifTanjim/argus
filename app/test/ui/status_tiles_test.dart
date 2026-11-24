import 'dart:convert';

import 'package:argus/models/session.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/grouping.dart';
import 'package:argus/state/status_filter.dart';
import 'package:argus/ui/blocked_card.dart';
import 'package:argus/ui/session_card.dart';
import 'package:argus/ui/session_sections_list.dart';
import 'package:argus/ui/status_tiles.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

Session _s(String id, String status, {bool prompt = false}) =>
    Session.fromJson(jsonDecode(jsonEncode({
      'id': id,
      'agent': 'claude',
      'status': status,
      'source': 'hooked',
      'tmux': {
        'server': 'argus',
        'pane_id': '%1',
        'session_name': 's',
        'window_index': 0,
        'current_path': '/p',
      },
      if (prompt) 'interaction': {'kind': 'permission', 'tool_name': 'Bash'},
    })) as Map<String, dynamic>);

final _blocked = _s('a', 'awaiting_input', prompt: true);
final _working = _s('c', 'working');
final _idle = _s('e', 'idle');

Widget _app(List<Session> sessions, {GroupBy by = GroupBy.host, bool filter = true}) =>
    ProviderScope(
      overrides: [sessionsLoadingProvider.overrideWith((ref) => false)],
      child: MaterialApp(
        home: Scaffold(
          body: Column(
            children: [
              StatusTiles(sessions: sessions),
              Expanded(
                child: SessionSectionsList(
                  sessions: sessions,
                  groupBy: by,
                  applyStatusFilter: filter,
                ),
              ),
            ],
          ),
        ),
      ),
    );

// A blocked session renders as a BlockedCard, the rest as SessionCards.
final _cards =
    find.byWidgetPredicate((w) => w is SessionCard || w is BlockedCard);

ProviderContainer _container(WidgetTester tester) =>
    ProviderScope.containerOf(tester.element(find.byType(StatusTiles)));

void main() {
  testWidgets('tapping a tile filters the list and tapping again clears it',
      (tester) async {
    await tester.pumpWidget(_app([_blocked, _working, _idle]));
    expect(_cards, findsNWidgets(3));

    await tester.tap(find.byKey(const Key('tile-working')));
    await tester.pump();
    expect(_cards, findsOneWidget);
    expect(_container(tester).read(statusFilterProvider), TileStatus.working);

    await tester.tap(find.byKey(const Key('tile-working')));
    await tester.pump();
    expect(_cards, findsNWidgets(3));
  });

  testWidgets('a zero tile ignores taps', (tester) async {
    await tester.pumpWidget(_app([_working, _idle]));
    await tester.tap(find.byKey(const Key('tile-blocked')));
    await tester.pump();
    expect(_container(tester).read(statusFilterProvider), isNull);
    expect(_cards, findsNWidgets(2));
  });

  testWidgets('filter clears when its count drops to zero', (tester) async {
    await tester.pumpWidget(_app([_blocked, _working, _idle]));
    await tester.tap(find.byKey(const Key('tile-blocked')));
    await tester.pump();
    expect(_cards, findsOneWidget);

    await tester.pumpWidget(_app([_s('a', 'working'), _working, _idle]));
    await tester.pump();
    expect(_container(tester).read(statusFilterProvider), isNull);
    expect(_cards, findsNWidgets(3));
  });

  testWidgets('a list without applyStatusFilter ignores the filter',
      (tester) async {
    await tester.pumpWidget(_app([_blocked, _working, _idle], filter: false));
    await tester.tap(find.byKey(const Key('tile-working')));
    await tester.pump();
    expect(_cards, findsNWidgets(3));
  });

  for (final by in [GroupBy.host, GroupBy.status]) {
    testWidgets('tiles show in ${by.name} grouping', (tester) async {
      await tester.pumpWidget(_app([_blocked, _working, _idle], by: by));
      for (final k in ['blocked', 'waiting', 'working']) {
        expect(find.byKey(Key('tile-$k')), findsOneWidget);
      }
      expect(
          find.descendant(
              of: find.byKey(const Key('tile-blocked')),
              matching: find.text('BLOCKED')),
          findsOneWidget);
    });
  }
}
