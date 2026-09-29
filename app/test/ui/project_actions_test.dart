import 'dart:async';

import 'package:argus/models/enums.dart';
import 'package:argus/models/project.dart';
import 'package:argus/models/project_sources.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/navigation.dart';
import 'package:argus/state/projects_api.dart';
import 'package:argus/state/sessions.dart';
import 'package:argus/transport/jsonrpc.dart';
import 'package:argus/ui/project_actions.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_riverpod/misc.dart';
import 'package:flutter_test/flutter_test.dart';

import '../support/fake_gateway_client.dart';

const _main = WorkspaceNode(
  id: 'A:w1',
  dir: '/src/argus',
  branch: 'main',
  isMain: true,
);
const _ws = WorkspaceNode(
  id: 'A:w2',
  dir: '/src/argus/.wt/reg',
  branch: 'feat/reg',
);
const _p = ProjectNode(
  id: 'A:p1',
  name: 'argus',
  nodeId: 'A',
  teardownScript: 'make clean',
  workspaces: [_main, _ws],
);

Session _live(String id, String ws) => Session(
  id: id,
  agent: 'claude',
  tmux: const TmuxLocation(
    server: TmuxServerKind.default_,
    paneId: '',
    sessionName: '',
    windowIndex: 0,
    currentPath: '',
  ),
  status: SessionStatus.working,
  source: SessionSource.discovered,
  workspaceId: ws,
);

/// Pumps a button that starts [action]. [show] removes the button, to test an
/// action whose launching widget is gone.
Future<(ProviderContainer, FakeGatewayClient)> _pump(
  WidgetTester tester,
  Future<void> Function(ActionContext ax) action, {
  Future<Object?> Function(String m, Object? p)? handler,
  ValueNotifier<bool>? show,
  List<Override> extra = const [],
}) async {
  final client = FakeGatewayClient(handler ?? (m, p) async => null);
  final c = ProviderContainer(
    overrides: [
      gatewayProvider.overrideWithValue(null),
      projectsApiProvider.overrideWithValue(ProjectsApi(() => client)),
      ...extra,
    ],
  );
  addTearDown(c.dispose);
  final visible = show ?? ValueNotifier(true);
  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: c,
      child: MaterialApp(
        home: Scaffold(
          body: ValueListenableBuilder<bool>(
            valueListenable: visible,
            builder: (_, on, _) => on
                ? Builder(
                    builder: (ctx) => TextButton(
                      onPressed: () => action(ActionContext.of(ctx)),
                      child: const Text('go'),
                    ),
                  )
                : const SizedBox(),
          ),
        ),
      ),
    ),
  );
  return (c, client);
}

// Records compare a map field by identity, so check the method and the params
// apart.
void _only(FakeGatewayClient c, String method, Object? params) {
  expect(c.calls, hasLength(1));
  expect(c.calls.single.$1, method);
  expect(c.calls.single.$2, params);
}

void main() {
  testWidgets('rename: prefilled, blank or unchanged disables Save', (
    tester,
  ) async {
    final (_, client) = await _pump(tester, (ax) => renameProject(ax, _p));
    await tester.tap(find.text('go'));
    await tester.pumpAndSettle();
    expect(find.widgetWithText(TextField, 'argus'), findsOneWidget);
    FilledButton save() =>
        tester.widget(find.widgetWithText(FilledButton, 'Save'));
    expect(save().onPressed, isNull);
    await tester.enterText(find.byType(TextField), '   ');
    await tester.pump();
    expect(save().onPressed, isNull);
    await tester.enterText(find.byType(TextField), ' argus2 ');
    await tester.pump();
    await tester.tap(find.text('Save'));
    await tester.pumpAndSettle();
    _only(client, 'project.rename', {'project_id': 'A:p1', 'name': 'argus2'});
    expect(find.text('renamed to argus2'), findsOneWidget);
  });

  testWidgets('a refused call reports the RPC message', (tester) async {
    await _pump(
      tester,
      (ax) => togglePinned(ax, _p),
      handler: (m, p) async => throw const RpcError(-32600, 'node offline'),
    );
    await tester.tap(find.text('go'));
    await tester.pumpAndSettle();
    expect(find.text('pin failed: node offline'), findsOneWidget);
  });

  testWidgets('hide adds the show-hidden hint', (tester) async {
    await _pump(tester, (ax) => toggleHidden(ax, _p));
    await tester.tap(find.text('go'));
    await tester.pumpAndSettle();
    expect(
      find.text('hid argus · Show hidden in the ⋮ menu shows it'),
      findsOneWidget,
    );
  });

  testWidgets('forget refuses while sessions are live', (tester) async {
    final (c, client) = await _pump(tester, (ax) => forgetProject(ax, _p));
    c.read(sessionsProvider.notifier).replaceAll([_live('s1', 'A:w2')]);
    await tester.tap(find.text('go'));
    await tester.pumpAndSettle();
    expect(
      find.text('argus has 1 live session · kill it first'),
      findsOneWidget,
    );
    expect(client.calls, isEmpty);
  });

  testWidgets('remove confirms with branch and teardown, then reports', (
    tester,
  ) async {
    final (_, client) = await _pump(
      tester,
      (ax) => removeWorkspace(ax, _p, _ws),
      handler: (m, p) async => {'warning': ''},
    );
    await tester.tap(find.text('go'));
    await tester.pumpAndSettle();
    expect(find.text('Remove workspace reg (feat/reg)?'), findsOneWidget);
    expect(find.text('Runs teardown: make clean'), findsOneWidget);
    expect(find.text('Uncommitted changes are lost.'), findsNothing);
    await tester.tap(find.widgetWithText(FilledButton, 'Remove'));
    await tester.pumpAndSettle();
    _only(client, 'workspace.remove', {'workspace_id': 'A:w2'});
    expect(find.text('removed reg'), findsOneWidget);
  });

  testWidgets('force remove warns and reports the teardown warning', (
    tester,
  ) async {
    final (_, client) = await _pump(
      tester,
      (ax) => removeWorkspace(ax, _p, _ws, force: true),
      handler: (m, p) async => {'warning': 'teardown exited 1'},
    );
    await tester.tap(find.text('go'));
    await tester.pumpAndSettle();
    expect(find.text('Force-remove workspace reg (feat/reg)?'), findsOneWidget);
    expect(find.text('Uncommitted changes are lost.'), findsOneWidget);
    await tester.tap(find.widgetWithText(FilledButton, 'Force remove'));
    await tester.pumpAndSettle();
    _only(client, 'workspace.remove', {'workspace_id': 'A:w2', 'force': true});
    expect(find.text('removed reg · teardown exited 1'), findsOneWidget);
  });

  testWidgets('cancel sends nothing', (tester) async {
    final (_, client) = await _pump(
      tester,
      (ax) => removeWorkspace(ax, _p, _ws),
    );
    await tester.tap(find.text('go'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Cancel'));
    await tester.pumpAndSettle();
    expect(client.calls, isEmpty);
  });

  testWidgets('an action completes after its launching widget is gone', (
    tester,
  ) async {
    final show = ValueNotifier(true);
    final (_, client) = await _pump(
      tester,
      (ax) => renameProject(ax, _p),
      show: show,
    );
    await tester.tap(find.text('go'));
    await tester.pumpAndSettle();
    show.value = false;
    await tester.pump();
    expect(find.text('go'), findsNothing);
    await tester.enterText(find.byType(TextField), 'kept');
    await tester.pump();
    await tester.tap(find.text('Save'));
    await tester.pumpAndSettle();
    expect(client.calls.single.$1, 'project.rename');
    expect(find.text('renamed to kept'), findsOneWidget);
  });

  testWidgets('change target picks a branch and reports', (tester) async {
    final (_, client) = await _pump(
      tester,
      (ax) => changeTarget(ax, _p, _ws),
      extra: [
        projectBranchesProvider('A:p1').overrideWith(
          (ref) async => const [
            BranchInfo(name: 'main'),
            BranchInfo(name: 'dev'),
          ],
        ),
      ],
    );
    await tester.tap(find.text('go'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('dev'));
    await tester.pumpAndSettle();
    _only(client, 'workspace.setTarget', {
      'workspace_id': 'A:w2',
      'target_branch': 'dev',
    });
    expect(find.text('target of reg → dev'), findsOneWidget);
  });

  test('project actions by project state', () {
    expect(projectActionsFor(_p), ProjectAction.values);
    const plain = ProjectNode(
      id: 'p',
      name: 'n',
      kind: 'plain',
      workspaces: [_main],
    );
    expect(
      projectActionsFor(plain),
      isNot(contains(ProjectAction.newWorkspace)),
    );
    const noMain = ProjectNode(id: 'p', name: 'n', workspaces: [_ws]);
    expect(
      projectActionsFor(noMain),
      isNot(contains(ProjectAction.newSession)),
    );
    const gone = ProjectNode(
      id: 'p',
      name: 'n',
      isGone: true,
      workspaces: [_main],
    );
    expect(projectActionsFor(gone), [ProjectAction.forget]);
    expect(projectActionLabel(_p, ProjectAction.pin), 'Pin');
    expect(
      projectActionLabel(
        const ProjectNode(id: 'p', name: 'n', pinned: true),
        ProjectAction.pin,
      ),
      'Unpin',
    );
    expect(
      projectActionLabel(
        const ProjectNode(id: 'p', name: 'n', hidden: true),
        ProjectAction.hide,
      ),
      'Unhide',
    );
  });

  test('workspace actions by workspace state', () {
    expect(workspaceActionsFor(_p, _main), [
      WorkspaceAction.newSession,
      WorkspaceAction.changeTarget,
    ]);
    const withSetup = ProjectNode(
      id: 'p',
      name: 'n',
      setupScript: 'make',
      workspaces: [_ws],
    );
    const ran = WorkspaceNode(
      id: 'A:w2',
      dir: '/x',
      setup: SetupRun(state: 'failed'),
    );
    expect(workspaceActionsFor(withSetup, ran), WorkspaceAction.values);
    const plain = ProjectNode(id: 'p', name: 'n', kind: 'plain');
    expect(workspaceActionsFor(plain, _ws), [
      WorkspaceAction.newSession,
      WorkspaceAction.remove,
      WorkspaceAction.forceRemove,
    ]);
    const gone = WorkspaceNode(id: 'A:w3', dir: '/y', isGone: true);
    expect(workspaceActionsFor(_p, gone), [
      WorkspaceAction.remove,
      WorkspaceAction.forceRemove,
    ]);
    const goneMain = WorkspaceNode(
      id: 'A:w1',
      dir: '/z',
      isMain: true,
      isGone: true,
    );
    expect(workspaceActionsFor(_p, goneMain), isEmpty);
  });

  testWidgets('a second remove while one runs is refused', (tester) async {
    final gate = Completer<Object?>();
    final (c, client) = await _pump(
      tester,
      (ax) => removeWorkspace(ax, _p, _ws),
      handler: (m, p) => gate.future,
    );
    await tester.tap(find.text('go'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(FilledButton, 'Remove'));
    await tester.pump();
    expect(c.read(removingProvider), {'A:w2'});
    expect(c.read(expectedGoneProvider), {'A:w2'});
    await tester.tap(find.text('go'));
    await tester.pumpAndSettle();
    expect(find.text('already removing reg'), findsOneWidget);
    expect(client.calls, hasLength(1));
    gate.complete({'warning': ''});
    await tester.pumpAndSettle();
    expect(c.read(removingProvider), isEmpty);
  });

  testWidgets('a failed remove or forget clears the expected-gone ids', (
    tester,
  ) async {
    final (c, _) = await _pump(tester, (ax) async {
      await removeWorkspace(ax, _p, _ws);
      await forgetProject(ax, _p);
    }, handler: (m, p) async => throw const RpcError(-32600, 'dirty'));
    await tester.tap(find.text('go'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(FilledButton, 'Remove'));
    await tester.pumpAndSettle();
    expect(c.read(expectedGoneProvider), isEmpty);
    expect(c.read(removingProvider), isEmpty);
    await tester.tap(find.widgetWithText(FilledButton, 'Forget'));
    await tester.pumpAndSettle();
    expect(c.read(expectedGoneProvider), isEmpty);
  });

  testWidgets('forget expects every workspace of the project to go', (
    tester,
  ) async {
    final gate = Completer<Object?>();
    final (c, _) = await _pump(
      tester,
      (ax) => forgetProject(ax, _p),
      handler: (m, p) => gate.future,
    );
    await tester.tap(find.text('go'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(FilledButton, 'Forget'));
    await tester.pump();
    expect(c.read(expectedGoneProvider), {'A:w1', 'A:w2'});
    gate.complete(null);
    await tester.pumpAndSettle();
  });
}
