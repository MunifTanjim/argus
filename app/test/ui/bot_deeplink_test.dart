import 'package:argus/models/bot.dart';
import 'package:argus/state/bot_link.dart';
import 'package:argus/state/bots.dart';
import 'package:argus/ui/bot_link.dart';
import 'package:argus/ui/bot_screen.dart';
import 'package:argus/ui/thread_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../support/fake_gateway_client.dart';

BotThread _t(String id, String kind, String status, int minute) =>
    BotThread(
      id: id,
      kind: kind,
      status: status,
      lastActiveAt: DateTime.utc(2026, 11, 22, 10, minute),
    );

void main() {
  test('parses argus://bot links', () {
    expect(parseBotLink('argus://bot/A:a1'), const BotLink('A:a1'));
    expect(
      parseBotLink('argus://bot/A:a1?text=hello%20there'),
      const BotLink('A:a1', text: 'hello there'),
    );
    expect(parseBotLink('argus://pair?url=x&token=y'), isNull);
    expect(parseBotLink('https://bot/A:a1'), isNull);
    expect(parseBotLink('argus://bot/'), isNull);
    expect(parseBotLink('argus://bot/a/b'), isNull);
    expect(parseBotLink('not a url'), isNull);
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
    BotLink link,
  ) async {
    tester.view.physicalSize = const Size(500, 1000);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final client = FakeGatewayClient((m, p) async {
      switch (m) {
        case 'bot.list':
          return {
            'bots': [
              {'id': 'A:a1', 'name': 'scout', 'status': 'idle'},
            ],
          };
        case 'bot.thread.list':
          return {'threads': threads};
        case 'bot.thread.timeline':
          return {'thread_id': 'A:t1', 'entries': [], 'sessions': []};
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
        child: MaterialApp(
          home: Builder(
            builder: (context) => Scaffold(
              body: TextButton(
                onPressed: () =>
                    openBotLink(context, c.read(botsProvider), link),
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
      const BotLink('A:a1', text: 'status?'),
    );
    expect(find.byType(ThreadScreen), findsOneWidget);
    expect(find.text('Newest'), findsOneWidget);
    expect(find.text('status?'), findsOneWidget);
  });

  testWidgets('without a chat thread opens a prefilled new chat', (tester) async {
    await pump(tester, [], const BotLink('A:a1', text: 'status?'));
    expect(find.byType(BotScreen), findsOneWidget);
    expect(find.text('New chat'), findsWidgets);
    expect(find.text('status?'), findsOneWidget);
  });

  testWidgets('an unknown bot says so', (tester) async {
    await pump(tester, [], const BotLink('A:nope'));
    expect(find.text('bot not found'), findsOneWidget);
    expect(find.byType(BotScreen), findsNothing);
  });

  testWidgets('a node-local bot id opens that bot', (tester) async {
    await pump(tester, [], const BotLink('a1', text: 'status?'));
    expect(find.byType(BotScreen), findsOneWidget);
  });

  test('a local id names a bot only when one bot has it', () {
    const state = BotsState(
      bots: [Bot(id: 'A:a1'), Bot(id: 'B:a1'), Bot(id: 'B:b2')],
    );
    expect(resolveBotLinkId(state, 'A:a1'), 'A:a1');
    expect(resolveBotLinkId(state, 'b2'), 'B:b2');
    expect(resolveBotLinkId(state, 'a1'), isNull);
    expect(resolveBotLinkId(state, 'zz'), isNull);
  });
}
