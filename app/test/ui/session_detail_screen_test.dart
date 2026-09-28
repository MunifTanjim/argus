import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_riverpod/misc.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/core/result.dart';
import 'package:argus/data/session_repository.dart';
import 'package:argus/data/transcript_repository.dart';
import 'package:argus/models/changes.dart';
import 'package:argus/models/chunk.dart';
import 'package:argus/models/project.dart';
import 'package:argus/models/session.dart';
import 'package:argus/models/workspace_files.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/navigation.dart';
import 'package:argus/state/projects.dart';
import 'package:argus/state/sessions.dart';
import 'package:argus/state/workspace.dart';
import 'package:argus/transport/connection.dart';
import 'package:argus/transport/gateway_client.dart';
import 'package:argus/transport/rpc_client.dart';
import 'package:argus/state/transcript.dart';
import 'package:argus/state/transcript_controller.dart';
import 'package:argus/ui/interaction_bar.dart';
import 'package:argus/ui/session_detail_screen.dart';
import 'package:argus/ui/workspace_screen.dart';
import '../support/fake_gateway_client.dart';
import '../support/fake_session_repository.dart';

Session _sStarting() => Session.fromJson({
      'id': 'mac:%1',
      'agent': 't',
      'status': 'starting',
      'source': 'discovered',
      'frontend': 'tmux',
      'tmux': {
        'server': 'argus',
        'pane_id': '%1',
        'session_name': 's',
        'window_index': 0,
        'current_path': '/p',
      },
      'repo': 'argus',
      'node_label': 'mac',
    });

Session _s({
  String? agentSessionId,
  String? name,
  String? branch,
  String? workspaceId,
}) =>
    Session.fromJson({
      'id': 'mac:%1',
      'agent': 't',
      'status': 'working',
      'source': 'hooked',
      'frontend': 'tmux',
      'tmux': {
        'server': 'argus',
        'pane_id': '%1',
        'session_name': 's',
        'window_index': 0,
        'current_path': '/p',
      },
      'repo': 'argus',
      'branch': ?branch,
      'workspace_id': ?workspaceId,
      'node_label': 'mac',
      'agent_session_id': ?agentSessionId,
      'name': ?name,
    });

class _SeededTranscript extends TranscriptNotifier {
  _SeededTranscript(this._seed);
  final List<Chunk> _seed;
  @override
  TranscriptState build() =>
      TranscriptState(subId: 'x', chunks: _seed, loaded: true);
}

class _NoopSub implements TranscriptSubscription {
  @override
  void dispose() {}
}

// Records the store each open() binds to, so a test can assert the screen
// re-opens onto the new key when the agent session id changes.
class _RecordingRepo implements TranscriptRepository {
  final List<TranscriptNotifier> stores = [];
  @override
  TranscriptSubscription? open({
    required String sessionId,
    String? agentId,
    required TranscriptNotifier store,
  }) {
    stores.add(store);
    return _NoopSub();
  }
}

class _FakeSessionControl extends FakeSessionRepository {
  final List<String> killedIds = [];

  @override
  Future<Result<void>> kill(String sessionId) async {
    killedIds.add(sessionId);
    return const Result.ok(null);
  }
}

const _ws = WorkspaceNode(
  id: 'A:w2',
  dir: '/src/argus/.worktrees/registry',
  branch: 'feat/registry',
  targetBranch: 'main',
);
const _project = ProjectNode(
  id: 'A:p1',
  name: 'argus',
  nodeId: 'A',
  workspaces: [_ws],
);

class _Projects extends ProjectsNotifier {
  @override
  ProjectsState build() =>
      const ProjectsState(projects: [_project], loaded: true);
}

class _Manager extends ConnectionManager {
  _Manager(this._client)
      : super(
          connect: () => throw UnimplementedError(),
          clientFactory: (incoming, send) =>
              RpcClient(incoming: incoming, sendFrame: send),
        );
  final GatewayClient _client;
  @override
  GatewayClient? get client => _client;
}

List<Override> _workspaceOverrides(FakeGatewayClient client) => [
      projectsProvider.overrideWith(_Projects.new),
      workspaceApiProvider.overrideWithValue(WorkspaceApi(() => client)),
      workspaceChangedFilesProvider(('A:w2', '')).overrideWith(
        (ref) async => const [ChangedFile(path: 'a.dart', change: 'modified')],
      ),
      workspaceCommitsProvider('A:w2')
          .overrideWith((ref) async => const CommitList()),
      workspaceDirProvider(('A:w2', '')).overrideWith(
        (ref) async => const DirListing(
          entries: [DirEntry(name: 'lib', path: 'lib', isDir: true)],
        ),
      ),
      workspaceDirProvider(('A:w2', 'lib')).overrideWith(
        (ref) async => const DirListing(
          path: 'lib',
          entries: [DirEntry(name: 'app.dart', path: 'lib/app.dart')],
        ),
      ),
    ];

Future<void> _openMenu(WidgetTester tester) async {
  await tester.tap(find.byType(PopupMenuButton<String>));
  await tester.pumpAndSettle();
}

Widget _app(List<Override> overrides, {Session? session}) => ProviderScope(
      overrides: overrides,
      child: MaterialApp(home: SessionDetailScreen(session: session ?? _s())),
    );

List<Override> _baseOverrides() => [
      gatewayProvider.overrideWithValue(null),
      transcriptProvider('mac:%1')
          .overrideWith(() => _SeededTranscript(const [])),
    ];

void main() {
  testWidgets('renders chunk feed from the store', (tester) async {
    await tester.pumpWidget(_app([
      gatewayProvider.overrideWithValue(null),
      transcriptProvider('mac:%1').overrideWith(() => _SeededTranscript(const [
            Chunk(id: 'u', kind: ChunkKind.user, text: 'hello world'),
          ])),
    ]));
    await tester.pump();
    expect(find.textContaining('hello world'), findsOneWidget);
  });

  testWidgets('empty state when no chunks', (tester) async {
    await tester.pumpWidget(_app([
      gatewayProvider.overrideWithValue(null),
      transcriptProvider('mac:%1')
          .overrideWith(() => _SeededTranscript(const [])),
    ]));
    await tester.pump();
    expect(find.textContaining('No transcript'), findsOneWidget);
  });

  testWidgets('shows the session name as an AppBar subtitle', (tester) async {
    await tester.pumpWidget(ProviderScope(
      overrides: _baseOverrides(),
      child:
          MaterialApp(home: SessionDetailScreen(session: _s(name: 'auth-refactor'))),
    ));
    await tester.pump();
    expect(find.text('argus'), findsOneWidget);
    expect(find.text('auth-refactor'), findsOneWidget);
  });

  testWidgets('shows terminal icon button in AppBar', (tester) async {
    await tester.pumpWidget(_app([
      gatewayProvider.overrideWithValue(null),
      transcriptProvider('mac:%1')
          .overrideWith(() => _SeededTranscript(const [])),
    ]));
    await tester.pump();
    expect(find.byIcon(Icons.terminal), findsOneWidget);
  });

  testWidgets('shows branch icon with branch name tooltip when set',
      (tester) async {
    await tester.pumpWidget(_app(_baseOverrides(),
        session: _s(branch: 'feat/session-git-branch')));
    await tester.pump();
    expect(find.byIcon(Icons.commit), findsOneWidget);
    expect(find.byTooltip('feat/session-git-branch'), findsOneWidget);
  });

  testWidgets('no branch icon when branch is absent', (tester) async {
    await tester.pumpWidget(_app(_baseOverrides()));
    await tester.pump();
    expect(find.byIcon(Icons.commit), findsNothing);
  });

  testWidgets('shows Changes and Files when the workspace is in the tree',
      (tester) async {
    await tester.pumpWidget(_app([
      ..._baseOverrides(),
      projectsProvider.overrideWith(_Projects.new),
    ], session: _s(workspaceId: 'A:w2')));
    await tester.pump();
    await _openMenu(tester);
    expect(find.text('Changes'), findsOneWidget);
    expect(find.text('Files'), findsOneWidget);
  });

  testWidgets('hides Changes and Files without a workspace id', (tester) async {
    await tester.pumpWidget(_app([
      ..._baseOverrides(),
      projectsProvider.overrideWith(_Projects.new),
    ], session: _s(branch: 'feat/registry')));
    await tester.pump();
    await _openMenu(tester);
    expect(find.text('Changes'), findsNothing);
    expect(find.text('Files'), findsNothing);
  });

  testWidgets('hides Changes and Files when the workspace is not in the tree',
      (tester) async {
    await tester.pumpWidget(_app([
      ..._baseOverrides(),
      projectsProvider.overrideWith(_Projects.new),
    ], session: _s(workspaceId: 'A:gone')));
    await tester.pump();
    await _openMenu(tester);
    expect(find.text('Changes'), findsNothing);
    expect(find.text('Files'), findsNothing);
  });

  testWidgets('Changes opens the workspace changes view', (tester) async {
    final client = FakeGatewayClient(
      (m, p) async => switch (m) {
        'workspace.diff' => {
            'path': 'a.dart',
            'diff': '@@ -1 +1 @@\n-old\n+new\n',
          },
        _ => null,
      },
    );
    await tester.pumpWidget(_app([
      ..._baseOverrides(),
      ..._workspaceOverrides(client),
    ], session: _s(workspaceId: 'A:w2')));
    await tester.pump();
    await _openMenu(tester);
    await tester.tap(find.text('Changes'));
    await tester.pumpAndSettle();

    expect(find.byType(WorkspaceChangesScreen), findsOneWidget);
    expect(find.text('registry'), findsOneWidget);
    expect(find.text('Uncommitted'), findsOneWidget);
    expect(find.text('vs main'), findsOneWidget);

    await tester.tap(find.textContaining('a.dart').first);
    await tester.pumpAndSettle();
    expect(find.textContaining('+ new'), findsOneWidget);
  });

  testWidgets('Files opens the browser; back closes it from any folder',
      (tester) async {
    await tester.pumpWidget(_app([
      ..._baseOverrides(),
      ..._workspaceOverrides(FakeGatewayClient((m, p) async => null)),
    ], session: _s(workspaceId: 'A:w2')));
    await tester.pump();
    await _openMenu(tester);
    await tester.tap(find.text('Files'));
    await tester.pumpAndSettle();

    expect(find.byType(WorkspaceFilesScreen), findsOneWidget);
    await tester.tap(find.text('lib'));
    await tester.pumpAndSettle();
    expect(find.text('app.dart'), findsOneWidget);
    final c = ProviderScope.containerOf(
        tester.element(find.byType(WorkspaceFilesScreen)));
    expect(c.read(filesPathProvider('A:w2')), 'lib');

    await tester.binding.handlePopRoute();
    await tester.pumpAndSettle();
    expect(find.byType(WorkspaceFilesScreen), findsNothing);
    expect(find.byType(SessionDetailScreen), findsOneWidget);
    // The folder stays remembered for the workspace Files tab.
    expect(c.read(filesPathProvider('A:w2')), 'lib');
  });

  testWidgets('pushed Changes follows the workspace in the tree',
      (tester) async {
    await tester.pumpWidget(_app([
      ..._baseOverrides(),
      ..._workspaceOverrides(FakeGatewayClient((m, p) async => null)),
    ], session: _s(workspaceId: 'A:w2')));
    await tester.pump();
    await _openMenu(tester);
    await tester.tap(find.text('Changes'));
    await tester.pumpAndSettle();
    expect(find.text('vs main'), findsOneWidget);

    final c = ProviderScope.containerOf(
        tester.element(find.byType(WorkspaceChangesScreen)));
    c.read(projectsProvider.notifier).state = const ProjectsState(
      projects: [
        ProjectNode(
          id: 'A:p1',
          name: 'argus',
          nodeId: 'A',
          workspaces: [
            WorkspaceNode(
              id: 'A:w2',
              dir: '/src/argus/.worktrees/registry',
              branch: 'feat/registry',
              targetBranch: 'dev',
            ),
          ],
        ),
      ],
      loaded: true,
    );
    await tester.pumpAndSettle();
    expect(find.text('vs dev'), findsOneWidget);

    c.read(projectsProvider.notifier).state =
        const ProjectsState(loaded: true);
    await tester.pumpAndSettle();
    expect(find.text('This workspace is no longer available.'), findsOneWidget);
  });

  group('Tasks item', () {
    late List<Map<String, dynamic>> tasks;
    late FakeGatewayClient client;

    setUp(() {
      tasks = [];
      client = FakeGatewayClient(
        (m, p) async => m == 'sessions.tasks' ? {'tasks': tasks} : null,
      );
    });

    Widget app() => _app([
          gatewayProvider.overrideWithValue(_Manager(client)),
          transcriptProvider('mac:%1')
              .overrideWith(() => _SeededTranscript(const [])),
          transcriptRepositoryProvider.overrideWithValue(_RecordingRepo()),
        ]);

    int taskCalls() =>
        client.calls.where((c) => c.$1 == 'sessions.tasks').length;

    testWidgets('is hidden when the list is empty', (tester) async {
      await tester.pumpWidget(app());
      await tester.pump();
      await _openMenu(tester);
      expect(find.text('Tasks'), findsNothing);
      expect(taskCalls(), 1);
    });

    testWidgets('is shown when the list is non-empty', (tester) async {
      tasks = [
        {'id': '1', 'subject': 'write tests', 'status': 'pending'},
      ];
      await tester.pumpWidget(app());
      await tester.pump();
      await _openMenu(tester);
      expect(find.text('Tasks'), findsOneWidget);
    });

    testWidgets('appears after tasks.changed fills the list', (tester) async {
      await tester.pumpWidget(app());
      await tester.pump();

      tasks = [
        {'id': '1', 'subject': 'write tests', 'status': 'pending'},
      ];
      client.notify('tasks.changed', {'session_id': 'other'});
      await tester.pump();
      expect(taskCalls(), 1);
      client.notify('tasks.changed', {'session_id': 'mac:%1'});
      await tester.pumpAndSettle();
      expect(taskCalls(), 2);

      await _openMenu(tester);
      expect(find.text('Tasks'), findsOneWidget);
    });
  });

  testWidgets('AppBar has overflow PopupMenuButton', (tester) async {
    await tester.pumpWidget(_app(_baseOverrides()));
    await tester.pump();
    expect(find.byType(PopupMenuButton<String>), findsOneWidget);
  });

  testWidgets('opening overflow menu shows Kill Session item', (tester) async {
    await tester.pumpWidget(_app(_baseOverrides()));
    await tester.pump();
    await tester.tap(find.byType(PopupMenuButton<String>));
    await tester.pumpAndSettle();
    expect(find.text('Kill Session'), findsOneWidget);
  });

  testWidgets('tapping Kill Session shows confirm AlertDialog', (tester) async {
    await tester.pumpWidget(_app(_baseOverrides()));
    await tester.pump();
    await tester.tap(find.byType(PopupMenuButton<String>));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Kill Session'));
    await tester.pumpAndSettle();
    expect(find.byType(AlertDialog), findsOneWidget);
    expect(find.text('Kill Session?'), findsOneWidget);
  });

  testWidgets('confirming kill calls SessionControl.kill with session id',
      (tester) async {
    final fake = _FakeSessionControl();
    await tester.pumpWidget(_app([
      ..._baseOverrides(),
      sessionRepositoryProvider.overrideWithValue(fake),
    ]));
    await tester.pump();
    await tester.tap(find.byType(PopupMenuButton<String>));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Kill Session'));
    await tester.pumpAndSettle();
    // Tap the destructive 'Kill' button in the dialog
    await tester.tap(find.text('Kill').last);
    await tester.pumpAndSettle();
    expect(fake.killedIds, contains('mac:%1'));
  });

  testWidgets('re-opens onto the new store when agent session id changes',
      (tester) async {
    final repo = _RecordingRepo();
    await tester.pumpWidget(_app([
      gatewayProvider.overrideWithValue(null),
      transcriptRepositoryProvider.overrideWithValue(repo),
      transcriptProvider('mac:%1') // fallback key, before any hook
          .overrideWith(() => _SeededTranscript(const [
                Chunk(id: 'pre', kind: ChunkKind.user, text: 'pre-clear line'),
              ])),
      transcriptProvider('c9') // post-clear store
          .overrideWith(() => _SeededTranscript(const [
                Chunk(id: 'post', kind: ChunkKind.user, text: 'post-clear line'),
              ])),
    ]));
    await tester.pump();
    expect(find.textContaining('pre-clear line'), findsOneWidget);
    expect(repo.stores, hasLength(1));

    // /clear arrives: same session id, new agent session id.
    final container =
        ProviderScope.containerOf(tester.element(find.byType(SessionDetailScreen)));
    container.read(sessionsProvider.notifier).replaceAll([
      _s(agentSessionId: 'c9'),
    ]);
    await tester.pump();

    expect(find.textContaining('post-clear line'), findsOneWidget);
    expect(find.textContaining('pre-clear line'), findsNothing);
    expect(repo.stores, hasLength(2)); // re-opened onto the fresh store
  });

  // opencode: paneless, input_mode api, so it accepts prompts and can be torn
  // down via the single Kill Session action.
  Session oc({String status = 'idle', String inputMode = 'api'}) =>
      Session.fromJson({
        'id': 'oc:%1',
        'agent': 'opencode',
        'status': status,
        'source': 'hooked',
        'frontend': 'external',
        'input_mode': inputMode,
        'tmux': {'pane_id': ''},
        'repo': 'proj',
        'node_label': 'mac',
        'interaction': {'kind': 'idle'},
      });

  Widget appFor(Session s, {List<Override> extra = const []}) => ProviderScope(
        overrides: [
          gatewayProvider.overrideWithValue(null),
          transcriptProvider(s.id)
              .overrideWith(() => _SeededTranscript(const [])),
          ...extra,
        ],
        child: MaterialApp(home: SessionDetailScreen(session: s)),
      );

  testWidgets('idle opencode session shows a tappable reply bar', (tester) async {
    await tester.pumpWidget(appFor(oc()));
    await tester.pump();
    expect(find.byType(InteractionBar), findsOneWidget);
    expect(find.text('Respond'), findsOneWidget);
    expect(
        find.text("argus can't send input to this session"), findsNothing);
  });

  testWidgets('idle decision-only session shows the respond-elsewhere notice',
      (tester) async {
    final vscode = Session.fromJson({
      'id': 'vs:%1',
      'agent': 'claude',
      'status': 'awaiting_input',
      'source': 'hooked',
      'frontend': 'vscode',
      'tmux': {'pane_id': ''},
      'repo': 'proj',
      'interaction': {'kind': 'idle'},
    });
    await tester.pumpWidget(appFor(vscode));
    await tester.pump();
    expect(
        find.text("argus can't send input to this session"), findsOneWidget);
  });

  testWidgets('Kill Session is enabled for an idle opencode session and calls kill',
      (tester) async {
    final fake = _FakeSessionControl();
    await tester.pumpWidget(appFor(oc(),
        extra: [sessionRepositoryProvider.overrideWithValue(fake)]));
    await tester.pump();
    await tester.tap(find.byType(PopupMenuButton<String>));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Kill Session'));
    await tester.pumpAndSettle();
    expect(find.byType(AlertDialog), findsOneWidget);
    await tester.tap(find.text('Kill').last);
    await tester.pumpAndSettle();
    expect(fake.killedIds, contains('oc:%1'));
  });

  testWidgets('Kill Session is enabled for a working opencode session',
      (tester) async {
    final fake = _FakeSessionControl();
    await tester.pumpWidget(appFor(oc(status: 'working'),
        extra: [sessionRepositoryProvider.overrideWithValue(fake)]));
    await tester.pump();
    await tester.tap(find.byType(PopupMenuButton<String>));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Kill Session'));
    await tester.pumpAndSettle();
    // Kill is unconditional: the confirm dialog opens and kill is sent.
    expect(find.byType(AlertDialog), findsOneWidget);
    await tester.tap(find.text('Kill').last);
    await tester.pumpAndSettle();
    expect(fake.killedIds, contains('oc:%1'));
  });

  testWidgets('starting session shows startup notice and no input bar',
      (tester) async {
    await tester.pumpWidget(_app(
      [
        gatewayProvider.overrideWithValue(null),
        transcriptProvider('mac:%1')
            .overrideWith(() => _SeededTranscript(const [])),
      ],
      session: _sStarting(),
    ));
    await tester.pump();
    expect(find.textContaining('startup prompt'), findsOneWidget);
    expect(find.byType(InteractionBar), findsNothing);
  });
}
