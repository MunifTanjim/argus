import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/core/result.dart';
import 'package:argus/data/history_repository.dart';
import 'package:argus/models/chunk.dart';
import 'package:argus/models/history.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/tool_detail.dart';
import 'package:argus/ui/subagent_trace_screen.dart';

class _FakeHistoryRepository implements HistoryRepository {
  _FakeHistoryRepository(this._chunks);
  final List<Chunk> _chunks;
  String? capturedAgentId;

  @override
  Future<Result<List<Chunk>>> transcript({
    String? nodeId,
    required String transcriptPath,
    String? agentId,
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

void main() {
  testWidgets('renders an inline trace without subscribing', (tester) async {
    const item = Item(
      id: 'i',
      kind: ItemKind.subagent,
      subagents: [
        Subagent(
          type: 'Explore',
          hasTrace: true,
          trace: [
            Chunk(id: 't', kind: ChunkKind.ai, previewItemId: 'ti', items: [
              Item(id: 'ti', kind: ItemKind.text, text: 'searched everything'),
            ]),
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
      Chunk(id: 't', kind: ChunkKind.ai, previewItemId: 'ti', items: [
        Item(id: 'ti', kind: ItemKind.text, text: 'nested output'),
      ]),
    ]);
    const item = Item(
      id: 'i',
      kind: ItemKind.subagent,
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
