import 'package:argus/models/pagent.dart';
import 'package:argus/state/agent_link.dart';
import 'package:argus/state/pagents.dart';
import 'package:argus/ui/agent_link.dart';
import 'package:argus/ui/agent_screen.dart';
import 'package:argus/ui/thread_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../support/fake_gateway_client.dart';

PAgentThread _t(String id, String kind, String status, int minute) =>
    PAgentThread(
      id: id,
      kind: kind,
      status: status,
      lastActiveAt: DateTime.utc(2026, 11, 22, 10, minute),
    );

void main() {
  test('parses argus://agent links', () {
    expect(parseAgentLink('argus://agent/A:a1'), const AgentLink('A:a1'));
    expect(
      parseAgentLink('argus://agent/A:a1?text=hello%20there'),
      const AgentLink('A:a1', text: 'hello there'),
    );
    expect(parseAgentLink('argus://pair?url=x&token=y'), isNull);
    expect(parseAgentLink('https://agent/A:a1'), isNull);
    expect(parseAgentLink('argus://agent/'), isNull);
    expect(parseAgentLink('argus://agent/a/b'), isNull);
    expect(parseAgentLink('not a url'), isNull);
  });

  test('the latest chat thread is the most recent open chat', () {
    expect(
      latestChatThread([
        _t('old', 'chat', 'active', 1),
        _t('run', 'run', 'active', 9),
        _t('closed', 'chat', 'closed', 8),
        _t('new', 'chat', 'active', 5),
      ])?.id,
      'new',
    );
    expect(latestChatThread([_t('r', 'run', 'active', 1)]), isNull);
  });

  Future<(ProviderContainer, FakeGatewayClient)> pump(
    WidgetTester tester,
    List<Map<String, dynamic>> threads,
    AgentLink link,
  ) async {
    tester.view.physicalSize = const Size(500, 1000);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final client = FakeGatewayClient((m, p) async {
      switch (m) {
        case 'pagent.list':
          return {
            'agents': [
              {'id': 'A:a1', 'name': 'scout', 'status': 'idle'},
            ],
          };
        case 'pagent.thread.list':
          return {'threads': threads};
        case 'pagent.thread.timeline':
          return {'thread_id': 'A:t1', 'entries': [], 'sessions': []};
      }
      return null;
    });
    final c = ProviderContainer(
      overrides: [pagentsClientProvider.overrideWithValue(client)],
    );
    addTearDown(c.dispose);
    await c.read(pagentsProvider.notifier).load(client);
    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: c,
        child: MaterialApp(
          home: Builder(
            builder: (context) => Scaffold(
              body: TextButton(
                onPressed: () =>
                    openAgentLink(context, c.read(pagentsProvider), link),
                child: const Text('go'),
              ),
            ),
          ),
        ),
      ),
    );
    await tester.tap(find.text('go'));
    await tester.pumpAndSettle();
    return (c, client);
  }

  testWidgets('opens the latest chat thread with the composer prefilled', (tester) async {
    await pump(
      tester,
      [
        {'id': 'A:t0', 'kind': 'chat', 'status': 'active', 'last_active_at': '2026-11-22T09:00:00Z'},
        {'id': 'A:t1', 'kind': 'chat', 'status': 'active', 'title': 'Newest', 'last_active_at': '2026-11-22T10:00:00Z'},
      ],
      const AgentLink('A:a1', text: 'status?'),
    );
    expect(find.byType(ThreadScreen), findsOneWidget);
    expect(find.text('Newest'), findsOneWidget);
    expect(find.text('status?'), findsOneWidget);
  });

  testWidgets('without a chat thread opens a prefilled new chat', (tester) async {
    await pump(tester, [], const AgentLink('A:a1', text: 'status?'));
    expect(find.byType(AgentScreen), findsOneWidget);
    expect(find.text('New chat'), findsWidgets);
    expect(find.text('status?'), findsOneWidget);
  });

  testWidgets('an unknown agent says so', (tester) async {
    await pump(tester, [], const AgentLink('A:nope'));
    expect(find.text('agent not found'), findsOneWidget);
    expect(find.byType(AgentScreen), findsNothing);
  });

  testWidgets('a node-local agent id opens that agent', (tester) async {
    await pump(tester, [], const AgentLink('a1', text: 'status?'));
    expect(find.byType(AgentScreen), findsOneWidget);
  });

  test('a local id names an agent only when one agent has it', () {
    const state = PAgentsState(
      agents: [PAgent(id: 'A:a1'), PAgent(id: 'B:a1'), PAgent(id: 'B:b2')],
    );
    expect(resolveAgentLinkId(state, 'A:a1'), 'A:a1');
    expect(resolveAgentLinkId(state, 'b2'), 'B:b2');
    expect(resolveAgentLinkId(state, 'a1'), isNull);
    expect(resolveAgentLinkId(state, 'zz'), isNull);
  });
}
