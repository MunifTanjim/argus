import 'dart:async';

import 'package:argus/data/session_repository.dart';
import 'package:argus/models/project.dart';
import 'package:argus/models/project_sources.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/navigation.dart';
import 'package:argus/state/projects_api.dart';
import 'package:argus/transport/jsonrpc.dart';
import 'package:argus/ui/new_workspace_screen.dart';
import 'package:argus/ui/project_actions.dart';
import 'package:argus/ui/spawn_dialog.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../support/fake_gateway_client.dart';
import '../support/fake_session_repository.dart';

const _p = ProjectNode(
  id: 'A:p1',
  name: 'argus',
  nodeId: 'A',
  setupScript: 'make deps',
  workspaces: [
    WorkspaceNode(id: 'A:w1', dir: '/src/argus', isMain: true, branch: 'main'),
  ],
);

Future<(ProviderContainer, FakeGatewayClient)> _pump(
  WidgetTester tester, {
  Future<Object?> Function(String m, Object? p)? handler,
}) async {
  final client = FakeGatewayClient(
    handler ??
        (m, p) async => {'workspace_id': 'A:w9', 'dir': '/src/argus/.wt/new'},
  );
  final c = ProviderContainer(
    overrides: [
      gatewayProvider.overrideWithValue(null),
      sessionRepositoryProvider.overrideWithValue(FakeSessionRepository()),
      projectsApiProvider.overrideWithValue(ProjectsApi(() => client)),
      projectBranchesProvider('A:p1').overrideWith(
        (ref) async => const [
          BranchInfo(name: 'main', local: true, checkedOut: true),
          BranchInfo(name: 'feat/a', local: true),
        ],
      ),
      projectPrsProvider('A:p1').overrideWith(
        (ref) async => const SourceList([
          PrInfo(
            number: 7,
            title: 'Fix it',
            author: 'ann',
            headBranch: 'fix',
            baseBranch: 'dev',
          ),
        ], truncated: true),
      ),
      projectIssuesProvider('A:p1').overrideWith(
        (ref) async => const SourceList([
          IssueInfo(number: 3, title: 'Crash', author: 'bob'),
        ]),
      ),
    ],
  );
  addTearDown(c.dispose);
  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: c,
      child: MaterialApp(
        home: Scaffold(
          body: Builder(
            builder: (ctx) => TextButton(
              onPressed: () => openNewWorkspace(ActionContext.of(ctx), _p),
              child: const Text('go'),
            ),
          ),
        ),
      ),
    ),
  );
  await tester.tap(find.text('go'));
  await tester.pumpAndSettle();
  return (c, client);
}

List<(String, Object?)> _creates(FakeGatewayClient c) =>
    c.calls.where((x) => x.$1 == 'workspace.create').toList();

void main() {
  testWidgets('New tab: blank disables Create; create closes and opens it', (
    tester,
  ) async {
    final (c, client) = await _pump(tester);
    expect(find.text('New workspace in argus'), findsOneWidget);
    expect(find.text('setup: make deps'), findsOneWidget);
    FilledButton create() =>
        tester.widget(find.widgetWithText(FilledButton, 'Create'));
    expect(create().onPressed, isNull);
    await tester.enterText(find.byKey(const Key('create-branch')), '   ');
    await tester.pump();
    expect(create().onPressed, isNull);
    await tester.enterText(find.byKey(const Key('create-branch')), 'feat/new');
    await tester.pump();
    await tester.tap(find.text('Create'));
    await tester.pumpAndSettle();
    expect(_creates(client).single.$2, {
      'project_id': 'A:p1',
      'source': 'new',
      'branch': 'feat/new',
    });
    expect(find.byType(NewWorkspaceScreen), findsNothing);
    expect(find.text('created workspace new'), findsOneWidget);
    expect(c.read(pendingScopeProvider), 'A:w9');
  });

  testWidgets('a picked target is sent; PRs lock it to the PR base', (
    tester,
  ) async {
    final (_, client) = await _pump(tester);
    expect(find.text('Target: default branch'), findsOneWidget);
    await tester.tap(find.byKey(const Key('create-target')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('feat/a'));
    await tester.pumpAndSettle();
    expect(find.text('Target: feat/a'), findsOneWidget);

    await tester.tap(find.text('PRs'));
    await tester.pumpAndSettle();
    expect(find.text('Target: from PR base'), findsOneWidget);
    expect(
      tester.widget<ListTile>(find.byKey(const Key('create-target'))).enabled,
      isFalse,
    );
    expect(
      find.text('First 1 · the filter searches only these'),
      findsOneWidget,
    );
    await tester.tap(find.text('#7 Fix it'));
    await tester.pumpAndSettle();
    expect(_creates(client).single.$2, {
      'project_id': 'A:p1',
      'source': 'pr',
      'number': 7,
    });
  });

  testWidgets('a picked target goes with a branch create', (tester) async {
    final (_, client) = await _pump(tester);
    await tester.tap(find.byKey(const Key('create-target')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('main'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byKey(const Key('create-branch')), 'x');
    await tester.pump();
    await tester.tap(find.text('Create'));
    await tester.pumpAndSettle();
    expect(_creates(client).single.$2, {
      'project_id': 'A:p1',
      'source': 'new',
      'branch': 'x',
      'target_branch': 'main',
    });
  });

  testWidgets('Branches tab disables in-use branches', (tester) async {
    final (_, client) = await _pump(tester);
    await tester.tap(find.text('Branches'));
    await tester.pumpAndSettle();
    expect(find.text('(in use)'), findsOneWidget);
    await tester.tap(find.text('main'));
    await tester.pumpAndSettle();
    expect(_creates(client), isEmpty);
    await tester.tap(find.text('feat/a'));
    await tester.pumpAndSettle();
    expect(_creates(client).single.$2, {
      'project_id': 'A:p1',
      'source': 'branch',
      'branch': 'feat/a',
    });
  });

  testWidgets('a failed create keeps the page and shows the error', (
    tester,
  ) async {
    await _pump(
      tester,
      handler: (m, p) async => throw const RpcError(-32600, 'branch exists'),
    );
    await tester.enterText(find.byKey(const Key('create-branch')), 'dup');
    await tester.pump();
    await tester.tap(find.text('Create'));
    await tester.pumpAndSettle();
    expect(find.byType(NewWorkspaceScreen), findsOneWidget);
    expect(find.text('branch exists'), findsOneWidget);
    await tester.enterText(find.byKey(const Key('create-branch')), 'dup2');
    await tester.pump();
    expect(find.text('branch exists'), findsNothing);
  });

  testWidgets('controls are disabled while the create call runs', (
    tester,
  ) async {
    final gate = Completer<Object?>();
    final (_, client) = await _pump(tester, handler: (m, p) => gate.future);
    await tester.enterText(find.byKey(const Key('create-branch')), 'x');
    await tester.pump();
    await tester.tap(find.text('Create'));
    await tester.pump();
    expect(find.text('Creating…'), findsOneWidget);
    expect(find.text('Create'), findsNothing);
    expect(_creates(client), hasLength(1));
    gate.complete({'workspace_id': 'A:w9', 'dir': '/x'});
    await tester.pumpAndSettle();
  });

  testWidgets('an issue with a prompt offers a spawn with the prompt', (
    tester,
  ) async {
    await _pump(
      tester,
      handler: (m, p) async => {
        'workspace_id': 'A:w9',
        'dir': '/src/argus/.wt/issue-3',
        'prompt': 'Crash text',
      },
    );
    await tester.tap(find.text('Issues'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('#3 Crash'));
    await tester.pumpAndSettle();
    expect(find.text('Start an agent with this issue?'), findsOneWidget);
    await tester.tap(find.text('Start'));
    await tester.pumpAndSettle();
    final dialog = tester.widget<SpawnDialog>(find.byType(SpawnDialog));
    expect(dialog.target?.prompt, 'Crash text');
    expect(dialog.target?.cwd, '/src/argus/.wt/issue-3');
  });

  test('createdText adds setup and warning parts', () {
    expect(
      createdText(const CreateResult(workspaceId: 'x', dir: '/a/b')),
      'created workspace b',
    );
    expect(
      createdText(
        const CreateResult(
          workspaceId: 'x',
          dir: '/a/b',
          setup: 'make',
          warning: 'w',
        ),
      ),
      'created workspace b · setting up · w',
    );
  });

  testWidgets('Back while creating: success still reports and opens it', (
    tester,
  ) async {
    final gate = Completer<Object?>();
    final (c, _) = await _pump(tester, handler: (m, p) => gate.future);
    await tester.enterText(find.byKey(const Key('create-branch')), 'x');
    await tester.pump();
    await tester.tap(find.text('Create'));
    await tester.pump();
    tester.state<NavigatorState>(find.byType(Navigator)).pop();
    await tester.pump(const Duration(milliseconds: 50));
    gate.complete({'workspace_id': 'A:w9', 'dir': '/src/argus/.wt/x'});
    await tester.pumpAndSettle();
    expect(find.text('go'), findsOneWidget);
    expect(find.text('created workspace x'), findsOneWidget);
    expect(c.read(pendingScopeProvider), 'A:w9');
  });

  testWidgets('Back while creating: a failure shows a snackbar', (
    tester,
  ) async {
    final gate = Completer<Object?>();
    await _pump(tester, handler: (m, p) => gate.future);
    await tester.enterText(find.byKey(const Key('create-branch')), 'x');
    await tester.pump();
    await tester.tap(find.text('Create'));
    await tester.pump();
    tester.state<NavigatorState>(find.byType(Navigator)).pop();
    await tester.pump(const Duration(milliseconds: 50));
    gate.completeError(const RpcError(-32600, 'branch exists'));
    await tester.pumpAndSettle();
    expect(find.text('go'), findsOneWidget);
    expect(find.text('create workspace failed: branch exists'), findsOneWidget);
  });

  testWidgets('two taps in one frame send one create', (tester) async {
    final gate = Completer<Object?>();
    final (_, client) = await _pump(tester, handler: (m, p) => gate.future);
    await tester.enterText(find.byKey(const Key('create-branch')), 'x');
    await tester.pump();
    final create = tester.widget<FilledButton>(
      find.widgetWithText(FilledButton, 'Create'),
    );
    create.onPressed!();
    create.onPressed!();
    await tester.pump();
    expect(_creates(client), hasLength(1));
    gate.complete({'workspace_id': 'A:w9', 'dir': '/x'});
    await tester.pumpAndSettle();
  });

  testWidgets('a failing issue offer does not report a failed create', (
    tester,
  ) async {
    final (c, _) = await _pump(
      tester,
      handler: (m, p) async => {
        'workspace_id': 'A:w9',
        'dir': '/src/argus/.wt/issue-3',
        'prompt': 'Crash text',
      },
    );
    await tester.tap(find.text('Issues'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('#3 Crash'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Not now'));
    await tester.pumpAndSettle();
    expect(find.textContaining('failed'), findsNothing);
    expect(c.read(pendingScopeProvider), 'A:w9');
  });

  testWidgets('a tab change clears the filter; no matches and none open', (
    tester,
  ) async {
    await _pump(tester);
    await tester.tap(find.text('PRs'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byKey(const Key('create-filter')), 'zzz');
    await tester.pump();
    expect(find.text('No matches'), findsOneWidget);
    await tester.tap(find.text('Issues'));
    await tester.pumpAndSettle();
    expect(
      tester
          .widget<TextField>(find.byKey(const Key('create-filter')))
          .controller
          ?.text,
      '',
    );
    expect(find.text('#3 Crash'), findsOneWidget);
  });
}
