import 'package:argus/models/entry.dart';
import 'package:argus/models/pagent.dart';
import 'package:argus/state/pagents.dart';
import 'package:argus/transport/jsonrpc.dart';
import 'package:argus/ui/thread_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../support/fake_gateway_client.dart';

Map<String, dynamic> _timeline({bool live = true}) => {
  'thread_id': 'A:t1',
  'entries': [
    {
      'id': 'e1',
      'kind': 'text',
      'timestamp': '2026-11-22T10:00:00.123456789Z',
      'text': 'hello from the agent',
    },
    {
      'id': 'e2',
      'kind': 'argus',
      'timestamp': '2026-11-22T10:00:05Z',
      'label': 'memory.changed',
      'detail': 'remembered a thing',
      'event': {
        'id': 3,
        'kind': 'memory.changed',
        'payload': {'commit': 'abc123'},
      },
    },
    {
      'id': 'e4',
      'kind': 'argus',
      'timestamp': '2026-11-22T10:00:04Z',
      'label': 'routine.changed',
      'detail': 'Routine billing updated',
      'event': {
        'id': 5,
        'kind': 'routine.changed',
        'payload': {'commit': 'def456'},
      },
    },
    {
      'id': 'e5',
      'kind': 'argus',
      'timestamp': '2026-11-22T10:00:03Z',
      'label': 'session.paused',
      'event': {'id': 6, 'kind': 'session.paused'},
    },
    {
      'id': 'e3',
      'kind': 'argus',
      'timestamp': '2026-11-22T10:00:06Z',
      'detail': 'check-back set',
      'event': {'id': 4, 'kind': 'check-back'},
    },
  ],
  'sessions': [
    {'session_id': 'A:s1', 'start_index': 0, 'live': live},
  ],
};

Future<FakeGatewayClient> _pump(
  WidgetTester tester, {
  Object? Function(String m, Object? p)? extra,
  String status = 'idle',
  String initialText = '',
}) async {
  tester.view.physicalSize = const Size(500, 1000);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  final client = FakeGatewayClient((m, p) async {
    final x = extra?.call(m, p);
    if (x != null) return x;
    if (m == 'pagent.thread.timeline') return _timeline();
    if (m == 'pagent.list') {
      return {
        'agents': [
          {'id': 'A:a1', 'name': 'scout', 'status': status},
        ],
      };
    }
    if (m == 'pagent.thread.list') return {'threads': []};
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
        home: ThreadScreen(
          agentId: 'A:a1',
          threadId: 'A:t1',
          title: 'Plan',
          initialText: initialText,
        ),
      ),
    ),
  );
  await tester.pumpAndSettle();
  return client;
}

void main() {
  group('timelineDetailRef', () {
    const live = PAgentTimelineSession(
      sessionId: 'A:s2',
      live: true,
      cli: 'claude',
      transcriptPath: '/t/b.jsonl',
    );
    const ended = PAgentTimelineSession(
      sessionId: 'A:s1',
      cli: 'codex',
      transcriptPath: '/t/a.jsonl',
    );
    const none = PAgentTimelineSession(sessionId: 'A:s0');
    const sessions = [ended, live, none];

    test('a live session serves its entries', () {
      final r = timelineDetailRef('A', sessions, 's1:e7');
      expect(r.isHistory, false);
      expect(r.sessionId, 'A:s2');
      expect(r.agent, 'claude');
    });

    test('an ended session reads its transcript file', () {
      final r = timelineDetailRef('A', sessions, 's0:e3');
      expect(r.isHistory, true);
      expect(r.nodeId, 'A');
      expect(r.transcriptPath, '/t/a.jsonl');
      expect(r.agent, 'codex');
    });

    test('the live session of the same transcript wins for an older entry', () {
      final r = timelineDetailRef(
        'A',
        const [
          PAgentTimelineSession(
            sessionId: 'A:s1',
            cli: 'claude',
            transcriptPath: '/t/b.jsonl',
          ),
          live,
        ],
        's0:e1',
      );
      expect(r.sessionId, 'A:s2');
    });

    test('an entry without a known source gets an empty reference', () {
      for (final id in ['argus-1', 's2:e1', 's9:e1']) {
        final r = timelineDetailRef('A', sessions, id);
        expect(r.sessionId, isNull, reason: id);
        expect(r.transcriptPath, isNull, reason: id);
      }
    });
  });

  test('timestampNanos keeps nanosecond precision', () {
    expect(
      timestampNanos('2026-11-22T10:00:00.123456789Z'),
      DateTime.utc(2026, 11, 22, 10).millisecondsSinceEpoch * 1000000 + 123456789,
    );
    expect(
      timestampNanos('2026-11-22T10:00:00Z'),
      DateTime.utc(2026, 11, 22, 10).millisecondsSinceEpoch * 1000000,
    );
    expect(timestampNanos('nope'), 0);
    expect(timestampNanos(null), 0);
  });

  test('liveSessionOf picks the last live session', () {
    final tl = PAgentTimeline.fromJson({
      'thread_id': 't',
      'sessions': [
        {'session_id': 'a', 'live': true},
        {'session_id': 'b', 'live': true},
        {'session_id': 'c'},
      ],
    });
    expect(liveSessionOf(tl), 'b');
    expect(undoCommit(Entry.fromJson({'id': 'x', 'kind': 'text'})), '');
  });

  testWidgets('renders the timeline with argus events and marks it read', (tester) async {
    final client = await _pump(tester);
    expect(find.text('hello from the agent'), findsOneWidget);
    expect(find.text('  remembered a thing'), findsOneWidget);
    expect(find.text('  check-back set'), findsOneWidget);
    expect(find.text('Undo'), findsNWidgets(2)); // memory and routine changes
    expect(find.text('  Routine billing updated'), findsOneWidget);
    expect(find.text('  session.paused'), findsOneWidget); // no detail: the kind
    final read = client.calls.firstWhere((c) => c.$1 == 'pagent.thread.markRead');
    expect(read.$2, {
      'thread_id': 'A:t1',
      'position': DateTime.utc(2026, 11, 22, 10, 0, 6).millisecondsSinceEpoch * 1000000,
    });
  });

  testWidgets('undo calls pagent.memory.undo with the commit and reloads', (tester) async {
    final client = await _pump(tester);
    final loads = client.calls.where((c) => c.$1 == 'pagent.thread.timeline').length;
    await tester.tap(find.text('Undo').first);
    await tester.pumpAndSettle();
    expect(
      client.calls.firstWhere((c) => c.$1 == 'pagent.memory.undo').$2,
      {'agent_id': 'A:a1', 'commit': 'abc123'},
    );
    expect(
      client.calls.where((c) => c.$1 == 'pagent.thread.timeline').length,
      loads + 1,
    );
  });

  testWidgets('undo on a routine change reverts its commit', (tester) async {
    final client = await _pump(tester);
    await tester.tap(find.text('Undo').last);
    await tester.pumpAndSettle();
    expect(
      client.calls.firstWhere((c) => c.$1 == 'pagent.memory.undo').$2,
      {'agent_id': 'A:a1', 'commit': 'def456'},
    );
  });

  testWidgets('the composer sends to the thread', (tester) async {
    final client = await _pump(tester, initialText: 'prefilled');
    expect(find.text('prefilled'), findsOneWidget);
    await tester.enterText(find.byType(TextField), 'next step');
    await tester.tap(find.byTooltip('Send'));
    await tester.pumpAndSettle();
    expect(
      client.calls.firstWhere((c) => c.$1 == 'pagent.thread.send').$2,
      {'agent_id': 'A:a1', 'thread_id': 'A:t1', 'text': 'next step'},
    );
    expect(find.text('next step'), findsNothing); // cleared
  });

  testWidgets('a failed send keeps the text and shows the error', (tester) async {
    await _pump(
      tester,
      extra: (m, p) => m == 'pagent.thread.send'
          ? throw const RpcError(-32000, 'agent busy')
          : null,
    );
    await tester.enterText(find.byType(TextField), 'next step');
    await tester.tap(find.byTooltip('Send'));
    await tester.pumpAndSettle();
    expect(find.text('send: agent busy'), findsOneWidget);
    expect(find.text('next step'), findsOneWidget);
  });

  testWidgets('an unreachable agent does not send', (tester) async {
    final client = await _pump(tester, status: 'unreachable');
    await tester.enterText(find.byType(TextField), 'x');
    await tester.tap(find.byTooltip('Send'));
    await tester.pump();
    expect(find.text("agent's node is offline"), findsOneWidget);
    expect(client.calls.any((c) => c.$1 == 'pagent.thread.send'), false);
  });

  testWidgets('open session says so when the live session is unknown', (tester) async {
    await _pump(tester);
    await tester.tap(find.byTooltip('Open session'));
    await tester.pump();
    expect(find.text('no live session in this thread'), findsOneWidget);
  });
}
