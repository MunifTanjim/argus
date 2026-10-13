import 'package:argus/models/session.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/projects.dart';
import 'package:argus/state/sessions.dart';
import 'package:argus/transport/connection.dart';
import 'package:argus/transport/gateway_client.dart';
import 'package:argus/ui/sidebar_sessions_list.dart';
import 'package:argus/ui/workspace_sessions_tab.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../support/fake_gateway_client.dart';

class _Manager extends ConnectionManager {
  _Manager(this._client)
    : super(
        connect: () => throw UnimplementedError(),
        clientFactory: (_, _) => _client,
      );
  final GatewayClient _client;
  @override
  GatewayClient? get client => _client;
}

class _Projects extends ProjectsNotifier {
  @override
  ProjectsState build() => const ProjectsState(projects: [], loaded: true);
}

class _Sessions extends SessionsNotifier {
  _Sessions(this._seed);
  final List<Session> _seed;
  @override
  Map<String, Session> build() => {for (final s in _seed) s.id: s};
}

Session _s(String id) => Session.fromJson({
  'id': id,
  'agent': 'claude',
  'status': 'idle',
  'source': 'hooked',
  'tmux': <String, dynamic>{},
  'workspace_id': 'w1',
});

Future<FakeGatewayClient> _pump(
  WidgetTester tester,
  Widget child, {
  List<Session> sessions = const [],
}) async {
  final client = FakeGatewayClient((m, p) async => const []);
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        gatewayProvider.overrideWithValue(_Manager(client)),
        projectsProvider.overrideWith(_Projects.new),
        sessionsProvider.overrideWith(() => _Sessions(sessions)),
      ],
      child: MaterialApp(home: Scaffold(body: child)),
    ),
  );
  return client;
}

Future<void> _pull(WidgetTester tester) async {
  await tester.fling(find.byType(Scrollable).first, const Offset(0, 400), 1000);
  await tester.pumpAndSettle();
}

int _refreshes(FakeGatewayClient c) =>
    c.calls.where((c) => c.$1 == 'sessions.refresh').length;

void main() {
  for (final (name, sessions) in [
    ('empty', <Session>[]),
    ('short', [_s('a')]),
  ]) {
    testWidgets('workspace sessions: pull refreshes a $name list', (
      tester,
    ) async {
      final client = await _pump(
        tester,
        const WorkspaceSessionsTab(workspaceId: 'w1'),
        sessions: sessions,
      );
      await _pull(tester);
      expect(_refreshes(client), 1);
    });

    testWidgets('sidebar sessions: pull refreshes a $name list', (
      tester,
    ) async {
      final client = await _pump(
        tester,
        SidebarSessionsList(onOpen: () {}),
        sessions: sessions,
      );
      await _pull(tester);
      expect(_refreshes(client), 1);
    });
  }
}
