import 'package:argus/state/gateway.dart';
import 'package:argus/state/bots.dart';
import 'package:argus/transport/jsonrpc.dart';
import 'package:argus/ui/bot_screen.dart';
import 'package:argus/ui/bot_style.dart' show botOffline;
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../support/fake_gateway_client.dart';

Future<(ProviderContainer, FakeGatewayClient)> _pump(
  WidgetTester tester, {
  String status = 'idle',
  bool primary = false,
  Object? Function(String m, Object? p)? extra,
}) async {
  tester.view.physicalSize = const Size(500, 2000);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  var primaryId = primary ? 'A:a1' : '';
  final client = FakeGatewayClient((m, p) async {
    final x = extra?.call(m, p);
    if (x != null) return x;
    switch (m) {
      case 'bot.list':
        return {
          'bots': [
            {
              'id': 'A:a1',
              'name': 'scout',
              'status': status,
              'agent': 'claude',
              'node_label': 'mbp',
            },
          ],
          if (primaryId.isNotEmpty) 'primary': primaryId,
        };
      case 'bot.thread.list':
        return {
          'threads': [
            {
              'id': 'A:t1',
              'bot_id': 'A:a1',
              'kind': 'chat',
              'title': 'Plan the week',
              'unread': true,
              'last_active_at': DateTime.now().toUtc().toIso8601String(),
            },
          ],
        };
      case 'bot.memory.show':
        return {'memory': 'm', 'usage': 3072, 'budget': 8192};
      case 'bot.memory.log':
        return {
          'commits': [
            {
              'hash': 'h',
              'when': DateTime.now().toUtc().toIso8601String(),
            },
          ],
        };
      case 'bot.setPrimary':
        primaryId = (p as Map)['bot_id'] as String? ?? '';
        return null;
      case 'bot.thread.send':
        return {'thread_id': 'A:t2', 'session_id': 'A:s2'};
    }
    return null;
  });
  final c = ProviderContainer(
    overrides: [
      gatewayProvider.overrideWithValue(null),
      botsClientProvider.overrideWithValue(client),
    ],
  );
  addTearDown(c.dispose);
  await c.read(botsProvider.notifier).load(client);
  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: c,
      child: const MaterialApp(home: BotScreen(botId: 'A:a1')),
    ),
  );
  await tester.pumpAndSettle();
  return (c, client);
}

void main() {
  testWidgets('shows the bot, its threads and memory usage', (tester) async {
    await _pump(tester);
    expect(find.text('scout'), findsOneWidget);
    expect(find.text('Claude · home: mbp'), findsOneWidget);
    expect(find.text('Plan the week'), findsOneWidget);
    expect(find.byIcon(Icons.circle), findsOneWidget);
    expect(find.text('Memory · 3 KB / 8 KB · last change just now'), findsOneWidget);
  });

  testWidgets('a new chat sends with new set', (tester) async {
    final (_, client) = await _pump(tester);
    await tester.tap(find.byTooltip('New chat'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField), 'hello there');
    await tester.tap(find.text('Send'));
    await tester.pumpAndSettle();
    final call = client.calls.firstWhere((c) => c.$1 == 'bot.thread.send');
    expect(call.$2, {'bot_id': 'A:a1', 'text': 'hello there', 'new': true});
  });

  testWidgets('a failed send shows the error', (tester) async {
    await _pump(
      tester,
      extra: (m, p) => m == 'bot.thread.send'
          ? throw const RpcError(-32000, 'no capacity')
          : null,
    );
    await tester.tap(find.byTooltip('New chat'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField), 'x');
    await tester.tap(find.text('Send'));
    await tester.pumpAndSettle();
    expect(find.text('send failed: no capacity'), findsOneWidget);
  });

  testWidgets('make primary and unset primary', (tester) async {
    final (c, client) = await _pump(tester);
    await tester.tap(find.byType(PopupMenuButton<String>));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Make primary'));
    await tester.pumpAndSettle();
    expect(
      client.calls.firstWhere((x) => x.$1 == 'bot.setPrimary').$2,
      {'bot_id': 'A:a1'},
    );
    expect(c.read(botsProvider).primary, 'A:a1');
    expect(find.text('Claude · home: mbp · primary'), findsOneWidget);

    await tester.tap(find.byType(PopupMenuButton<String>));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Unset primary'));
    await tester.pumpAndSettle();
    expect(client.calls.where((x) => x.$1 == 'bot.setPrimary').last.$2, <String, dynamic>{});
    expect(c.read(botsProvider).primary, '');
  });

  testWidgets('an unreachable bot disables its actions', (tester) async {
    final (_, client) = await _pump(tester, status: 'unreachable');
    expect(find.text(botOffline), findsNWidgets(4));
    await tester.tap(find.byTooltip('New chat'));
    await tester.pump();
    expect(find.text(botOffline), findsNWidgets(5)); // four notes and the snackbar
    expect(find.byType(AlertDialog), findsNothing);
    await tester.tap(find.byType(PopupMenuButton<String>));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Make primary'));
    await tester.pump();
    expect(client.calls.any((c) => c.$1 == 'bot.setPrimary'), false);
    expect(client.calls.any((c) => c.$1 == 'bot.thread.list'), false);
  });
}
