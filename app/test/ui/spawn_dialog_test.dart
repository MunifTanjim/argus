import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_riverpod/misc.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/core/result.dart';
import 'package:argus/data/session_repository.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/grouping.dart';
import 'package:argus/state/sessions.dart';
import 'package:argus/ui/spawn_dialog.dart';
import '../support/fake_session_repository.dart';

class _RecordingControl extends FakeSessionRepository {
  final spawnCalls = <Map<String, dynamic>>[];

  @override
  Future<Result<void>> spawn({
    String? nodeId,
    required String name,
    String? cwd,
    String? command,
  }) async {
    spawnCalls.add({
      'nodeId': nodeId,
      'name': name,
      'cwd': cwd,
      'command': command,
    });
    return const Result.ok(null);
  }
}

class _RecordingControlWithNodes extends _RecordingControl {
  _RecordingControlWithNodes(this._nodes);
  final List<NodeRef> _nodes;

  @override
  Future<Result<List<NodeRef>>> nodes() async => Result.ok(_nodes);
}

class _ThrowingControl extends FakeSessionRepository {
  @override
  Future<Result<void>> spawn({
    String? nodeId,
    required String name,
    String? cwd,
    String? command,
  }) async =>
      Result.error(Exception('spawn failed'));
}

class _SeededSessions extends SessionsNotifier {
  _SeededSessions(this._seed);
  final List<Session> _seed;
  @override
  Map<String, Session> build() => {for (final s in _seed) s.id: s};
}

Session _sn(String id, {String? nodeId, String? nodeLabel}) =>
    Session.fromJson(jsonDecode(jsonEncode({
      'id': id,
      'tool': 't',
      'status': 'working',
      'source': 'hooked',
      'tmux': {
        'server': 'argus',
        'pane_id': '%1',
        'session_name': 's',
        'window_index': 0,
        'current_path': '/p',
      },
      'node_id': ?nodeId,
      'node_label': ?nodeLabel,
    })) as Map<String, dynamic>);

Widget _app(List<Override> overrides) => ProviderScope(
      overrides: overrides,
      child: const MaterialApp(home: Scaffold(body: SpawnDialog())),
    );

void main() {
  testWidgets('Spawn button is disabled when name is empty', (tester) async {
    final control = _RecordingControl();
    await tester.pumpWidget(_app([
      sessionRepositoryProvider.overrideWithValue(control),
      gatewayProvider.overrideWithValue(null),
    ]));
    await tester.pump();

    final spawnButton = find.widgetWithText(TextButton, 'Spawn');
    expect(spawnButton, findsOneWidget);
    final btn = tester.widget<TextButton>(spawnButton);
    expect(btn.onPressed, isNull);
  });

  testWidgets('entering name enables Spawn and tapping it calls spawn',
      (tester) async {
    final control = _RecordingControl();
    await tester.pumpWidget(_app([
      sessionRepositoryProvider.overrideWithValue(control),
      gatewayProvider.overrideWithValue(null),
    ]));
    await tester.pump();

    await tester.enterText(find.widgetWithText(TextField, 'Name'), 'my-session');
    await tester.pump();

    final spawnButton = find.widgetWithText(TextButton, 'Spawn');
    final btn = tester.widget<TextButton>(spawnButton);
    expect(btn.onPressed, isNotNull);

    await tester.tap(spawnButton);
    await tester.pumpAndSettle();

    expect(control.spawnCalls, hasLength(1));
    expect(control.spawnCalls.first['name'], 'my-session');
  });

  testWidgets('host DropdownButton is absent when fewer than 2 nodes',
      (tester) async {
    final control = _RecordingControl();
    await tester.pumpWidget(_app([
      sessionRepositoryProvider.overrideWithValue(control),
      gatewayProvider.overrideWithValue(null),
      sessionsProvider.overrideWith(() => _SeededSessions([
            _sn('a', nodeId: 'node1', nodeLabel: 'Node One'),
          ])),
    ]));
    await tester.pump();

    expect(find.byType(DropdownButton<String>), findsNothing);
  });

  testWidgets('host DropdownButton is present when 2 or more nodes',
      (tester) async {
    final control = _RecordingControl();
    await tester.pumpWidget(_app([
      sessionRepositoryProvider.overrideWithValue(control),
      gatewayProvider.overrideWithValue(null),
      sessionsProvider.overrideWith(() => _SeededSessions([
            _sn('a', nodeId: 'node1', nodeLabel: 'Node One'),
            _sn('b', nodeId: 'node2', nodeLabel: 'Node Two'),
          ])),
    ]));
    await tester.pump();

    expect(find.byType(DropdownButton<String>), findsOneWidget);
  });

  testWidgets(
      'spawn call includes nodeId when 2+ nodes and a node is selected',
      (tester) async {
    final control = _RecordingControl();
    await tester.pumpWidget(_app([
      sessionRepositoryProvider.overrideWithValue(control),
      gatewayProvider.overrideWithValue(null),
      sessionsProvider.overrideWith(() => _SeededSessions([
            _sn('a', nodeId: 'node1', nodeLabel: 'Node One'),
            _sn('b', nodeId: 'node2', nodeLabel: 'Node Two'),
          ])),
    ]));
    await tester.pump();

    // The default selected node is the first one (node1)
    await tester.enterText(find.widgetWithText(TextField, 'Name'), 'my-session');
    await tester.pump();

    await tester.tap(find.widgetWithText(TextButton, 'Spawn'));
    await tester.pumpAndSettle();

    expect(control.spawnCalls, hasLength(1));
    expect(control.spawnCalls.first['nodeId'], 'node1');
  });

  testWidgets('spawn call omits nodeId (null) when only 1 node', (tester) async {
    final control = _RecordingControl();
    await tester.pumpWidget(_app([
      sessionRepositoryProvider.overrideWithValue(control),
      gatewayProvider.overrideWithValue(null),
      sessionsProvider.overrideWith(() => _SeededSessions([
            _sn('a', nodeId: 'node1', nodeLabel: 'Node One'),
          ])),
    ]));
    await tester.pump();

    await tester.enterText(find.widgetWithText(TextField, 'Name'), 'solo-session');
    await tester.pump();

    await tester.tap(find.widgetWithText(TextButton, 'Spawn'));
    await tester.pumpAndSettle();

    expect(control.spawnCalls, hasLength(1));
    expect(control.spawnCalls.first['nodeId'], isNull);
  });

  testWidgets('uses nodes.list for the picker even with no sessions',
      (tester) async {
    final control = _RecordingControlWithNodes(const [
      NodeRef('node1', 'Node One'),
      NodeRef('node2', 'Node Two'),
    ]);
    await tester.pumpWidget(_app([
      sessionRepositoryProvider.overrideWithValue(control),
      gatewayProvider.overrideWithValue(null),
    ]));
    await tester.pump(); // resolve the nodes.list future
    await tester.pump();

    // Picker appears from nodes.list despite there being zero sessions.
    expect(find.byType(DropdownButton<String>), findsOneWidget);

    await tester.enterText(
        find.widgetWithText(TextField, 'Name'), 'first-session');
    await tester.pump();
    await tester.tap(find.widgetWithText(TextButton, 'Spawn'));
    await tester.pumpAndSettle();

    expect(control.spawnCalls, hasLength(1));
    expect(control.spawnCalls.first['nodeId'], 'node1');
  });

  testWidgets('shows SnackBar with Failed text on spawn error and keeps dialog open',
      (tester) async {
    final control = _ThrowingControl();
    await tester.pumpWidget(_app([
      sessionRepositoryProvider.overrideWithValue(control),
      gatewayProvider.overrideWithValue(null),
    ]));
    await tester.pump();

    await tester.enterText(find.widgetWithText(TextField, 'Name'), 'fail-session');
    await tester.pump();

    await tester.tap(find.widgetWithText(TextButton, 'Spawn'));
    await tester.pumpAndSettle();

    // SnackBar with 'Failed' text is shown
    expect(find.textContaining('Failed'), findsOneWidget);
    // Dialog is still open: Name TextField is still present
    expect(find.widgetWithText(TextField, 'Name'), findsOneWidget);
  });
}
