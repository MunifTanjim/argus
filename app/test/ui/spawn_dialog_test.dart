import 'dart:async';

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
  _RecordingSpawnRepo({this.agents = const <AgentInfo>[]});
  final List<AgentInfo> agents;
  String? spawnedPrompt;
  String? spawnedAgent;
  String? spawnedNode;
  String? spawnedCwd;
  @override
  Future<Result<void>> spawn({
    String? nodeId,
    String? cwd,
    String? agent,
    required String prompt,
  }) async {
    spawnedPrompt = prompt;
    spawnedAgent = agent;
    spawnedNode = nodeId;
    spawnedCwd = cwd;
    return const Result.ok(null);
  }

  @override
  Future<Result<List<AgentInfo>>> listAgents(String? nodeId) async =>
      Result.ok(agents);
}

class _CompletingRepo extends _RecordingSpawnRepo {
  _CompletingRepo(this.subdirs, {this.homes = const {}, this.latency});
  final Map<String, String> homes; // by node id ('' = sole node)
  final Duration? latency; // lets the field mount before the reply lands
  final Map<String, List<String>> subdirs; // by path
  final List<String> completedOn = [];
  final List<String> completedPaths = [];
  List<NodeRef> nodeList = const [];
  @override
  Future<Result<List<NodeRef>>> nodes() async => Result.ok(nodeList);
  @override
  Future<Result<DirCompletion>> completeDir(String? nodeId, String path) async {
    completedOn.add(nodeId ?? '');
    completedPaths.add(path);
    if (latency case final d?) await Future<void>.delayed(d);
    final home = homes[nodeId ?? ''] ?? '';
    final p = path.isEmpty ? '$home/' : path;
    final parent = p.substring(0, p.lastIndexOf('/') + 1);
    return Result.ok(DirCompletion(
        home: home, paths: [for (final n in subdirs[p] ?? const []) '$parent$n/']));
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
    String? agent,
    required String prompt,
  }) async =>
      const Result.error('backend error');
}

class _PendingSpawnRepo extends FakeSessionRepository {
  final done = Completer<Result<void>>();
  @override
  Future<Result<void>> spawn({
    String? nodeId,
    String? cwd,
    String? agent,
    required String prompt,
  }) =>
      done.future;
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

    final spawnFinder = find.widgetWithText(FilledButton, 'Spawn');
    expect(tester.widget<FilledButton>(spawnFinder).onPressed, isNull);

    await tester.enterText(find.byKey(const Key('spawn-prompt')), 'do the thing');
    await tester.pump();
    expect(tester.widget<FilledButton>(spawnFinder).onPressed, isNotNull);
  });

  testWidgets('name and command fields are gone', (tester) async {
    await tester.pumpWidget(_app(const [
      HistoryProject(
          projectDir: '/a', cwd: '/home/u/argus', label: 'argus',
          sessionCount: 1, lastActivity: '2026-06-29T10:00:00Z'),
    ]));
    await tester.pumpAndSettle();
    // Use the labels that actually existed before this change.
    expect(find.widgetWithText(TextField, 'Name'), findsNothing);
    expect(find.widgetWithText(TextField, 'Command'), findsNothing);
    // The only text field is the prompt; any re-added field would trip this.
    expect(find.byType(TextField), findsOneWidget);
    expect(find.byKey(const Key('spawn-prompt')), findsOneWidget);
  });

  testWidgets('directory picker shows paths, most recent first',
      (tester) async {
    await tester.pumpWidget(_app([
      const HistoryProject(
          projectDir: '/b', cwd: '/home/u/argus', label: 'argus',
          sessionCount: 1, lastActivity: '2026-06-29T10:00:00Z'),
      const HistoryProject(
          projectDir: '/a', cwd: '/home/u/old', label: 'old',
          sessionCount: 1, lastActivity: '2026-06-01T10:00:00Z'),
    ]));
    await tester.pumpAndSettle();
    // The most recent directory is the default, with its path.
    expect(find.text('Directory'), findsOneWidget);
    expect(find.text('argus'), findsOneWidget);
    expect(find.text('/home/u/argus'), findsOneWidget);

    await tester.tap(find.text('argus'));
    await tester.pumpAndSettle();
    expect(find.text('/home/u/old'), findsOneWidget);
    expect(find.text('Custom path…'), findsOneWidget);
    expect(tester.getTopLeft(find.text('argus')).dy,
        lessThan(tester.getTopLeft(find.text('old')).dy));

    await tester.tap(find.text('old'));
    await tester.pumpAndSettle();
    expect(find.text('/home/u/old'), findsOneWidget);
    expect(find.text('/home/u/argus'), findsNothing);
  });

  testWidgets('directories gone from the node are not offered',
      (tester) async {
    await tester.pumpWidget(_app([
      const HistoryProject(
          projectDir: '/a', cwd: '/home/u/gone', label: 'gone',
          sessionCount: 1, lastActivity: '2026-06-29T10:00:00Z',
          missing: true),
      const HistoryProject(
          projectDir: '/b', cwd: '/home/u/argus', label: 'argus',
          sessionCount: 1, lastActivity: '2026-06-01T10:00:00Z'),
    ]));
    await tester.pumpAndSettle();
    // The newer but missing directory is skipped as the default too.
    expect(find.text('argus'), findsOneWidget);
    await tester.tap(find.text('argus'));
    await tester.pumpAndSettle();
    expect(find.text('gone'), findsNothing);
  });

  testWidgets('directory filter matches the path', (tester) async {
    await tester.pumpWidget(_app([
      const HistoryProject(
          projectDir: '/a', cwd: '/home/u/argus', label: 'argus',
          sessionCount: 1, lastActivity: '2026-06-29T10:00:00Z'),
      const HistoryProject(
          projectDir: '/b', cwd: '/srv/web', label: 'web',
          sessionCount: 1, lastActivity: '2026-06-01T10:00:00Z'),
    ]));
    await tester.pumpAndSettle();
    await tester.tap(find.text('argus'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byKey(const Key('spawn-filter')), 'srv');
    await tester.pump();
    expect(find.text('argus'), findsNothing);
    expect(find.text('web'), findsOneWidget);
    expect(find.text('Custom path…'), findsOneWidget);
  });

  testWidgets('duplicate project cwds are listed once', (tester) async {
    await tester.pumpWidget(_app([
      const HistoryProject(
          projectDir: '/a', cwd: '/home/u/argus', label: 'argus',
          sessionCount: 1, lastActivity: '2026-06-29T10:00:00Z', nodeId: 'n1'),
      const HistoryProject(
          projectDir: '/a', cwd: '/home/u/argus', label: 'argus',
          sessionCount: 1, lastActivity: '2026-06-29T10:00:00Z', nodeId: 'n2'),
    ]));
    await tester.pumpAndSettle();
    await tester.tap(find.text('argus'));
    await tester.pumpAndSettle();
    expect(find.text('/home/u/argus'), findsOneWidget);
  });

  testWidgets('a custom path is sent as the cwd', (tester) async {
    final repo = _RecordingSpawnRepo();
    await tester.pumpWidget(_appWithRepo(repo));
    await tester.pumpAndSettle();
    await tester.enterText(
        find.byKey(const Key('spawn-custom-path')), '/tmp/x');
    await tester.enterText(find.byKey(const Key('spawn-prompt')), 'go');
    await tester.pump();
    await tester.tap(find.widgetWithText(FilledButton, 'Spawn'));
    await tester.pumpAndSettle();
    expect(repo.spawnedCwd, '/tmp/x');
  });

  testWidgets('a custom path starts at the node\'s home and completes',
      (tester) async {
    final repo = _CompletingRepo({
      '/home/u/': ['Dev', 'Docs', 'src'],
      '/home/u/D': ['Dev', 'Docs'],
    }, homes: {'': '/home/u'}, latency: const Duration(milliseconds: 500));
    await tester.pumpWidget(_appWithRepo(repo));
    await tester.pumpAndSettle();
    await tester.pump(const Duration(milliseconds: 500)); // the reply
    await tester.pumpAndSettle();
    final field = find.byKey(const Key('spawn-custom-path'));
    expect(tester.widget<TextField>(field).controller!.text, '/home/u/');
    expect(find.text('/home/u/src/'), findsOneWidget);
    expect(repo.completedPaths, ['']); // the seed's reply lists the home

    await tester.enterText(field, '/home/u/D');
    await tester.pump(const Duration(milliseconds: 300)); // the debounce
    await tester.pumpAndSettle();
    expect(find.text('/home/u/Dev/'), findsOneWidget);
    await tester.tap(find.text('/home/u/Docs/'));
    await tester.pumpAndSettle();
    expect(tester.widget<TextField>(field).controller!.text, '/home/u/Docs/');

    await tester.enterText(find.byKey(const Key('spawn-prompt')), 'go');
    await tester.pump();
    await tester.tap(find.widgetWithText(FilledButton, 'Spawn'));
    await tester.pumpAndSettle();
    expect(repo.spawnedCwd, '/home/u/Docs/');
  });

  testWidgets('a custom path asks the chosen node, not another',
      (tester) async {
    final repo = _CompletingRepo({}, homes: {'a': '/Users/me', 'b': '/home/b'})
      ..nodeList = const [NodeRef('a', 'A'), NodeRef('b', 'B')];
    await tester.pumpWidget(_appWithRepo(repo));
    await tester.pumpAndSettle();
    await tester.tap(find.text('A'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('B'));
    await tester.pumpAndSettle();
    final field = find.byKey(const Key('spawn-custom-path'));
    expect(tester.widget<TextField>(field).controller!.text, '/home/b/');
    expect(repo.completedOn.last, 'b');
  });

  testWidgets('a node with no directory history starts at a custom path',
      (tester) async {
    final repo = _CompletingRepo({}, homes: {'a': '/Users/me', 'b': '/home/b'})
      ..nodeList = const [NodeRef('a', 'A'), NodeRef('b', 'B')];
    await tester.pumpWidget(ProviderScope(
      overrides: [
        historyProjectsProvider.overrideWith(() => _FakeProjects(const [
              HistoryProject(
                  projectDir: '/a', cwd: '/Users/me/argus', label: 'argus',
                  sessionCount: 1, lastActivity: '2026-06-29T10:00:00Z',
                  nodeId: 'a'),
            ])),
        sessionRepositoryProvider.overrideWithValue(repo),
      ],
      child: const MaterialApp(home: Scaffold(body: SpawnDialogBody())),
    ));
    await tester.pumpAndSettle();
    final field = find.byKey(const Key('spawn-custom-path'));
    expect(find.text('argus'), findsOneWidget);
    expect(field, findsNothing);
    await tester.tap(find.text('A'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('B'));
    await tester.pumpAndSettle();
    expect(tester.widget<TextField>(field).controller!.text, '/home/b/');
  });

  testWidgets('a custom path asks for completions once typing pauses',
      (tester) async {
    final repo = _CompletingRepo({}, homes: {'': '/home/u'});
    await tester.pumpWidget(_appWithRepo(repo));
    await tester.pumpAndSettle();
    final asked = repo.completedPaths.length;
    final field = find.byKey(const Key('spawn-custom-path'));
    await tester.enterText(field, '/home/u/D');
    await tester.pump(const Duration(milliseconds: 100));
    await tester.enterText(field, '/home/u/De');
    await tester.pump(const Duration(milliseconds: 100));
    expect(repo.completedPaths.length, asked);
    await tester.pump(const Duration(milliseconds: 300));
    await tester.pumpAndSettle();
    expect(repo.completedPaths.sublist(asked), ['/home/u/De']);
  });

  testWidgets('spawn fires with the typed prompt', (tester) async {
    final repo = _RecordingSpawnRepo();
    await tester.pumpWidget(_appWithRepo(repo));
    await tester.pumpAndSettle();

    await tester.enterText(find.byKey(const Key('spawn-prompt')), 'the text');
    await tester.pump();

    await tester.tap(find.widgetWithText(FilledButton, 'Spawn'));
    await tester.pumpAndSettle();

    expect(repo.spawnedPrompt, equals('the text'));
  });

  testWidgets('single agent is auto-selected and sent without a picker',
      (tester) async {
    final repo = _RecordingSpawnRepo(agents: const [
      AgentInfo(id: 'codex', name: 'Codex', color: '#b8bb26', spawnable: true),
    ]);
    await tester.pumpWidget(_appWithRepo(repo));
    await tester.pumpAndSettle();

    // One agent → no picker.
    expect(find.text('Agent'), findsNothing);

    await tester.enterText(find.byKey(const Key('spawn-prompt')), 'go');
    await tester.pump();
    await tester.tap(find.widgetWithText(FilledButton, 'Spawn'));
    await tester.pumpAndSettle();

    expect(repo.spawnedAgent, equals('codex'));
  });

  testWidgets('agent picker appears with 2+ agents and the choice is sent',
      (tester) async {
    final repo = _RecordingSpawnRepo(agents: const [
      AgentInfo(id: 'claude', name: 'Claude', color: '#fe8019', spawnable: true),
      AgentInfo(id: 'codex', name: 'Codex', color: '#b8bb26', spawnable: true),
    ]);
    await tester.pumpWidget(_appWithRepo(repo));
    await tester.pumpAndSettle();

    // Default selection is the first agent.
    expect(find.text('Agent'), findsOneWidget);
    expect(find.text('Claude'), findsOneWidget);

    await tester.tap(find.text('Claude'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Codex'));
    await tester.pumpAndSettle();

    await tester.enterText(find.byKey(const Key('spawn-prompt')), 'go');
    await tester.pump();
    await tester.tap(find.widgetWithText(FilledButton, 'Spawn'));
    await tester.pumpAndSettle();

    expect(repo.spawnedAgent, equals('codex'));
  });

  testWidgets('node without tmux disables spawn and shows a hint',
      (tester) async {
    final repo = _NodesRepo(const [NodeRef('n1', 'box', spawnSupported: false)]);
    await tester.pumpWidget(_appWithRepo(repo));
    await tester.pumpAndSettle();

    await tester.enterText(find.byKey(const Key('spawn-prompt')), 'do it');
    await tester.pump();

    // Even with a prompt, a non-tmux node can't spawn.
    final spawnFinder = find.widgetWithText(FilledButton, 'Spawn');
    expect(tester.widget<FilledButton>(spawnFinder).onPressed, isNull);
    expect(find.textContaining('tmux is not available'), findsOneWidget);
  });

  testWidgets('error path shows a SnackBar and keeps the dialog open',
      (tester) async {
    final repo = _FailingSpawnRepo();
    await tester.pumpWidget(_appWithRepo(repo));
    await tester.pumpAndSettle();

    await tester.enterText(find.byKey(const Key('spawn-prompt')), 'anything');
    await tester.pump();

    await tester.tap(find.widgetWithText(FilledButton, 'Spawn'));
    await tester.pumpAndSettle();

    expect(find.textContaining('Failed'), findsOneWidget);
    expect(find.byType(SpawnDialogBody), findsOneWidget);
  });

  testWidgets('a fixed target hides the pickers and spawns there',
      (tester) async {
    final repo = _RecordingSpawnRepo(agents: const [
      AgentInfo(id: 'claude', name: 'Claude', color: '#d79921', spawnable: true),
    ]);
    await tester.pumpWidget(ProviderScope(
      overrides: [
        historyProjectsProvider.overrideWith(() => _FakeProjects(const [])),
        sessionRepositoryProvider.overrideWithValue(repo),
      ],
      child: const MaterialApp(
        home: Scaffold(
          body: SpawnDialogBody(
            target: SpawnTarget(
                nodeId: 'A', cwd: '/src/argus/.worktrees/registry', label: 'argus · registry'),
          ),
        ),
      ),
    ));
    await tester.pumpAndSettle();

    // Shown read-only, with the path.
    expect(find.text('argus · registry'), findsOneWidget);
    expect(find.text('/src/argus/.worktrees/registry'), findsOneWidget);
    expect(find.byIcon(Icons.chevron_right), findsNothing);
    await tester.enterText(find.byKey(const Key('spawn-prompt')), 'go');
    await tester.pump();
    await tester.tap(find.widgetWithText(FilledButton, 'Spawn'));
    await tester.pumpAndSettle();

    expect(repo.spawnedNode, 'A');
    expect(repo.spawnedCwd, '/src/argus/.worktrees/registry');
  });

  testWidgets('a target prompt fills the prompt field and is sent',
      (tester) async {
    final repo = _RecordingSpawnRepo(agents: const [
      AgentInfo(id: 'claude', name: 'Claude', color: '#d79921', spawnable: true),
    ]);
    await tester.pumpWidget(ProviderScope(
      overrides: [
        historyProjectsProvider.overrideWith(() => _FakeProjects(const [])),
        sessionRepositoryProvider.overrideWithValue(repo),
      ],
      child: const MaterialApp(
        home: Scaffold(
          body: SpawnDialogBody(
            target: SpawnTarget(
              nodeId: 'A',
              cwd: '/src/x',
              label: 'argus · x',
              prompt: 'Fix issue #3',
            ),
          ),
        ),
      ),
    ));
    await tester.pumpAndSettle();
    expect(find.text('Fix issue #3'), findsOneWidget);
    await tester.tap(find.widgetWithText(FilledButton, 'Spawn'));
    await tester.pumpAndSettle();
    expect(repo.spawnedPrompt, 'Fix issue #3');
  });

  testWidgets('the page stays open while a spawn runs', (tester) async {
    final repo = _PendingSpawnRepo();
    await tester.pumpWidget(ProviderScope(
      overrides: [
        historyProjectsProvider.overrideWith(() => _FakeProjects(const [])),
        sessionRepositoryProvider.overrideWithValue(repo),
      ],
      child: MaterialApp(
        home: Builder(
          builder: (context) => TextButton(
            onPressed: () => Navigator.of(context).push(spawnRoute()),
            child: const Text('open'),
          ),
        ),
      ),
    ));
    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byKey(const Key('spawn-prompt')), 'go');
    await tester.pump();
    await tester.tap(find.widgetWithText(FilledButton, 'Spawn'));
    await tester.pump();

    await tester.state<NavigatorState>(find.byType(Navigator)).maybePop();
    await tester.pump(const Duration(milliseconds: 500));
    await tester.pump();
    expect(find.text('open'), findsNothing); // the page still covers home

    repo.done.complete(const Result.ok(null));
    await tester.pumpAndSettle();
    expect(find.text('open'), findsOneWidget);
  });
}
