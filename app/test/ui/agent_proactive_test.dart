import 'dart:convert';
import 'dart:typed_data';

import 'package:argus/models/entry.dart';
import 'package:argus/models/pagent.dart';
import 'package:argus/push/unifiedpush_background.dart';
import 'package:argus/state/pagents.dart';
import 'package:argus/transport/jsonrpc.dart';
import 'package:argus/ui/agent_screen.dart';
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

Future<FakeGatewayClient> _pumpAgent(
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
      case 'pagent.list':
        return {
          'agents': [
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
      case 'pagent.usage':
        return {
          'agent_id': 'A:a1',
          'rows': [],
          'total': {'sessions': 4, 'cost': 1.234, 'tokens': 15200},
        };
      case 'pagent.set':
        return {'commit': 'c1'};
      case 'pagent.routine.list':
        return {'routines': []};
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
      case 'pagent.thread.timeline':
        return {'thread_id': 'A:t1', 'entries': entries, 'sessions': []};
      case 'pagent.list':
        return {
          'agents': [
            {'id': 'A:a1', 'name': 'scout', 'status': 'idle'},
          ],
        };
      case 'pagent.thread.list':
        return {'threads': []};
      case 'pagent.offer.start':
        return {'thread_id': 'A:t1', 'session_id': 'A:s1'};
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
      child: const MaterialApp(
        home: ThreadScreen(agentId: 'A:a1', threadId: 'A:t1', title: 'Chat'),
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
    final client = await _pumpAgent(tester);
    expect(find.text('Last 7 days · 4 sessions · \$1.23 · 15.2k tokens'), findsOneWidget);
    final since = (client.calls.firstWhere((c) => c.$1 == 'pagent.usage').$2 as Map)['since'] as String;
    final age = DateTime.now().toUtc().difference(DateTime.parse(since));
    expect(age.inDays, 7);
  });

  testWidgets('the selectors show the current settings', (tester) async {
    await _pumpAgent(tester);
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

  testWidgets('the notification selector writes through pagent.set', (tester) async {
    final client = await _pumpAgent(tester);
    await tester.tap(
      find.descendant(
        of: find.byKey(const ValueKey('setting-notify')),
        matching: find.text('Off'),
      ),
    );
    await tester.pumpAndSettle();
    expect(
      client.calls.firstWhere((c) => c.$1 == 'pagent.set').$2,
      {'agent_id': 'A:a1', 'key': 'notify', 'value': 'off'},
    );
    // The agent list is read again afterwards.
    expect(client.calls.where((c) => c.$1 == 'pagent.list').length, 2);
    // Picking the selected value removes the setting.
    await tester.tap(
      find.descendant(
        of: find.byKey(const ValueKey('setting-notify')),
        matching: find.text('Needs you'),
      ),
    );
    await tester.pumpAndSettle();
    expect(
      client.calls.where((c) => c.$1 == 'pagent.set').last.$2,
      {'agent_id': 'A:a1', 'key': 'notify', 'value': ''},
    );
  });

  testWidgets('the proactivity selector writes its key', (tester) async {
    final client = await _pumpAgent(tester);
    await tester.tap(
      find.descendant(
        of: find.byKey(const ValueKey('setting-proactivity')),
        matching: find.text('High'),
      ),
    );
    await tester.pumpAndSettle();
    expect(
      client.calls.firstWhere((c) => c.$1 == 'pagent.set').$2,
      {'agent_id': 'A:a1', 'key': 'proactivity', 'value': 'high'},
    );
  });

  testWidgets('a rejected setting shows the server error', (tester) async {
    await _pumpAgent(
      tester,
      extra: (m, p) => m == 'pagent.set'
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

  testWidgets('an unreachable agent cannot change settings', (tester) async {
    final client = await _pumpAgent(tester, status: 'unreachable');
    await tester.tap(
      find.descendant(
        of: find.byKey(const ValueKey('setting-notify')),
        matching: find.text('Off'),
      ),
    );
    await tester.pump();
    expect(find.text("agent's node is offline"), findsWidgets);
    expect(client.calls.any((c) => c.$1 == 'pagent.set'), false);
    expect(client.calls.any((c) => c.$1 == 'pagent.usage'), false);
  });

  testWidgets('a brief shows whole and health shows its message', (tester) async {
    await _pumpThread(tester, [
      _argus('e1', 'brief', {'text': 'Good morning. Three things today.'}, detail: 'Good morning. Three things today.', sec: 1),
      _argus('e2', 'health', {'key': 'k', 'message': 'disk is almost full'}, detail: 'Health: disk is almost full', sec: 2),
    ]);
    expect(find.textContaining('Three things today'), findsOneWidget);
    expect(find.text('  Health: disk is almost full'), findsOneWidget);
  });

  testWidgets('an open offer can be started', (tester) async {
    final client = await _pumpThread(tester, [_offer]);
    expect(find.text('  Offer: Tidy the inbox: about 10 minutes'), findsOneWidget);
    await tester.tap(find.text('Start'));
    await tester.pumpAndSettle();
    expect(
      client.calls.firstWhere((c) => c.$1 == 'pagent.offer.start').$2,
      {'thread_id': 'A:t1', 'offer_id': 'of_1'},
    );
  });

  testWidgets('an open offer can be dismissed', (tester) async {
    final client = await _pumpThread(tester, [_offer]);
    await tester.tap(find.text('Dismiss'));
    await tester.pumpAndSettle();
    expect(
      client.calls.firstWhere((c) => c.$1 == 'pagent.offer.dismiss').$2,
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
      extra: (m, p) => m == 'pagent.offer.start'
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

  test('an agent push is qualified with its node', () {
    final push = decodeUnifiedPush(
      Uint8List.fromList(
        utf8.encode(
          jsonEncode({
            'title': 'scout',
            'data': {
              'node_id': 'A',
              'pagent_id': 'a1',
              'pagent_thread_id': 't1',
              'pagent_kind': 'brief',
            },
          }),
        ),
      ),
    );
    expect(push.pagentThreadId, 'A:t1');
    expect(push.data['pagent_id'], 'A:a1');
  });

  test('findThread matches the qualified thread id', () async {
    final client = FakeGatewayClient((m, p) async {
      if (m == 'pagent.list') {
        return {
          'agents': [
            {'id': 'A:a1', 'name': 'x'},
          ],
        };
      }
      return {
        'threads': [
          {'id': 'A:t1', 'agent_id': 'A:a1', 'last_active_at': '2026-11-22T10:00:00Z'},
        ],
      };
    });
    final c = ProviderContainer();
    addTearDown(c.dispose);
    await c.read(pagentsProvider.notifier).load(client);
    final s = c.read(pagentsProvider);
    expect(s.findThread('A:t1')?.$1, 'A:a1');
    expect(s.findThread('A:t1')?.$2.id, 'A:t1');
    expect(s.findThread('t1'), isNull);
    expect(s.findThread('A:t2'), isNull);
  });
}
