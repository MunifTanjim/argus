import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/core/result.dart';
import 'package:argus/data/history_repository.dart';
import 'package:argus/models/history.dart';
import 'package:argus/models/entry.dart';
import 'package:argus/ui/history_screen.dart';

class _FakeHistoryRepository implements HistoryRepository {
  _FakeHistoryRepository(this._projects);

  final List<HistoryProject> _projects;

  @override
  Future<Result<List<HistoryProject>>> projects() async =>
      Result.ok(_projects);

  @override
  Future<Result<HistorySessionPage>> sessions({
    String? nodeId,
    required String projectDir,
    required int limit,
    required int offset,
  }) async =>
      const Result.ok(HistorySessionPage(items: [], hasMore: false));

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
  Future<Result<List<HistoryProject>>> projects() async =>
      Result.error(Exception('projects error'));

  @override
  Future<Result<HistorySessionPage>> sessions({
    String? nodeId,
    required String projectDir,
    required int limit,
    required int offset,
  }) async =>
      const Result.ok(HistorySessionPage(items: [], hasMore: false));

  @override
  Future<Result<List<Entry>>> transcript({
    String? nodeId,
    required String transcriptPath,
    String? agentId,
    String? agent,
  }) async =>
      const Result.ok([]);
}

HistoryProject _project(
  String label, {
  String? nodeLabel,
  String? nodeId,
  bool missing = false,
  int sessionCount = 1,
  String lastActivity = '',
}) =>
    HistoryProject(
      projectDir: '/home/user/$label',
      cwd: '/home/user/$label',
      label: label,
      sessionCount: sessionCount,
      lastActivity: lastActivity,
      nodeId: nodeId,
      nodeLabel: nodeLabel,
      missing: missing,
    );

Future<void> _pumpApp(WidgetTester tester, List<HistoryProject> projects) async {
  await tester.pumpWidget(_app(_FakeHistoryRepository(projects)));
  await tester.pump();
  await tester.pump();
}

Widget _app(HistoryRepository repo) => ProviderScope(
      overrides: [historyRepositoryProvider.overrideWithValue(repo)],
      child: const MaterialApp(home: HistoryScreen()),
    );

void main() {
  testWidgets('renders both project labels when two projects returned',
      (tester) async {
    final repo = _FakeHistoryRepository([
      _project('Alpha'),
      _project('Beta'),
    ]);

    await tester.pumpWidget(_app(repo));
    await tester.pump(); // build() fires
    await tester.pump(); // async data resolves

    expect(find.text('Alpha'), findsOneWidget);
    expect(find.text('Beta'), findsOneWidget);
  });

  testWidgets('shows empty-state text when projects list is empty',
      (tester) async {
    final repo = _FakeHistoryRepository([]);

    await tester.pumpWidget(_app(repo));
    await tester.pump();
    await tester.pump();

    expect(find.text('No past sessions found.'), findsOneWidget);
  });

  testWidgets('shows error text when projects() fails', (tester) async {
    await tester.pumpWidget(_app(_ThrowingHistoryRepository()));
    await tester.pumpAndSettle();

    expect(find.textContaining('projects error'), findsOneWidget);
  });

  testWidgets('filters projects by path', (tester) async {
    final repo = _FakeHistoryRepository([
      _project('Alpha'),
      _project('Beta'),
    ]);

    await tester.pumpWidget(_app(repo));
    await tester.pump();
    await tester.pump();

    await tester.tap(find.byTooltip('Filter projects'));
    await tester.pump();
    await tester.enterText(find.byType(TextField), 'USER/BETA');
    await tester.pump();

    expect(find.text('Alpha'), findsNothing);
    expect(find.text('Beta'), findsOneWidget);

    await tester.enterText(find.byType(TextField), 'zzz');
    await tester.pump();

    expect(find.text('No projects match.'), findsOneWidget);

    await tester.tap(find.byTooltip('Clear filter'));
    await tester.pump();

    expect(find.text('Alpha'), findsOneWidget);
    expect(find.text('Beta'), findsOneWidget);
  });

  testWidgets('project row shows the session count and the short path',
      (tester) async {
    await _pumpApp(tester, [_project('Alpha', sessionCount: 42)]);

    expect(find.text('42 sessions · ~/Alpha'), findsOneWidget);
  });

  testWidgets('uses the singular for one session', (tester) async {
    await _pumpApp(tester, [_project('Alpha')]);

    expect(find.text('1 session · ~/Alpha'), findsOneWidget);
  });

  testWidgets('marks a project whose folder is missing', (tester) async {
    await _pumpApp(tester, [_project('Old', missing: true), _project('New')]);

    expect(find.byKey(const Key('history-missing-/home/user/Old')),
        findsOneWidget);
    expect(find.byKey(const Key('history-missing-/home/user/New')),
        findsNothing);
  });

  testWidgets('long-press shows the path', (tester) async {
    await _pumpApp(tester, [_project('Alpha')]);

    await tester.longPress(find.text('Alpha'));
    await tester.pumpAndSettle();

    expect(find.text('/home/user/Alpha'), findsOneWidget);
  });

  testWidgets('tapping a machine header folds and unfolds its projects',
      (tester) async {
    await _pumpApp(tester, [
      _project('Alpha', nodeId: 'n1', nodeLabel: 'MacBook'),
      _project('Beta', nodeId: 'n2', nodeLabel: 'devbox'),
    ]);

    await tester.tap(find.byKey(const Key('history-node-n1')));
    await tester.pump();
    expect(find.text('Alpha'), findsNothing);
    expect(find.text('Beta'), findsOneWidget);

    await tester.tap(find.byKey(const Key('history-node-n1')));
    await tester.pump();
    expect(find.text('Alpha'), findsOneWidget);
  });

  testWidgets('folds the group with no node like any other', (tester) async {
    await _pumpApp(tester, [_project('Alpha')]);

    await tester.tap(find.byKey(const Key('history-node-')));
    await tester.pump();

    expect(find.text('Alpha'), findsNothing);
  });

  testWidgets('an active filter shows matches inside folded groups',
      (tester) async {
    await _pumpApp(tester, [
      _project('Alpha', nodeId: 'n1', nodeLabel: 'MacBook'),
    ]);

    await tester.tap(find.byKey(const Key('history-node-n1')));
    await tester.pump();
    expect(find.text('Alpha'), findsNothing);

    await tester.tap(find.byTooltip('Filter projects'));
    await tester.pump();
    await tester.enterText(find.byType(TextField), 'Alp');
    await tester.pump();

    expect(find.text('Alpha'), findsOneWidget);
  });

  testWidgets('machine headers show the node icon', (tester) async {
    await _pumpApp(tester, [
      _project('Alpha', nodeId: 'n1', nodeLabel: 'MacBook'),
    ]);

    expect(
        find.descendant(
            of: find.byKey(const Key('history-node-n1')),
            matching: find.byIcon(Icons.dns_outlined)),
        findsOneWidget);
  });

  testWidgets('project details show the node icon by the machine',
      (tester) async {
    await _pumpApp(tester, [
      _project('Alpha', nodeId: 'n1', nodeLabel: 'MacBook'),
    ]);

    await tester.longPress(find.text('Alpha'));
    await tester.pumpAndSettle();

    final sheet = find.byType(BottomSheet);
    expect(find.descendant(of: sheet, matching: find.text('MacBook')),
        findsOneWidget);
    expect(
        find.descendant(of: sheet, matching: find.byIcon(Icons.dns_outlined)),
        findsOneWidget);
  });
}
