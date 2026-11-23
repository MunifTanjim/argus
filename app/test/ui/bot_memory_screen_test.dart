import 'package:argus/state/bots.dart';
import 'package:argus/transport/jsonrpc.dart';
import 'package:argus/ui/bot_memory_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../support/fake_gateway_client.dart';

Future<FakeGatewayClient> _pump(
  WidgetTester tester, {
  Object? Function(String m, Object? p)? extra,
}) async {
  tester.view.physicalSize = const Size(500, 1000);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  var proposals = [
    {
      'id': 'A:mp1',
      'bot_id': 'A:a1',
      'source': 'capture',
      'reason': 'learned a preference',
      'ops': [
        {'action': 'add', 'text': 'likes tea'},
      ],
      'created_at': '2026-11-22T10:00:00Z',
    },
    {
      'id': 'A:mp2',
      'bot_id': 'A:a1',
      'source': 'tidy',
      'kind': 'instructions',
      'text': 'Be brief.',
      'ops': [],
      'created_at': '2026-11-22T10:00:00Z',
    },
  ];
  final client = FakeGatewayClient((m, p) async {
    final x = extra?.call(m, p);
    if (x != null) return x;
    switch (m) {
      case 'bot.list':
        return {
          'bots': [
            {'id': 'A:a1', 'name': 'scout', 'status': 'idle'},
          ],
        };
      case 'bot.memory.show':
        return {
          'memory': '- likes coffee',
          'user': '',
          'profile': 'shared',
          'topics': ['topics/work.md'],
          'usage': 2048,
          'budget': 8192,
        };
      case 'bot.memory.log':
        return {
          'commits': [
            {
              'hash': 'h1',
              'subject': 'capture: coffee',
              'source': (p as Map)['profile'] == true ? 'tidy' : 'capture',
              'when': DateTime.now().toUtc().toIso8601String(),
            },
          ],
        };
      case 'bot.memory.proposals':
        return {'proposals': proposals};
      case 'bot.memory.decide':
        final id = (p as Map)['proposal_id'];
        proposals = [
          for (final x in proposals)
            if (x['id'] != id) x,
        ];
        return {'applied': 1, 'rejected': <String>[]};
      case 'bot.memory.undo':
        return {'commit': 'h2'};
    }
    return null;
  });
  final c = ProviderContainer(
    overrides: [botsClientProvider.overrideWithValue(client)],
  );
  addTearDown(c.dispose);
  await c.read(botsProvider.notifier).load(client);
  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: c,
      child: const MaterialApp(home: BotMemoryScreen(botId: 'A:a1')),
    ),
  );
  await tester.pumpAndSettle();
  return client;
}

void main() {
  testWidgets('Files shows the memory files and usage', (tester) async {
    await _pump(tester);
    expect(find.text('2 KB / 8 KB'), findsOneWidget);
    expect(find.text('USER.md'), findsOneWidget);
    expect(find.text('empty'), findsOneWidget);
    expect(find.text('topics/work.md'), findsOneWidget);
    await tester.tap(find.text('MEMORY.md'));
    await tester.pumpAndSettle();
    expect(find.text('- likes coffee'), findsOneWidget);
  });

  testWidgets('History undoes a commit after confirmation', (tester) async {
    final client = await _pump(tester);
    await tester.tap(find.text('History'));
    await tester.pumpAndSettle();
    expect(find.text('capture: coffee'), findsOneWidget);
    await tester.tap(find.byTooltip('Undo'));
    await tester.pumpAndSettle();
    expect(client.calls.any((c) => c.$1 == 'bot.memory.undo'), false);
    await tester.tap(find.text('Cancel'));
    await tester.pumpAndSettle();
    expect(client.calls.any((c) => c.$1 == 'bot.memory.undo'), false);

    await tester.tap(find.byTooltip('Undo'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(FilledButton, 'Undo'));
    await tester.pumpAndSettle();
    expect(
      client.calls.firstWhere((c) => c.$1 == 'bot.memory.undo').$2,
      {'bot_id': 'A:a1', 'commit': 'h1'},
    );
  });

  testWidgets('History can show the shared profile log', (tester) async {
    final client = await _pump(tester);
    await tester.tap(find.text('History'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Shared profile'));
    await tester.pumpAndSettle();
    expect(
      client.calls.where((c) => c.$1 == 'bot.memory.log').last.$2,
      {'bot_id': 'A:a1', 'profile': true},
    );
  });

  testWidgets('Review lists proposals and decides them', (tester) async {
    final client = await _pump(tester);
    await tester.tap(find.text('Review'));
    await tester.pumpAndSettle();
    expect(find.text('Memory change'), findsOneWidget);
    expect(find.text('Instruction rewrite'), findsOneWidget);
    expect(find.text('Be brief.'), findsOneWidget);
    expect(find.text('likes tea'), findsOneWidget);

    await tester.tap(find.text('Accept').first);
    await tester.pumpAndSettle();
    expect(
      client.calls.firstWhere((c) => c.$1 == 'bot.memory.decide').$2,
      {'proposal_id': 'A:mp1', 'accept': true},
    );
    expect(find.text('likes tea'), findsNothing);

    await tester.tap(find.text('Reject'));
    await tester.pumpAndSettle();
    expect(
      client.calls.where((c) => c.$1 == 'bot.memory.decide').last.$2,
      {'proposal_id': 'A:mp2', 'accept': false},
    );
    expect(find.text('nothing to review'), findsOneWidget);
  });

  testWidgets('Review shows the server error when a decision fails', (tester) async {
    await _pump(
      tester,
      extra: (m, p) => m == 'bot.memory.decide'
          ? throw const RpcError(-32000, 'file changed')
          : null,
    );
    await tester.tap(find.text('Review'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Accept').first);
    await tester.pumpAndSettle();
    expect(find.text('accept: file changed'), findsOneWidget);
    expect(find.text('likes tea'), findsOneWidget);
  });
}
