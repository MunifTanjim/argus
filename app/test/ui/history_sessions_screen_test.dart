import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/core/result.dart';
import 'package:argus/data/history_repository.dart';
import 'package:argus/models/history.dart';
import 'package:argus/models/chunk.dart';
import 'package:argus/ui/history_sessions_screen.dart';

class _FakeHistoryRepository implements HistoryRepository {
  _FakeHistoryRepository(this._pages);

  /// Successive calls to sessions() consume pages in order; last one repeats.
  final List<HistorySessionPage> _pages;
  int _callCount = 0;

  @override
  Future<Result<HistorySessionPage>> sessions({
    String? nodeId,
    required String projectDir,
    required int limit,
    required int offset,
  }) async {
    final idx = _callCount < _pages.length ? _callCount : _pages.length - 1;
    _callCount++;
    return Result.ok(_pages[idx]);
  }

  @override
  Future<Result<List<HistoryProject>>> projects() async => const Result.ok([]);

  @override
  Future<Result<List<Chunk>>> transcript({
    String? nodeId,
    required String transcriptPath,
    String? agentId,
  }) async =>
      const Result.ok([]);
}

class _ThrowingHistoryRepository implements HistoryRepository {
  @override
  Future<Result<HistorySessionPage>> sessions({
    String? nodeId,
    required String projectDir,
    required int limit,
    required int offset,
  }) async =>
      Result.error(Exception('sessions error'));

  @override
  Future<Result<List<HistoryProject>>> projects() async => const Result.ok([]);

  @override
  Future<Result<List<Chunk>>> transcript({
    String? nodeId,
    required String transcriptPath,
    String? agentId,
  }) async =>
      const Result.ok([]);
}

HistoryProject _project() => const HistoryProject(
      projectDir: '/home/user/project',
      cwd: '/home/user/project',
      label: 'My Project',
      sessionCount: 2,
      lastActivity: '',
    );

HistorySession _session(String id, {String? title, int turnCount = 0}) =>
    HistorySession(
      sessionId: id,
      title: title,
      transcriptPath: '/path/to/$id',
      lastActivity: '',
      tokens: 0,
      turnCount: turnCount,
      durationMs: 0,
    );

Widget _app(HistoryRepository repo) => ProviderScope(
      overrides: [historyRepositoryProvider.overrideWithValue(repo)],
      child: MaterialApp(
        home: HistorySessionsScreen(project: _project()),
      ),
    );

void main() {
  testWidgets('shows first page item, loads more and appends second page',
      (tester) async {
    final page1 = HistorySessionPage(
      items: [_session('sess-1', title: 'First Session')],
      hasMore: true,
    );
    final page2 = HistorySessionPage(
      items: [_session('sess-2', title: 'Second Session')],
      hasMore: false,
    );
    final repo = _FakeHistoryRepository([page1, page2]);

    await tester.pumpWidget(_app(repo));
    await tester.pump(); // initState fires
    await tester.pump(); // setState re-render

    expect(find.text('First Session'), findsOneWidget);
    expect(find.text('Load more'), findsOneWidget);

    await tester.tap(find.text('Load more'));
    await tester.pump(); // tap triggers loadMore
    await tester.pump(); // setState with second page

    expect(find.text('First Session'), findsOneWidget);
    expect(find.text('Second Session'), findsOneWidget);
    expect(find.text('Load more'), findsNothing);
  });

  testWidgets('shows empty state when no sessions', (tester) async {
    final repo = _FakeHistoryRepository([
      const HistorySessionPage(items: [], hasMore: false),
    ]);

    await tester.pumpWidget(_app(repo));
    await tester.pump();
    await tester.pump();

    expect(find.text('No sessions in this project.'), findsOneWidget);
  });

  testWidgets('shows AppBar title from project.label', (tester) async {
    final repo = _FakeHistoryRepository([
      const HistorySessionPage(items: [], hasMore: false),
    ]);

    await tester.pumpWidget(_app(repo));
    await tester.pump();
    await tester.pump();

    expect(find.text('My Project'), findsOneWidget);
  });

  testWidgets('shows error text when sessions() fails', (tester) async {
    await tester.pumpWidget(_app(_ThrowingHistoryRepository()));
    await tester.pump();
    await tester.pump();

    expect(find.textContaining('sessions error'), findsOneWidget);
  });

  testWidgets('uses sessionId as fallback when title and firstMessage null',
      (tester) async {
    final repo = _FakeHistoryRepository([
      HistorySessionPage(
        items: [_session('sess-abc')],
        hasMore: false,
      ),
    ]);

    await tester.pumpWidget(_app(repo));
    await tester.pump();
    await tester.pump();

    expect(find.text('sess-abc'), findsOneWidget);
  });

  testWidgets('shows turn count when turnCount > 0', (tester) async {
    final repo = _FakeHistoryRepository([
      HistorySessionPage(
        items: [_session('sess-1', title: 'A Session', turnCount: 5)],
        hasMore: false,
      ),
    ]);

    await tester.pumpWidget(_app(repo));
    await tester.pump();
    await tester.pump();

    expect(find.text('5 turns'), findsOneWidget);
  });
}
