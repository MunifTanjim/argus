import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_riverpod/misc.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/sessions.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/transport/connection.dart';
import 'package:argus/ui/session_list_screen.dart';

Session _s(String id, String host, String status) =>
    Session.fromJson(jsonDecode(
        '{"id":"$id","agent":"t","status":"$status","source":"hooked","tmux":{"server":"argus","pane_id":"%1","session_name":"s","window_index":0,"current_path":"/p"},"repo":"$id","node_label":"$host"}'));

Session _sa(String id, String host, String status, String agent) =>
    Session.fromJson(jsonDecode(
        '{"id":"$id","agent":"$agent","status":"$status","source":"hooked","tmux":{"server":"argus","pane_id":"%1","session_name":"s","window_index":0,"current_path":"/p"},"repo":"$id","node_label":"$host"}'));

Widget _app(List<Override> overrides) => ProviderScope(
      overrides: overrides,
      child: const MaterialApp(home: SessionListScreen()),
    );

void main() {
  testWidgets('shows needs-you header and host header', (tester) async {
    await tester.pumpWidget(_app([
      sessionsProvider.overrideWith(() => _SeededSessions([
            _s('dev:1', 'dev', 'awaiting_input'),
            _s('dev:2', 'dev', 'working'),
          ])),
      gatewayProvider.overrideWithValue(null),
    ]));
    await tester.pump();
    expect(find.text('▌ NEEDS YOU'), findsOneWidget);
    expect(find.textContaining('dev'), findsWidgets);
  });

  testWidgets('agent badges appear only when agents are mixed', (tester) async {
    await tester.pumpWidget(_app([
      sessionsProvider.overrideWith(() => _SeededSessions([
            _sa('dev:1', 'dev', 'working', 'claude'),
            _sa('dev:2', 'dev', 'working', 'codex'),
          ])),
      gatewayProvider.overrideWithValue(null),
    ]));
    await tester.pump();
    expect(find.text('CLAUDE'), findsOneWidget);
    expect(find.text('CODEX'), findsOneWidget);
  });

  testWidgets('no agent badges when all one agent', (tester) async {
    await tester.pumpWidget(_app([
      sessionsProvider.overrideWith(() => _SeededSessions([
            _sa('dev:1', 'dev', 'working', 'claude'),
            _sa('dev:2', 'dev', 'working', 'claude'),
          ])),
      gatewayProvider.overrideWithValue(null),
    ]));
    await tester.pump();
    expect(find.text('CLAUDE'), findsNothing);
  });

  testWidgets('empty state when no sessions', (tester) async {
    await tester.pumpWidget(_app([
      gatewayProvider.overrideWithValue(null),
    ]));
    await tester.pump();
    expect(find.textContaining('No sessions'), findsOneWidget);
  });

  testWidgets('filter toggles between all and active sessions', (tester) async {
    await tester.pumpWidget(_app([
      sessionsProvider.overrideWith(() => _SeededSessions([
            _s('dev:1', 'dev', 'awaiting_input'),
            _s('dev:2', 'dev', 'working'),
            _s('dev:3', 'dev', 'idle'),
          ])),
      gatewayProvider.overrideWithValue(null),
    ]));
    await tester.pump();
    expect(find.text('dev:3'), findsOneWidget);

    await tester.tap(find.byTooltip('Show active sessions'));
    await tester.pump();
    expect(find.text('dev:1'), findsOneWidget);
    expect(find.text('dev:2'), findsOneWidget);
    expect(find.text('dev:3'), findsNothing);

    await tester.tap(find.byTooltip('Show all sessions'));
    await tester.pump();
    expect(find.text('dev:3'), findsOneWidget);
  });

  testWidgets('filtered empty state names the filter', (tester) async {
    await tester.pumpWidget(_app([
      sessionsProvider.overrideWith(() => _SeededSessions([
            _s('dev:1', 'dev', 'idle'),
          ])),
      gatewayProvider.overrideWithValue(null),
    ]));
    await tester.pump();
    await tester.tap(find.byTooltip('Show active sessions'));
    await tester.pump();
    expect(find.text('No active sessions.'), findsOneWidget);
  });

  testWidgets('search narrows the list as you type', (tester) async {
    await tester.pumpWidget(_app([
      sessionsProvider.overrideWith(() => _SeededSessions([
            _s('dev:alpha', 'dev', 'idle'),
            _s('dev:beta', 'dev', 'idle'),
          ])),
      gatewayProvider.overrideWithValue(null),
    ]));
    await tester.pump();
    await tester.tap(find.byTooltip('Filter sessions'));
    await tester.pump();
    await tester.enterText(find.byType(TextField), 'ALP');
    await tester.pump();
    expect(find.text('dev:alpha'), findsOneWidget);
    expect(find.text('dev:beta'), findsNothing);

    await tester.enterText(find.byType(TextField), 'zzz');
    await tester.pump();
    expect(find.text('No sessions match.'), findsOneWidget);

    await tester.tap(find.byTooltip('Clear filter'));
    await tester.pump();
    expect(find.byType(TextField), findsNothing);
    expect(find.text('dev:beta'), findsOneWidget);
  });

  testWidgets('reconnect banner when not connected', (tester) async {
    await tester.pumpWidget(_app([
      gatewayProvider.overrideWithValue(null),
      connStateProvider.overrideWith((ref) => ConnState.reconnecting),
    ]));
    await tester.pump();
    expect(find.textContaining('Reconnecting'), findsOneWidget);
  });
}

class _SeededSessions extends SessionsNotifier {
  _SeededSessions(this._seed);
  final List<Session> _seed;
  @override
  Map<String, Session> build() => {for (final s in _seed) s.id: s};
}
