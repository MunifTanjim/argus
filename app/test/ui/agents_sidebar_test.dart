import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/state/agent_sections.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/left_sidebar.dart';
import 'package:argus/state/pagents.dart';
import 'package:argus/ui/project_drawer.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../support/fake_gateway_client.dart';

class _MemKv implements SecureKv {
  final m = <String, String>{};
  @override
  Future<String?> read(String key) async => m[key];
  @override
  Future<void> write(String key, String value) async => m[key] = value;
  @override
  Future<void> delete(String key) async => m.remove(key);
}

final _overrideStore = leftSidebarTabStoreProvider.overrideWithValue(
  LeftSidebarTabStore(_MemKv()),
);

final _agents = {
  'agents': [
    {'id': 'A:i1', 'name': 'idle-one', 'status': 'idle', 'agent': 'claude', 'node_label': 'mbp'},
    {'id': 'A:u1', 'name': 'unread-one', 'status': 'idle'},
    {'id': 'A:w1', 'name': 'working-one', 'status': 'working'},
    {'id': 'A:n1', 'name': 'needs-one', 'status': 'needs_you'},
    {'id': 'A:t1', 'name': 'waiting-one', 'status': 'waiting'},
    {'id': 'B:x1', 'name': 'offline-one', 'status': 'unreachable'},
    {'id': 'A:p1', 'name': 'primary-one', 'status': 'working'},
    {'id': 'A:gone', 'name': 'archived-one', 'status': 'idle', 'archived': true},
  ],
  'primary': 'A:p1',
};


FakeGatewayClient _client() => FakeGatewayClient((m, p) async {
  if (m == 'pagent.list') return _agents;
  if (m == 'pagent.thread.list') {
    final id = (p as Map)['agent_id'];
    return {
      'threads': [
        {
          'id': 't',
          'agent_id': id,
          'last_active_at': '2026-11-22T10:00:00Z',
          'unread': id == 'A:u1',
        },
      ],
    };
  }
  return null;
});

void main() {
  test('sections are ordered primary, needs you, waiting, working, unread, idle, unreachable', () async {
    final c = ProviderContainer();
    addTearDown(c.dispose);
    await c.read(pagentsProvider.notifier).load(_client());
    final groups = buildAgentSections(c.read(pagentsProvider), '');
    expect(groups.map((g) => g.section.title), [
      'Primary',
      'Needs you',
      'Waiting',
      'Working',
      'Unread',
      'Idle',
      'Unreachable',
    ]);
    expect(groups[0].agents.single.name, 'primary-one');
    expect(groups[3].agents.single.name, 'working-one');
    expect(
      groups.expand((g) => g.agents).any((a) => a.archived),
      false,
    );
    expect(
      buildAgentSections(c.read(pagentsProvider), 'mbp').single.agents.single.name,
      'idle-one',
    );
  });

  testWidgets('the Agents tab lists agents by section and filters', (tester) async {
    tester.view.physicalSize = const Size(400, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final c = ProviderContainer(
      overrides: [gatewayProvider.overrideWithValue(null), _overrideStore],
    );
    addTearDown(c.dispose);
    await c.read(pagentsProvider.notifier).load(_client());
    await c.read(leftSidebarTabProvider.notifier).set(LeftSidebarTab.agents);
    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: c,
        child: const MaterialApp(home: Scaffold(body: ProjectDrawer())),
      ),
    );
    await tester.pump();

    for (final t in ['Primary', 'Needs you', 'Waiting', 'Working', 'Unread', 'Idle', 'Unreachable']) {
      expect(find.text(t), findsOneWidget, reason: t);
    }
    expect(find.text('primary-one'), findsOneWidget);
    expect(find.text('archived-one'), findsNothing);
    expect(find.text('Claude · mbp · 1 thread'), findsOneWidget);
    expect(find.byIcon(Icons.circle), findsOneWidget);

    await tester.enterText(find.byType(TextField), 'work');
    await tester.pump();
    expect(find.text('working-one'), findsOneWidget);
    expect(find.text('idle-one'), findsNothing);

    await tester.enterText(find.byType(TextField), 'zzz');
    await tester.pump();
    expect(find.text('no agents match "zzz"'), findsOneWidget);
  });

  testWidgets('tapping an agent opens its screen', (tester) async {
    tester.view.physicalSize = const Size(400, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final client = _client();
    final c = ProviderContainer(
      overrides: [
        gatewayProvider.overrideWithValue(null),
        _overrideStore,
        pagentsClientProvider.overrideWithValue(client),
      ],
    );
    addTearDown(c.dispose);
    await c.read(pagentsProvider.notifier).load(client);
    await c.read(leftSidebarTabProvider.notifier).set(LeftSidebarTab.agents);
    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: c,
        child: const MaterialApp(home: Scaffold(body: ProjectDrawer())),
      ),
    );
    await tester.pump();
    await tester.tap(find.text('idle-one'));
    await tester.pumpAndSettle();
    expect(find.text('Threads'), findsOneWidget);
  });
}
