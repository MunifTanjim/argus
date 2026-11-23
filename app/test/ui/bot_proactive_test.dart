import 'dart:convert';
import 'dart:typed_data';

import 'package:argus/models/entry.dart';
import 'package:argus/models/bot.dart';
import 'package:argus/push/unifiedpush_background.dart';
import 'package:argus/state/bots.dart';
import 'package:argus/transport/jsonrpc.dart';
import 'package:argus/ui/bot_screen.dart';
import 'package:argus/ui/thread_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../support/fake_gateway_client.dart';

Map<String, dynamic> _argus(
  String id,
  String kind,
  Map<String, dynamic> payload, {
  String detail = '',
  int sec = 0,
}) => {
  'id': id,
  'kind': 'argus',
  'timestamp': '2026-11-22T10:00:0${sec}Z',
  'label': kind,
  if (detail.isNotEmpty) 'detail': detail,
  'event': {'id': sec, 'kind': kind, 'payload': payload},
};

Future<FakeGatewayClient> _pumpBot(
  WidgetTester tester, {
  String status = 'idle',
  Object? Function(String m, Object? p)? extra,
}) async {
  tester.view.physicalSize = const Size(500, 1800);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
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
              'settings': {
                'notify': {'value': 'needs-you', 'explicit': true},
                'proactivity': {'value': 'default'},
                'memory_mode': {'value': 'auto'},
              },
            },
          ],
        };
      case 'bot.usage':
        return {
          'bot_id': 'A:a1',
          'rows': [],
          'total': {'sessions': 4, 'cost': 1.234, 'tokens': 15200},
        };
      case 'bot.set':
        return {'commit': 'c1'};
      case 'bot.routine.list':
        return {'routines': []};
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
      child: const MaterialApp(home: BotScreen(botId: 'A:a1')),
    ),
  );
  await tester.pumpAndSettle();
  return client;
}

Future<FakeGatewayClient> _pumpThread(
  WidgetTester tester,
  List<Map<String, dynamic>> entries, {
  Object? Function(String m, Object? p)? extra,
}) async {
  tester.view.physicalSize = const Size(500, 1000);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  final client = FakeGatewayClient((m, p) async {
    final x = extra?.call(m, p);
    if (x != null) return x;
    switch (m) {
      case 'bot.thread.timeline':
        return {'thread_id': 'A:t1', 'entries': entries, 'sessions': []};
      case 'bot.list':
        return {
          'bots': [
            {'id': 'A:a1', 'name': 'scout', 'status': 'idle'},
          ],
        };
      case 'bot.thread.list':
        return {'threads': []};
      case 'bot.offer.start':
        return {'thread_id': 'A:t1', 'session_id': 'A:s1'};
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
      child: const MaterialApp(
        home: ThreadScreen(botId: 'A:a1', threadId: 'A:t1', title: 'Chat'),
      ),
    ),
  );
  await tester.pumpAndSettle();
  return client;
}

final _offer = _argus(
  'e1',
  'offer',
  {'offer_id': 'of_1', 'title': 'Tidy the inbox', 'details': 'about 10 minutes', 'prompt': 'tidy it'},
  detail: 'Offer: Tidy the inbox: about 10 minutes',
);

void main() {
  testWidgets('shows the last seven days of usage', (tester) async {
    final client = await _pumpBot(tester);
    expect(find.text('Last 7 days · 4 sessions · \$1.23 · 15.2k tokens'), findsOneWidget);
    final since = (client.calls.firstWhere((c) => c.$1 == 'bot.usage').$2 as Map)['since'] as String;
    final age = DateTime.now().toUtc().difference(DateTime.parse(since));
    expect(age.inDays, 7);
  });

  testWidgets('the selectors show the current settings', (tester) async {
    await _pumpBot(tester);
    SegmentedButton<String> sel(String key) => tester.widget(
      find.byKey(ValueKey('setting-$key')),
    );
    // An explicit setting is selected; a default selects nothing and says what applies.
    expect(sel('notify').selected, {'needs-you'});
    expect(sel('proactivity').selected, <String>{});
    expect(sel('memory.mode').selected, <String>{});
    expect(find.text('  default (default)'), findsOneWidget);
    expect(find.text('  default (auto)'), findsOneWidget);
    expect(find.textContaining('default (needs-you)'), findsNothing);
  });

  testWidgets('the notification selector writes through bot.set', (tester) async {
    final client = await _pumpBot(tester);
    await tester.tap(
      find.descendant(
        of: find.byKey(const ValueKey('setting-notify')),
        matching: find.text('Off'),
      ),
    );
    await tester.pumpAndSettle();
    expect(
      client.calls.firstWhere((c) => c.$1 == 'bot.set').$2,
      {'bot_id': 'A:a1', 'key': 'notify', 'value': 'off'},
    );
    // The bot list is read again afterwards.
    expect(client.calls.where((c) => c.$1 == 'bot.list').length, 2);
    // Picking the selected value removes the setting.
    await tester.tap(
      find.descendant(
        of: find.byKey(const ValueKey('setting-notify')),
        matching: find.text('Needs you'),
      ),
    );
    await tester.pumpAndSettle();
    expect(
      client.calls.where((c) => c.$1 == 'bot.set').last.$2,
      {'bot_id': 'A:a1', 'key': 'notify', 'value': ''},
    );
  });

  testWidgets('the proactivity selector writes its key', (tester) async {
    final client = await _pumpBot(tester);
    await tester.tap(
      find.descendant(
        of: find.byKey(const ValueKey('setting-proactivity')),
        matching: find.text('High'),
      ),
    );
    await tester.pumpAndSettle();
    expect(
      client.calls.firstWhere((c) => c.$1 == 'bot.set').$2,
      {'bot_id': 'A:a1', 'key': 'proactivity', 'value': 'high'},
    );
  });

  testWidgets('a rejected setting shows the server error', (tester) async {
    await _pumpBot(
      tester,
      extra: (m, p) => m == 'bot.set'
          ? throw const RpcError(-32000, 'notify must be one of all')
          : null,
    );
    await tester.tap(
      find.descendant(
        of: find.byKey(const ValueKey('setting-notify')),
        matching: find.text('Off'),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('set: notify must be one of all'), findsOneWidget);
  });

  testWidgets('an unreachable bot cannot change settings', (tester) async {
    final client = await _pumpBot(tester, status: 'unreachable');
    await tester.tap(
      find.descendant(
        of: find.byKey(const ValueKey('setting-notify')),
        matching: find.text('Off'),
      ),
    );
    await tester.pump();
    expect(find.text("bot's node is offline"), findsWidgets);
    expect(client.calls.any((c) => c.$1 == 'bot.set'), false);
    expect(client.calls.any((c) => c.$1 == 'bot.usage'), false);
  });

  testWidgets('a brief shows whole and health shows its message', (tester) async {
    await _pumpThread(tester, [
      _argus('e1', 'brief', {'text': 'Good morning. Three things today.'}, detail: 'Good morning. Three things today.', sec: 1),
      _argus('e2', 'health', {'key': 'k', 'message': 'disk is almost full'}, detail: 'Health: disk is almost full', sec: 2),
    ]);
    expect(find.textContaining('Three things today'), findsOneWidget);
    expect(find.text('  Health: disk is almost full'), findsOneWidget);
  });

  testWidgets('a hand-off answer shows the bot and its full answer', (tester) async {
    await _pumpThread(tester, [
      _argus('e1', 'handoff.asked', {'bot': 'ledger', 'task': 'totals'}, detail: 'Asked ledger: totals', sec: 1),
      _argus(
        'e2',
        'handoff.answered',
        {'bot': 'ledger', 'text': 'Line one.\n\nLine **two** is the total.'},
        detail: 'Answer from ledger',
        sec: 2,
      ),
      _argus('e3', 'handoff.refused', {'bot': 'vault'}, detail: 'vault refused', sec: 3),
      _argus('e4', 'handoff.no_answer', {'bot': 'ledger'}, detail: 'ledger did not answer', sec: 4),
    ]);
    expect(find.text('  Asked ledger: totals'), findsOneWidget);
    expect(find.byKey(const ValueKey('handoff-answer')), findsOneWidget);
    expect(find.text('ledger'), findsOneWidget);
    expect(find.textContaining('answered'), findsOneWidget);
    expect(find.textContaining('Line one.'), findsOneWidget);
    expect(find.textContaining('is the total.'), findsOneWidget);
    expect(find.text('  vault refused'), findsOneWidget);
    expect(find.text('  ledger did not answer'), findsOneWidget);
  });

  test('handoffAnswerOf reads the bot and text', () {
    Entry e(String kind, Map<String, dynamic> p) =>
        Entry.fromJson(_argus('x', kind, p));
    final a = handoffAnswerOf(e('handoff.answered', {'bot': ' ledger ', 'text': 'hi'}));
    expect(a?.bot, 'ledger');
    expect(a?.text, 'hi');
    expect(handoffAnswerOf(e('handoff.answered', {}))?.bot, 'Bot');
    expect(handoffAnswerOf(e('handoff.asked', {})), isNull);
  });

  testWidgets('an open offer can be started', (tester) async {
    final client = await _pumpThread(tester, [_offer]);
    expect(find.text('  Offer: Tidy the inbox: about 10 minutes'), findsOneWidget);
    await tester.tap(find.text('Start'));
    await tester.pumpAndSettle();
    expect(
      client.calls.firstWhere((c) => c.$1 == 'bot.offer.start').$2,
      {'thread_id': 'A:t1', 'offer_id': 'of_1'},
    );
  });

  testWidgets('an open offer can be dismissed', (tester) async {
    final client = await _pumpThread(tester, [_offer]);
    await tester.tap(find.text('Dismiss'));
    await tester.pumpAndSettle();
    expect(
      client.calls.firstWhere((c) => c.$1 == 'bot.offer.dismiss').$2,
      {'thread_id': 'A:t1', 'offer_id': 'of_1'},
    );
  });

  testWidgets('a decided offer has no buttons', (tester) async {
    await _pumpThread(tester, [
      _offer,
      _argus('e2', 'offer.started', {'offer_id': 'of_1'}, detail: 'Started', sec: 2),
    ]);
    expect(find.text('Start'), findsNothing);
    expect(find.text('Dismiss'), findsNothing);
  });

  testWidgets('a failed start shows the server error', (tester) async {
    await _pumpThread(
      tester,
      [_offer],
      extra: (m, p) => m == 'bot.offer.start'
          ? throw const RpcError(-32000, 'offer was already started')
          : null,
    );
    await tester.tap(find.text('Start'));
    await tester.pumpAndSettle();
    expect(find.text('start: offer was already started'), findsOneWidget);
  });

  test('argusRowText shows the node text, else the kind', () {
    Entry e(String kind, Map<String, dynamic> payload, {String detail = ''}) =>
        Entry.fromJson(_argus('x', kind, payload, detail: detail));
    expect(argusRowText(e('health', {'message': 'm'}, detail: 'Node text')), 'Node text');
    expect(argusRowText(e('health', {'message': 'm'})), 'health');
    expect(offerIdOf(e('offer', {'offer_id': 'of_1'})), 'of_1');
    expect(offerIdOf(e('brief', {'offer_id': 'of_1'})), '');
    expect(
      decidedOffers([
        e('offer.started', {'offer_id': 'a'}),
        e('offer.dismissed', {'offer_id': 'b'}),
        e('offer', {'offer_id': 'c'}),
      ]),
      {'a', 'b'},
    );
  });

  test('a bot push is qualified with its node', () {
    final push = decodeUnifiedPush(
      Uint8List.fromList(
        utf8.encode(
          jsonEncode({
            'title': 'scout',
            'data': {
              'node_id': 'A',
              'bot_id': 'a1',
              'bot_thread_id': 't1',
              'bot_kind': 'brief',
            },
          }),
        ),
      ),
    );
    expect(push.botThreadId, 'A:t1');
    expect(push.data['bot_id'], 'A:a1');
  });

  test('findThread matches the qualified thread id', () async {
    final client = FakeGatewayClient((m, p) async {
      if (m == 'bot.list') {
        return {
          'bots': [
            {'id': 'A:a1', 'name': 'x'},
          ],
        };
      }
      return {
        'threads': [
          {'id': 'A:t1', 'bot_id': 'A:a1', 'last_active_at': '2026-11-22T10:00:00Z'},
        ],
      };
    });
    final c = ProviderContainer();
    addTearDown(c.dispose);
    await c.read(botsProvider.notifier).load(client);
    final s = c.read(botsProvider);
    expect(s.findThread('A:t1')?.$1, 'A:a1');
    expect(s.findThread('A:t1')?.$2.id, 'A:t1');
    expect(s.findThread('t1'), isNull);
    expect(s.findThread('A:t2'), isNull);
  });
}
