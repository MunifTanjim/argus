import 'dart:convert';

import 'package:argus/models/session.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/grouping.dart';
import 'package:argus/state/session_filter.dart';
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
              StatusTiles(sessions: sessions, scope: homeStatusScope),
              Expanded(
                child: SessionSectionsList(
                  sessions: sessions,
                  groupBy: by,
                  statusScope: filter ? homeStatusScope : null,
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

Set<TileStatus> _home(WidgetTester tester) =>
    _container(tester).read(statusFilterProvider(homeStatusScope));

Future<void> _tap(WidgetTester tester, String key) async {
  await tester.tap(find.byKey(Key(key)));
  await tester.pump();
}

void main() {
  testWidgets('tapping a tile filters the list and tapping again clears it',
      (tester) async {
    await tester.pumpWidget(_app([_blocked, _working, _idle]));
    expect(_cards, findsNWidgets(3));

    await _tap(tester, 'tile-working');
    expect(_cards, findsOneWidget);
    expect(_home(tester), {TileStatus.working});

    await _tap(tester, 'tile-working');
    expect(_cards, findsNWidgets(3));
  });

  testWidgets('a tile tells a screen reader whether its filter is on', (
    tester,
  ) async {
    final handle = tester.ensureSemantics();
    await tester.pumpWidget(_app([_blocked, _working, _idle]));
    final tile = find.byKey(const Key('tile-working'));
    expect(
      tester.getSemantics(tile),
      isSemantics(isButton: true, isSelected: false, hasTapAction: true),
    );

    await _tap(tester, 'tile-working');
    expect(
      tester.getSemantics(tile),
      isSemantics(isButton: true, isSelected: true, hasTapAction: true),
    );
    handle.dispose();
  });

  testWidgets('two tiles show the union', (tester) async {
    await tester.pumpWidget(_app([_blocked, _working, _idle]));
    await _tap(tester, 'tile-blocked');
    await _tap(tester, 'tile-working');
    expect(_cards, findsNWidgets(2));
  });

  testWidgets('a zero tile can be selected', (tester) async {
    await tester.pumpWidget(_app([_working, _idle]));
    await _tap(tester, 'tile-blocked');
    expect(_home(tester), {TileStatus.blocked});
    expect(_cards, findsNothing);
    expect(find.text('No sessions match the filter.'), findsOneWidget);
  });

  testWidgets('a selected tile stays selected at zero and shows new sessions',
      (tester) async {
    await tester.pumpWidget(_app([_blocked, _working, _idle]));
    await _tap(tester, 'tile-blocked');

    await tester.pumpWidget(_app([_s('a', 'working'), _working, _idle]));
    await tester.pump();
    expect(_home(tester), {TileStatus.blocked});
    expect(_cards, findsNothing);

    await tester.pumpWidget(
        _app([_s('x', 'awaiting_input', prompt: true), _working, _idle]));
    await tester.pump();
    expect(_cards, findsOneWidget);
  });

  testWidgets('Show all clears only its own filter', (tester) async {
    await tester.pumpWidget(_app([_working, _idle]));
    _container(tester)
        .read(statusFilterProvider('other').notifier)
        .toggle(TileStatus.blocked);
    await _tap(tester, 'tile-blocked');

    await tester.tap(find.text('Show all'));
    await tester.pump();
    expect(_home(tester), isEmpty);
    expect(_cards, findsNWidgets(2));
    expect(_container(tester).read(statusFilterProvider('other')),
        {TileStatus.blocked});
  });

  testWidgets('search message wins over the filter message', (tester) async {
    await tester.pumpWidget(_app([_blocked, _working, _idle]));
    final search = _container(tester).read(sessionSearchProvider.notifier);
    search.open();
    search.set('zzz');
    await _tap(tester, 'tile-working');
    expect(find.text('No sessions match.'), findsOneWidget);
    expect(find.text('Show all'), findsNothing);
  });

  testWidgets('a list without a status scope ignores the filter',
      (tester) async {
    await tester.pumpWidget(_app([_blocked, _working, _idle], filter: false));
    await _tap(tester, 'tile-working');
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
