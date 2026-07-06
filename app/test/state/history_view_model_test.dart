// app/test/state/history_view_model_test.dart
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/core/result.dart';
import 'package:argus/data/history_repository.dart';
import 'package:argus/models/chunk.dart';
import 'package:argus/models/history.dart';
import 'package:argus/state/history_view_model.dart';

class _FakeRepo implements HistoryRepository {
  _FakeRepo(this.result);
  Result<List<HistoryProject>> result;
  int calls = 0;

  @override
  Future<Result<List<HistoryProject>>> projects() async {
    calls++;
    return result;
  }

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
    String? agentId,
    String? agent,
  }) async =>
      const Result.ok([]);
}

HistoryProject _project(String label) => HistoryProject(
      projectDir: '/p/$label',
      cwd: '/p/$label',
      label: label,
      sessionCount: 1,
      lastActivity: '',
    );

ProviderContainer _container(HistoryRepository repo) {
  final c = ProviderContainer(
    overrides: [historyRepositoryProvider.overrideWithValue(repo)],
  );
  addTearDown(c.dispose);
  return c;
}

void main() {
  test('exposes projects from the repository on build', () async {
    final repo = _FakeRepo(Result.ok([_project('Alpha')]));
    final c = _container(repo);

    final projects = await c.read(historyProjectsProvider.future);
    expect(projects.map((p) => p.label), ['Alpha']);
    expect(repo.calls, 1);
  });

  test('surfaces a repository Error as an AsyncError', () async {
    final repo = _FakeRepo(Result.error(Exception('boom')));
    final c = _container(repo);

    c.listen(historyProjectsProvider, (_, _) {});
    await Future<void>.delayed(const Duration(milliseconds: 10));

    final state = c.read(historyProjectsProvider);
    expect(state.hasError, isTrue);
    expect(state.error, isA<Exception>());
  });

  test('reload re-fetches and replaces the data', () async {
    final repo = _FakeRepo(Result.ok([_project('Alpha')]));
    final c = _container(repo);

    await c.read(historyProjectsProvider.future);
    expect(repo.calls, 1);

    repo.result = Result.ok([_project('Beta'), _project('Gamma')]);
    await c.read(historyProjectsProvider.notifier).reload();

    expect(repo.calls, 2);
    final value = c.read(historyProjectsProvider).requireValue;
    expect(value.map((p) => p.label), ['Beta', 'Gamma']);
  });
}
