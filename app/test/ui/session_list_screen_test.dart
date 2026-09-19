import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_riverpod/misc.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/core/result.dart';
import 'package:argus/data/session_repository.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/grouping.dart';
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

  testWidgets('reconnect banner when not connected', (tester) async {
    await tester.pumpWidget(_app([
      gatewayProvider.overrideWithValue(null),
      connStateProvider.overrideWith((ref) => ConnState.reconnecting),
    ]));
    await tester.pump();
    expect(find.textContaining('Reconnecting'), findsOneWidget);
  });

  testWidgets('idle opencode session is dismissible; working is not',
      (tester) async {
    final idleSession = _sa('oc:idle', 'dev', 'idle', 'opencode');
    final workingSession = _sa('oc:work', 'dev', 'working', 'opencode');
    final repo = _FakeRepository();

    await tester.pumpWidget(_app([
      sessionsProvider.overrideWith(
          () => _SeededSessions([idleSession, workingSession])),
      gatewayProvider.overrideWithValue(null),
      sessionRepositoryProvider.overrideWithValue(repo),
    ]));
    await tester.pump();

    expect(find.byKey(const ValueKey('oc:idle')), findsOneWidget);
    expect(find.byKey(const ValueKey('oc:work')), findsNothing);

    await tester.drag(
        find.byKey(const ValueKey('oc:idle')), const Offset(-500, 0));
    await tester.pumpAndSettle();
    expect(repo.dismissed, contains('oc:idle'));
  });

  testWidgets('dismissing removes the row from local state and survives rebuild',
      (tester) async {
    final idleSession = _sa('oc:idle', 'dev', 'idle', 'opencode');
    final repo = _FakeRepository();

    await tester.pumpWidget(_app([
      sessionsProvider.overrideWith(() => _SeededSessions([idleSession])),
      gatewayProvider.overrideWithValue(null),
      sessionRepositoryProvider.overrideWithValue(repo),
      connStateProvider.overrideWith((ref) => ConnState.connected),
    ]));
    await tester.pump();

    await tester.drag(
        find.byKey(const ValueKey('oc:idle')), const Offset(-500, 0));
    await tester.pumpAndSettle();

    // Force a parent rebuild before any server removal event arrives.
    final container = ProviderScope.containerOf(
        tester.element(find.byType(SessionListScreen)));
    container.read(connStateProvider.notifier).state = ConnState.reconnecting;
    await tester.pumpAndSettle();

    expect(tester.takeException(), isNull);
    expect(find.byKey(const ValueKey('oc:idle')), findsNothing);
    expect(repo.dismissed, contains('oc:idle'));
  });

  testWidgets('failed dismiss re-inserts the row and shows a SnackBar',
      (tester) async {
    final idleSession = _sa('oc:idle', 'dev', 'idle', 'opencode');
    final repo = _FakeRepository()..dismissError = 'node rejected';

    await tester.pumpWidget(_app([
      sessionsProvider.overrideWith(() => _SeededSessions([idleSession])),
      gatewayProvider.overrideWithValue(null),
      sessionRepositoryProvider.overrideWithValue(repo),
    ]));
    await tester.pump();

    await tester.drag(
        find.byKey(const ValueKey('oc:idle')), const Offset(-500, 0));
    await tester.pumpAndSettle();

    expect(find.textContaining('Failed to dismiss'), findsOneWidget);
    expect(find.byKey(const ValueKey('oc:idle')), findsOneWidget);
  });
}

class _SeededSessions extends SessionsNotifier {
  _SeededSessions(this._seed);
  final List<Session> _seed;
  @override
  Map<String, Session> build() => {for (final s in _seed) s.id: s};
}

class _FakeRepository implements SessionRepository {
  final List<String> dismissed = [];
  String? dismissError;

  @override
  Future<Result<void>> dismiss(String sessionId) {
    dismissed.add(sessionId);
    if (dismissError != null) return Future.value(Result.error(dismissError!));
    return Future.value(Result.ok(null));
  }

  @override
  Future<Result<void>> respond(Map<String, dynamic> params) async =>
      Result.ok(null);

  @override
  Future<Result<void>> sendInput(String sessionId, String text) async =>
      Result.ok(null);

  @override
  Future<Result<String>> capture(String sessionId) async =>
      const Result.ok('');

  @override
  Future<Result<void>> sendKeys(String sessionId, List<String> keys) async =>
      Result.ok(null);

  @override
  Future<Result<void>> sendRaw(String sessionId, String text) async =>
      Result.ok(null);

  @override
  Future<Result<void>> spawn({
    String? nodeId,
    String? cwd,
    String? agent,
    required String prompt,
  }) async =>
      Result.ok(null);

  @override
  Future<Result<void>> kill(String sessionId) async => Result.ok(null);

  @override
  Future<Result<List<NodeRef>>> nodes() async => const Result.ok([]);

  @override
  Future<Result<List<AgentInfo>>> listAgents(String? nodeId) async =>
      const Result.ok([]);

  @override
  Future<Result<ResumeOutcome>> resume({
    String? nodeId,
    required String agent,
    required String agentSessionId,
    required String cwd,
  }) async =>
      Result.ok(const ResumeOutcome(sessionId: ''));
}
