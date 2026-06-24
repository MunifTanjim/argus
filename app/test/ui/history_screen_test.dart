import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/core/result.dart';
import 'package:argus/data/history_repository.dart';
import 'package:argus/models/history.dart';
import 'package:argus/models/chunk.dart';
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
  Future<Result<List<Chunk>>> transcript({
    String? nodeId,
    required String transcriptPath,
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
  Future<Result<List<Chunk>>> transcript({
    String? nodeId,
    required String transcriptPath,
  }) async =>
      const Result.ok([]);
}

HistoryProject _project(String label, {String? nodeLabel}) => HistoryProject(
      projectDir: '/home/user/$label',
      cwd: '/home/user/$label',
      label: label,
      sessionCount: 1,
      lastActivity: '',
      nodeLabel: nodeLabel,
    );

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
}
