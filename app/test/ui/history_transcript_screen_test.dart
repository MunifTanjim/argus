import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/core/result.dart';
import 'package:argus/data/history_repository.dart';
import 'package:argus/models/chunk.dart';
import 'package:argus/models/history.dart';
import 'package:argus/ui/history_transcript_screen.dart';

class _FakeHistoryRepository implements HistoryRepository {
  _FakeHistoryRepository(this._result);
  final Object _result; // List<Chunk> or Exception

  @override
  Future<Result<List<Chunk>>> transcript({
    String? nodeId,
    required String transcriptPath,
    String? agentId,
  }) async {
    final r = _result;
    if (r is Exception) return Result.error(r);
    return Result.ok(r as List<Chunk>);
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

HistorySession _session({String? title, String? firstMessage}) => HistorySession(
      sessionId: 'sess-1',
      title: title,
      firstMessage: firstMessage,
      transcriptPath: '/path/to/transcript',
      lastActivity: '',
      tokens: 0,
      turnCount: 0,
      durationMs: 0,
    );

Widget _app(HistoryRepository repo, HistorySession session) => ProviderScope(
      overrides: [historyRepositoryProvider.overrideWithValue(repo)],
      child: MaterialApp(home: HistoryTranscriptScreen(session: session)),
    );

void main() {
  testWidgets('renders chunk text when transcript loads', (tester) async {
    final repo = _FakeHistoryRepository(const [
      Chunk(id: 'c1', kind: ChunkKind.user, text: 'hello from history'),
    ]);
    await tester.pumpWidget(_app(repo, _session(title: 'My Session')));
    await tester.pump(); // initState fires, future resolves
    await tester.pump(); // setState re-render
    expect(find.textContaining('hello from history'), findsOneWidget);
  });

  testWidgets('shows error text when transcript call fails', (tester) async {
    final repo = _FakeHistoryRepository(Exception('network error'));
    await tester.pumpWidget(_app(repo, _session()));
    await tester.pump();
    await tester.pump();
    expect(find.textContaining('network error'), findsOneWidget);
  });

  testWidgets('uses title as AppBar title', (tester) async {
    final repo = _FakeHistoryRepository(const <Chunk>[]);
    await tester.pumpWidget(_app(repo, _session(title: 'My Title')));
    await tester.pump();
    await tester.pump();
    expect(find.text('My Title'), findsOneWidget);
  });

  testWidgets('falls back to firstMessage when title is null', (tester) async {
    final repo = _FakeHistoryRepository(const <Chunk>[]);
    await tester.pumpWidget(
        _app(repo, _session(title: null, firstMessage: 'first msg')));
    await tester.pump();
    await tester.pump();
    expect(find.text('first msg'), findsOneWidget);
  });

  testWidgets('falls back to sessionId when title and firstMessage null',
      (tester) async {
    final repo = _FakeHistoryRepository(const <Chunk>[]);
    await tester.pumpWidget(
        _app(repo, _session(title: null, firstMessage: null)));
    await tester.pump();
    await tester.pump();
    expect(find.text('sess-1'), findsOneWidget);
  });
}
