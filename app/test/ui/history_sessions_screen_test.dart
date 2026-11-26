import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/core/result.dart';
import 'package:argus/data/history_repository.dart';
import 'package:argus/models/history.dart';
import 'package:argus/models/entry.dart';
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
  Future<Result<List<Entry>>> transcript({
    String? nodeId,
    required String transcriptPath,
    String? agentId,
    String? agent,
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
  Future<Result<List<Entry>>> transcript({
    String? nodeId,
    required String transcriptPath,
    String? agentId,
    String? agent,
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

HistorySession _session(
  String id, {
  String? title,
  String? firstMessage,
  int turnCount = 0,
  int durationMs = 0,
  String lastActivity = '',
  String agent = '',
}) =>
    HistorySession(
      sessionId: id,
      title: title,
      firstMessage: firstMessage,
      transcriptPath: '/path/to/$id',
      lastActivity: lastActivity,
      tokens: 0,
      turnCount: turnCount,
      durationMs: durationMs,
      agent: agent,
    );

Future<void> _pumpSessions(
    WidgetTester tester, List<HistorySession> items) async {
  await tester.pumpWidget(_app(_FakeHistoryRepository(
      [HistorySessionPage(items: items, hasMore: false)])));
  await tester.pump();
  await tester.pump();
}

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

  testWidgets('filters loaded sessions and applies the filter to new pages',
      (tester) async {
    final page1 = HistorySessionPage(
      items: [
        _session('sess-1', title: 'Fix the login bug'),
        _session('sess-2', title: 'Add history filter'),
      ],
      hasMore: true,
    );
    final page2 = HistorySessionPage(
      items: [_session('sess-3', title: 'Filter the logs')],
      hasMore: false,
    );
    final repo = _FakeHistoryRepository([page1, page2]);

    await tester.pumpWidget(_app(repo));
    await tester.pump();
    await tester.pump();

    await tester.tap(find.byTooltip('Filter sessions'));
    await tester.pump();
    await tester.enterText(find.byType(TextField), 'FILTER');
    await tester.pump();

    expect(find.text('Fix the login bug'), findsNothing);
    expect(find.text('Add history filter'), findsOneWidget);

    await tester.tap(find.text('Load more'));
    await tester.pump();
    await tester.pump();

    expect(find.text('Filter the logs'), findsOneWidget);
    expect(find.text('Fix the login bug'), findsNothing);

    await tester.tap(find.byTooltip('Clear filter'));
    await tester.pump();

    expect(find.text('Fix the login bug'), findsOneWidget);
    expect(find.text('My Project'), findsOneWidget);
  });

  testWidgets('shows a no-match state when the filter hides every session',
      (tester) async {
    final repo = _FakeHistoryRepository([
      HistorySessionPage(
        items: [_session('sess-1', title: 'A Session')],
        hasMore: false,
      ),
    ]);

    await tester.pumpWidget(_app(repo));
    await tester.pump();
    await tester.pump();

    await tester.tap(find.byTooltip('Filter sessions'));
    await tester.pump();
    await tester.enterText(find.byType(TextField), 'zzz');
    await tester.pump();

    expect(find.text('No sessions match.'), findsOneWidget);
  });

  testWidgets('groups sessions under day headers', (tester) async {
    final now = DateTime.now();
    await _pumpSessions(tester, [
      _session('a', title: 'Today work', lastActivity: now.toIso8601String()),
      _session('b',
          title: 'Older work',
          lastActivity:
              now.subtract(const Duration(days: 1)).toIso8601String()),
    ]);

    expect(find.text('TODAY'), findsOneWidget);
    expect(find.text('YESTERDAY'), findsOneWidget);
  });

  testWidgets('shows turns and duration in the meta line', (tester) async {
    await _pumpSessions(tester, [
      _session('a', title: 'A', turnCount: 12, durationMs: 34 * 60000),
    ]);

    expect(find.text('12 turns · 34m'), findsOneWidget);
  });

  testWidgets('names the agent only when the project mixes agents',
      (tester) async {
    await _pumpSessions(tester, [
      _session('a', title: 'A', turnCount: 1, agent: 'claude'),
      _session('b', title: 'B', turnCount: 1, agent: 'codex'),
    ]);

    expect(find.text('1 turn · Claude'), findsOneWidget);
    expect(find.text('1 turn · Codex'), findsOneWidget);
  });

  testWidgets('leaves the agent out when every session has the same agent',
      (tester) async {
    await _pumpSessions(tester, [
      _session('a', title: 'A', turnCount: 2, agent: 'claude'),
    ]);

    expect(find.text('2 turns'), findsOneWidget);
  });

  testWidgets('falls back to the first message with a quote mark',
      (tester) async {
    await _pumpSessions(tester, [
      _session('sess-1', firstMessage: 'check the e2e nodes'),
    ]);

    expect(find.textContaining('“check the e2e nodes'), findsOneWidget);
    expect(find.text('sess-1'), findsNothing);
  });

  testWidgets('puts sessions with no time under a no-date header',
      (tester) async {
    await _pumpSessions(tester, [
      _session('a',
          title: 'Dated', lastActivity: DateTime.now().toIso8601String()),
      _session('b', title: 'A Session'),
    ]);

    expect(find.text('A Session'), findsOneWidget);
    expect(find.text('TODAY'), findsOneWidget);
    expect(find.text('NO DATE'), findsOneWidget);
    expect(
        tester.getTopLeft(find.text('NO DATE')).dy,
        lessThan(tester.getTopLeft(find.text('A Session')).dy));
  });

  testWidgets('shows no meta line when there is nothing to say',
      (tester) async {
    await _pumpSessions(tester, [_session('a', title: 'A Session')]);

    expect(find.textContaining('turn'), findsNothing);
    expect(find.textContaining(' · '), findsNothing);
  });

  testWidgets('tapping the project name opens the project details',
      (tester) async {
    await _pumpSessions(tester, [_session('a', title: 'A')]);

    await tester.tap(find.byKey(const Key('history-title')));
    await tester.pumpAndSettle();

    expect(find.text('/home/user/project'), findsOneWidget);
    expect(find.textContaining('2 sessions'), findsOneWidget);
  });

  testWidgets('tapping the project path copies it and keeps the sheet open',
      (tester) async {
    final copied = <String>[];
    tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
      SystemChannels.platform,
      (call) async {
        if (call.method == 'Clipboard.setData') {
          copied.add((call.arguments as Map)['text'] as String);
        }
        return null;
      },
    );
    addTearDown(
      () => tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
        SystemChannels.platform,
        null,
      ),
    );
    await _pumpSessions(tester, [_session('a', title: 'A')]);
    await tester.tap(find.byKey(const Key('history-title')));
    await tester.pumpAndSettle();

    await tester.tap(find.text('/home/user/project'));
    await tester.pump();
    expect(copied, ['/home/user/project']);
    expect(find.byType(BottomSheet), findsOneWidget);
    expect(find.text('Copied'), findsOneWidget);
    await tester.pumpAndSettle(const Duration(seconds: 2));
    expect(find.text('/home/user/project'), findsOneWidget);
  });

  testWidgets('tapping a day header folds and unfolds that day',
      (tester) async {
    final now = DateTime.now();
    await _pumpSessions(tester, [
      _session('a', title: 'Today work', lastActivity: now.toIso8601String()),
      _session('b',
          title: 'Older work',
          lastActivity:
              now.subtract(const Duration(days: 1)).toIso8601String()),
    ]);

    await tester.tap(find.byKey(const Key('history-day-Today')));
    await tester.pump();
    expect(find.text('Today work'), findsNothing);
    expect(find.text('Older work'), findsOneWidget);

    await tester.tap(find.byKey(const Key('history-day-Today')));
    await tester.pump();
    expect(find.text('Today work'), findsOneWidget);
  });

  testWidgets('an active filter shows matches inside folded days',
      (tester) async {
    await _pumpSessions(tester, [
      _session('a',
          title: 'Today work', lastActivity: DateTime.now().toIso8601String()),
    ]);

    await tester.tap(find.byKey(const Key('history-day-Today')));
    await tester.pump();
    await tester.tap(find.byTooltip('Filter sessions'));
    await tester.pump();
    await tester.enterText(find.byType(TextField), 'work');
    await tester.pump();

    expect(find.text('Today work'), findsOneWidget);
  });

  testWidgets('day headers have no node icon', (tester) async {
    await _pumpSessions(tester, [
      _session('a', title: 'A', lastActivity: DateTime.now().toIso8601String()),
    ]);

    expect(find.byIcon(Icons.dns_outlined), findsNothing);
    expect(find.byKey(const Key('history-day-Today')), findsOneWidget);
  });

  testWidgets('day header label sits in the middle of its tap area',
      (tester) async {
    await _pumpSessions(tester, [
      _session('a', title: 'A', lastActivity: DateTime.now().toIso8601String()),
    ]);

    final area = tester.getRect(find.descendant(
        of: find.byKey(const Key('history-day-Today')),
        matching: find.byType(InkWell)));
    final label = tester.getRect(find.text('TODAY'));
    expect(label.center.dy, closeTo(area.center.dy, 0.5));
  });
}
