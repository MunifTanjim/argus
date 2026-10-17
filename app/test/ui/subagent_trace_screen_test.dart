import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/core/result.dart';
import 'package:argus/data/history_repository.dart';
import 'package:argus/models/entry.dart';
import 'package:argus/models/history.dart';
import 'package:argus/data/transcript_repository.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/sessions.dart';
import 'package:argus/state/transcript.dart';
import 'package:argus/state/transcript_controller.dart';
import 'package:argus/state/tool_detail.dart';
import 'package:argus/ui/subagent_trace_screen.dart';

class _FakeHistoryRepository implements HistoryRepository {
  _FakeHistoryRepository(this._chunks);
  final List<Entry> _chunks;
  String? capturedAgentId;

  @override
  Future<Result<List<Entry>>> transcript({
    String? nodeId,
    required String transcriptPath,
    String? agentId,
    String? agent,
  }) async {
    capturedAgentId = agentId;
    return Result.ok(_chunks);
  }

  @override
  Future<Result<List<HistoryProject>>> projects() async => const Result.ok([]);

  @override
  Future<Result<HistorySessionPage>> sessions({
    String? nodeId,
    required String projectDir,
    required int limit,
    required int offset,
  }) async =>
      const Result.ok(HistorySessionPage(items: [], hasMore: false));
}

class _NoopSub implements TranscriptSubscription {
  @override
  void dispose() {}
}

class _CountingRepo implements TranscriptRepository {
  int opens = 0;
  @override
  TranscriptSubscription? open({
    required String sessionId,
    String? agentId,
    required TranscriptNotifier store,
  }) {
    opens++;
    return _NoopSub();
  }
}

Session _session({bool offline = false}) => Session.fromJson({
      'id': 's',
      'agent': 't',
      'status': 'working',
      'source': 'hooked',
      'frontend': 'tmux',
      'tmux': {'pane_id': '%1'},
      'offline': offline,
    });

void main() {
  // A restarted node drops its subscriptions while this connection stays up.
  testWidgets('re-opens a live trace when the session comes back online',
      (tester) async {
    final repo = _CountingRepo();
    const item = Entry(
      id: 'i',
      kind: EntryKind.subagent,
      subagents: [Subagent(id: 'a1', type: 'Explore', hasTrace: true)],
    );
    await tester.pumpWidget(ProviderScope(
      overrides: [
        gatewayProvider.overrideWithValue(null),
        transcriptRepositoryProvider.overrideWithValue(repo),
      ],
      child: const MaterialApp(
          home: SubagentTraceScreen(
              parentRef: ToolDetailRef.live('s'), item: item)),
    ));
    final container = ProviderScope.containerOf(
        tester.element(find.byType(SubagentTraceScreen)));
    container.read(sessionsProvider.notifier).replaceAll([_session()]);
    await tester.pump();
    final opened = repo.opens;

    container.read(sessionsProvider.notifier).replaceAll([_session(offline: true)]);
    await tester.pump();
    expect(repo.opens, opened);

    container.read(sessionsProvider.notifier).replaceAll([_session()]);
    await tester.pump();
    expect(repo.opens, opened + 1);
  });

  testWidgets('renders an inline trace without subscribing', (tester) async {
    const item = Entry(
      id: 'i',
      kind: EntryKind.subagent,
      subagents: [
        Subagent(
          type: 'Explore',
          hasTrace: true,
          trace: [
            Entry(id: 'ti', kind: EntryKind.text, text: 'searched everything'),
          ],
        ),
      ],
    );
    await tester.pumpWidget(ProviderScope(
      overrides: [gatewayProvider.overrideWithValue(null)],
      child: const MaterialApp(
          home: SubagentTraceScreen(
              parentRef: ToolDetailRef.live('s'), item: item)),
    ));
    await tester.pump();
    expect(find.text('Explore'), findsWidgets);
    expect(find.textContaining('searched everything'), findsOneWidget);
  });

  testWidgets('fetches nested trace for a history subagent', (tester) async {
    final repo = _FakeHistoryRepository(const [
      Entry(id: 'ti', kind: EntryKind.text, text: 'nested output'),
    ]);
    const item = Entry(
      id: 'i',
      kind: EntryKind.subagent,
      // no inline trace => lazy history fetch
      subagents: [Subagent(type: 'Explore', id: 'B', hasTrace: true)],
    );
    await tester.pumpWidget(ProviderScope(
      overrides: [
        gatewayProvider.overrideWithValue(null),
        historyRepositoryProvider.overrideWithValue(repo),
      ],
      child: const MaterialApp(
        home: SubagentTraceScreen(
          parentRef: ToolDetailRef.history(
              nodeId: 'n1', transcriptPath: '/p/sess.jsonl', agentId: 'A'),
          item: item,
        ),
      ),
    ));
    await tester.pump(); // initState kicks the future
    await tester.pump(); // FutureBuilder resolves
    expect(repo.capturedAgentId, 'B');
    expect(find.textContaining('nested output'), findsOneWidget);
  });
}
