import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'package:argus/core/result.dart';
import 'package:argus/data/session_repository.dart';
import 'package:argus/models/history.dart';
import 'package:argus/state/grouping.dart';
import 'package:argus/state/history_view_model.dart';
import 'package:argus/ui/spawn_dialog.dart';
import '../support/fake_session_repository.dart';

class _FakeProjects extends HistoryProjectsViewModel {
  _FakeProjects(this._items);
  final List<HistoryProject> _items;
  @override
  Future<List<HistoryProject>> build() async => _items;
}

class _RecordingSpawnRepo extends FakeSessionRepository {
  String? spawnedPrompt;
  @override
  Future<Result<void>> spawn({
    String? nodeId,
    String? cwd,
    required String prompt,
  }) async {
    spawnedPrompt = prompt;
    return const Result.ok(null);
  }
}

class _NodesRepo extends FakeSessionRepository {
  _NodesRepo(this._nodes);
  final List<NodeRef> _nodes;
  @override
  Future<Result<List<NodeRef>>> nodes() async => Result.ok(_nodes);
}

class _FailingSpawnRepo extends FakeSessionRepository {
  @override
  Future<Result<void>> spawn({
    String? nodeId,
    String? cwd,
    required String prompt,
  }) async =>
      const Result.error('backend error');
}

Widget _app(List<HistoryProject> projects) => ProviderScope(
      overrides: [
        historyProjectsProvider.overrideWith(() => _FakeProjects(projects)),
      ],
      child: const MaterialApp(home: Scaffold(body: SpawnDialogBody())),
    );

Widget _appWithRepo(SessionRepository repo) => ProviderScope(
      overrides: [
        historyProjectsProvider.overrideWith(() => _FakeProjects(const [])),
        sessionRepositoryProvider.overrideWithValue(repo),
      ],
      child: const MaterialApp(home: Scaffold(body: SpawnDialogBody())),
    );

void main() {
  testWidgets('Spawn is disabled until a prompt is entered', (tester) async {
    await tester.pumpWidget(_app(const []));
    await tester.pumpAndSettle();

    final spawnFinder = find.widgetWithText(TextButton, 'Spawn');
    expect(tester.widget<TextButton>(spawnFinder).onPressed, isNull);

    await tester.enterText(find.byKey(const Key('spawn-prompt')), 'do the thing');
    await tester.pump();
    expect(tester.widget<TextButton>(spawnFinder).onPressed, isNotNull);
  });

  testWidgets('name and command fields are gone', (tester) async {
    await tester.pumpWidget(_app(const []));
    await tester.pumpAndSettle();
    // Use the labels that actually existed before this change.
    expect(find.widgetWithText(TextField, 'Name'), findsNothing);
    expect(find.widgetWithText(TextField, 'Command'), findsNothing);
    // The only text field is the prompt; any re-added field would trip this.
    expect(find.byType(TextField), findsOneWidget);
    expect(find.byKey(const Key('spawn-prompt')), findsOneWidget);
  });

  testWidgets('directory picker still lists projects', (tester) async {
    await tester.pumpWidget(_app([
      const HistoryProject(
          projectDir: '/a', cwd: '/home/u/argus', label: 'argus',
          sessionCount: 1, lastActivity: '2026-06-29T10:00:00Z'),
    ]));
    await tester.pumpAndSettle();
    await tester.tap(find.byType(DropdownButton<String>).last);
    await tester.pumpAndSettle();
    expect(find.text('argus'), findsWidgets);
    expect(find.text('Custom path…'), findsWidgets);
  });

  testWidgets('duplicate project cwds do not crash the picker', (tester) async {
    // Same cwd on two nodes would trip DropdownButton's one-item-per-value
    // assertion if not deduped.
    await tester.pumpWidget(_app([
      const HistoryProject(
          projectDir: '/a', cwd: '/home/u/argus', label: 'argus',
          sessionCount: 1, lastActivity: '2026-06-29T10:00:00Z', nodeId: 'n1'),
      const HistoryProject(
          projectDir: '/a', cwd: '/home/u/argus', label: 'argus',
          sessionCount: 1, lastActivity: '2026-06-29T10:00:00Z', nodeId: 'n2'),
    ]));
    await tester.pumpAndSettle();

    expect(tester.takeException(), isNull);
    await tester.tap(find.byType(DropdownButton<String>).last);
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
  });

  testWidgets('spawn fires with the typed prompt', (tester) async {
    final repo = _RecordingSpawnRepo();
    await tester.pumpWidget(_appWithRepo(repo));
    await tester.pumpAndSettle();

    await tester.enterText(find.byKey(const Key('spawn-prompt')), 'the text');
    await tester.pump();

    await tester.tap(find.widgetWithText(TextButton, 'Spawn'));
    await tester.pumpAndSettle();

    expect(repo.spawnedPrompt, equals('the text'));
  });

  testWidgets('node without tmux disables spawn and shows a hint',
      (tester) async {
    final repo = _NodesRepo(const [NodeRef('n1', 'box', spawnSupported: false)]);
    await tester.pumpWidget(_appWithRepo(repo));
    await tester.pumpAndSettle();

    await tester.enterText(find.byKey(const Key('spawn-prompt')), 'do it');
    await tester.pump();

    // Even with a prompt, a non-tmux node can't spawn.
    final spawnFinder = find.widgetWithText(TextButton, 'Spawn');
    expect(tester.widget<TextButton>(spawnFinder).onPressed, isNull);
    expect(find.textContaining('tmux is not available'), findsOneWidget);
  });

  testWidgets('error path shows a SnackBar and keeps the dialog open',
      (tester) async {
    final repo = _FailingSpawnRepo();
    await tester.pumpWidget(_appWithRepo(repo));
    await tester.pumpAndSettle();

    await tester.enterText(find.byKey(const Key('spawn-prompt')), 'anything');
    await tester.pump();

    await tester.tap(find.widgetWithText(TextButton, 'Spawn'));
    await tester.pumpAndSettle();

    expect(find.textContaining('Failed'), findsOneWidget);
    expect(find.byType(SpawnDialogBody), findsOneWidget);
  });
}
