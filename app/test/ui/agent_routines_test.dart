import 'package:argus/models/entry.dart';
import 'package:argus/models/pagent.dart';
import 'package:argus/state/pagents.dart';
import 'package:argus/transport/jsonrpc.dart';
import 'package:argus/ui/agent_memory_screen.dart';
import 'package:argus/ui/agent_screen.dart';
import 'package:argus/ui/pagent_style.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../support/fake_gateway_client.dart';

Map<String, dynamic> _routine(
  String slug, {
  String state = 'active',
  String? pausedReason,
}) => {
  'agent_id': 'A:a1',
  'slug': slug,
  'what': 'Watch the billing queue',
  'starts': ['every weekday at 09:00'],
  'state': state,
  'paused_reason': ?pausedReason,
  'next_due': DateTime.now().toUtc().add(const Duration(hours: 3, minutes: 5)).toIso8601String(),
  'budget': {'sessions': 10, 'cost': 5.0, 'tokens': 0},
  'used': {'sessions': 2, 'cost': 0.4, 'tokens': 1500},
};

Future<FakeGatewayClient> _pump(
  WidgetTester tester, {
  List<Map<String, dynamic>>? routines,
  String status = 'idle',
  Object? Function(String m, Object? p)? extra,
}) async {
  tester.view.physicalSize = const Size(500, 1400);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  final client = FakeGatewayClient((m, p) async {
    final x = extra?.call(m, p);
    if (x != null) return x;
    switch (m) {
      case 'pagent.list':
        return {
          'agents': [
            {'id': 'A:a1', 'name': 'scout', 'status': status},
          ],
        };
      case 'pagent.routine.list':
        return {'routines': routines ?? [_routine('billing'), _routine('digest', state: 'paused', pausedReason: 'budget')]};
      case 'pagent.routine.run':
        return {'run_id': 'r1'};
      case 'pagent.routine.pause':
      case 'pagent.routine.resume':
        return _routine((p as Map)['slug'] as String);
      case 'pagent.thread.list':
        return {'threads': []};
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
      child: const MaterialApp(home: AgentScreen(agentId: 'A:a1')),
    ),
  );
  await tester.pumpAndSettle();
  return client;
}

Future<void> _menu(WidgetTester tester, int row, String item) async {
  await tester.tap(find.byTooltip('Routine actions').at(row));
  await tester.pumpAndSettle();
  await tester.tap(find.text(item));
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('lists routines with schedule, next run, progress and state', (tester) async {
    await _pump(tester);
    expect(find.text('Routines'), findsOneWidget);
    expect(find.text('billing'), findsOneWidget);
    expect(find.text('active'), findsOneWidget);
    expect(find.text('paused (budget)'), findsOneWidget);
    expect(
      find.text(
        'Watch the billing queue · every weekday at 09:00 · next in 3h · 2/10 sessions · \$0.40/\$5.00 · 1.5k tokens',
      ),
      findsOneWidget,
    );
    // A paused routine shows no next run.
    expect(
      find.text(
        'Watch the billing queue · every weekday at 09:00 · 2/10 sessions · \$0.40/\$5.00 · 1.5k tokens',
      ),
      findsOneWidget,
    );
  });

  testWidgets('shows no routines when there are none', (tester) async {
    await _pump(tester, routines: []);
    expect(find.text('no routines'), findsOneWidget);
  });

  testWidgets('Run now, Pause and Resume call their RPCs', (tester) async {
    final client = await _pump(tester);
    await _menu(tester, 0, 'Run now');
    expect(
      client.calls.firstWhere((c) => c.$1 == 'pagent.routine.run').$2,
      {'agent_id': 'A:a1', 'slug': 'billing'},
    );
    expect(find.text('billing: test run started'), findsOneWidget);

    await _menu(tester, 0, 'Pause');
    expect(
      client.calls.firstWhere((c) => c.$1 == 'pagent.routine.pause').$2,
      {'agent_id': 'A:a1', 'slug': 'billing'},
    );

    await _menu(tester, 1, 'Resume');
    expect(
      client.calls.firstWhere((c) => c.$1 == 'pagent.routine.resume').$2,
      {'agent_id': 'A:a1', 'slug': 'digest'},
    );
  });

  testWidgets('an action failure shows the server error', (tester) async {
    await _pump(
      tester,
      extra: (m, p) => m == 'pagent.routine.pause'
          ? throw const RpcError(-32000, 'routine is running')
          : null,
    );
    await _menu(tester, 0, 'Pause');
    expect(find.text('pause: routine is running'), findsOneWidget);
  });

  testWidgets('an unreachable agent has no routine actions', (tester) async {
    final client = await _pump(tester, status: 'unreachable');
    expect(find.byTooltip('Routine actions'), findsNothing);
    expect(client.calls.any((c) => c.$1 == 'pagent.routine.list'), false);
  });

  testWidgets('Review shows routine proposals', (tester) async {
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
        case 'pagent.memory.proposals':
          return {
            'proposals': [
              {
                'id': 'A:mp1',
                'agent_id': 'A:a1',
                'kind': 'routine',
                'slug': 'billing',
                'summary': 'change goal billing: outcome, verify',
                'reason': 'tighten the check',
                'ops': [],
                'created_at': '2026-11-22T10:00:00Z',
              },
            ],
          };
        case 'pagent.memory.decide':
          return {'applied': 1};
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
        child: const MaterialApp(home: AgentMemoryScreen(agentId: 'A:a1')),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.text('Review'));
    await tester.pumpAndSettle();
    expect(find.text('Routine change'), findsOneWidget);
    expect(find.text('billing · change goal billing: outcome, verify'), findsOneWidget);
    expect(find.text('tighten the check'), findsOneWidget);
    await tester.tap(find.text('Accept'));
    await tester.pumpAndSettle();
    expect(
      client.calls.firstWhere((x) => x.$1 == 'pagent.memory.decide').$2,
      {'proposal_id': 'A:mp1', 'accept': true},
    );
  });

  group('argusEventText', () {
    String text(String kind, Map<String, dynamic> payload, {String detail = ''}) =>
        argusEventText(
          Entry.fromJson({
            'id': 'e',
            'kind': 'argus',
            'label': kind,
            if (detail.isNotEmpty) 'detail': detail,
            'event': {'id': 1, 'kind': kind, 'payload': payload},
          }),
        );

    test('routine events read as sentences', () {
      expect(
        text('routine.result', {'routine': 'billing', 'result': 'done', 'summary': 'sent 3 invoices'}),
        'Routine billing (done): sent 3 invoices',
      );
      expect(
        text('routine.needs_you', {'routine': 'billing', 'summary': 'login expired'}),
        'Routine billing needs you: login expired',
      );
      expect(text('routine.completed', {'routine': 'billing'}), 'Routine billing completed');
      expect(
        text('routine.failing', {'routine': 'billing', 'error': 'timeout'}),
        'Routine billing is failing: timeout',
      );
      expect(
        text('routine.error', {'routine': 'billing', 'message': 'bad file'}),
        'Routine billing: bad file',
      );
      expect(
        text('routine.changed', {'routine': 'billing', 'action': 'updated'}),
        'Routine billing updated',
      );
      expect(
        text('routine.proposed', {'routine': 'billing', 'reason': 'new goal'}),
        'Routine billing: change to review: new goal',
      );
      expect(
        text('routine.skipped', {'routine': 'billing', 'reason': 'still running'}),
        'Routine billing skipped a run: still running',
      );
      expect(
        text('routine.stale', {'routine': 'billing', 'due': '2026-11-22T09:00:00Z'}),
        'Routine billing is overdue (due 2026-11-22T09:00:00Z)',
      );
    });

    test('missed counts and paused lists', () {
      expect(
        text('routine.missed', {'routine': 'b', 'count': 1, 'first': 'T1'}),
        'Routine b missed a run (T1)',
      );
      expect(
        text('routine.missed', {'routine': 'b', 'count': 3}),
        'Routine b missed 3 runs',
      );
      expect(
        text('routine.paused', {'reason': 'budget', 'routines': ['a']}),
        'Routine a paused (budget)',
      );
      expect(
        text('routine.paused', {'reason': 'away', 'routines': ['a', 'b']}),
        'Routines paused (away): a, b',
      );
    });

    test('drafts and unknown events', () {
      expect(
        text('draft', {'tool': 'Bash', 'target': 'git', 'command': 'git push'}),
        'Draft held: git push',
      );
      expect(text('draft', {'tool': 'Write', 'target': '/x'}), 'Draft held: Write /x');
      expect(
        text('memory.changed', {'commit': 'c'}, detail: 'Memory: changed'),
        'Memory: changed',
      );
    });
  });

  test('routineProgress and untilText', () {
    final r = PAgentRoutine.fromJson({
      'slug': 's',
      'budget': {'sessions': 0, 'cost': 0, 'tokens': 0},
      'used': {'sessions': 3, 'cost': 0, 'tokens': 0},
    });
    expect(routineProgress(r), '3 sessions');
    final now = DateTime.utc(2026, 11, 22, 10);
    expect(untilText(now.add(const Duration(days: 2)), now), 'in 2d');
    expect(untilText(now.add(const Duration(seconds: 10)), now), 'now');
    expect(untilText(now.subtract(const Duration(minutes: 5)), now), 'overdue 5m');
  });
}
