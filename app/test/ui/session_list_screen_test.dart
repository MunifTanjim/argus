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
        '{"id":"$id","tool":"t","status":"$status","source":"hooked","tmux":{"server":"argus","pane_id":"%1","session_name":"s","window_index":0,"current_path":"/p"},"repo":"$id","node_label":"$host"}'));

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

  testWidgets('empty state when no sessions', (tester) async {
    await tester.pumpWidget(_app([
      gatewayProvider.overrideWithValue(null),
    ]));
    await tester.pump();
    expect(find.textContaining('No sessions'), findsOneWidget);
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
