import 'dart:convert';

import 'package:argus/models/session.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/left_sidebar.dart';
import 'package:argus/state/push.dart';
import 'package:argus/state/sessions.dart';
import 'package:argus/state/navigation.dart';
import 'package:argus/state/projects.dart';
import 'package:argus/state/projects_api.dart';
import 'package:argus/state/project_tree.dart';
import 'package:argus/ui/nerd_icon.dart';
import 'package:argus/ui/node_screen.dart';
import 'package:argus/ui/project_drawer.dart';
import 'package:argus/ui/spawn_dialog.dart';
import 'package:argus/ui/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../support/fake_gateway_client.dart';

Map<String, dynamic> _tree({
  String branch = 'feat/registry',
  String name = 'argus',
}) => {
  'projects': [
    {
      'id': 'A:p1',
      'name': name,
      'pinned': true,
      'node_id': 'A',
      'node_label': 'mbp',
      'workspaces': [
        {'id': 'A:w1', 'dir': '/src/argus', 'branch': 'main', 'is_main': true},
        {
          'id': 'A:w2',
          'dir': '/src/argus/.worktrees/registry',
          'branch': branch,
        },
      ],
    },
    {
      'id': 'A:p2',
      'name': 'dotfiles',
      'error': 'git exploded',
      'node_id': 'A',
      'workspaces': [
        {'id': 'A:w3', 'dir': '/dot', 'branch': 'main'},
      ],
    },
  ],
};

class _MemKv implements SecureKv {
  _MemKv([Map<String, String>? m]) : m = m ?? {};
  final Map<String, String> m;
  @override
  Future<String?> read(String key) async => m[key];
  @override
  Future<void> write(String key, String value) async => m[key] = value;
  @override
  Future<void> delete(String key) async => m.remove(key);
}

Session _sess(String id, String status, {String? ws, String agent = 'claude'}) =>
    Session.fromJson(
  jsonDecode(jsonEncode({
    'id': id,
    'agent': agent,
    'name': 'name-$id',
    'status': status,
    'source': 'hooked',
    'workspace_id': ?ws,
    'tmux': {
      'server': 'argus',
      'pane_id': '%1',
      'session_name': 's',
      'window_index': 0,
      'current_path': '/p',
    },
  })) as Map<String, dynamic>,
);

Future<ProviderContainer> _pump(
  WidgetTester tester,
  Map<String, dynamic> tree, {
  double width = 400,
  _MemKv? kv,
}) async {
  tester.view.physicalSize = Size(width, 800);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  final c = ProviderContainer(
    overrides: [
      gatewayProvider.overrideWithValue(null),
      leftSidebarTabStoreProvider.overrideWithValue(
        LeftSidebarTabStore(kv ?? _MemKv()),
      ),
    ],
  );
  addTearDown(c.dispose);
  await c
      .read(projectsProvider.notifier)
      .load(FakeGatewayClient((m, p) async => tree));
  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: c,
      child: const MaterialApp(home: Scaffold(body: ProjectDrawer())),
    ),
  );
  await tester.pump();
  return c;
}

void main() {
  testWidgets('the Sessions tab lists live sessions and is remembered', (tester) async {
    final kv = _MemKv();
    final c = await _pump(tester, _tree(), kv: kv);
    c.read(sessionsProvider.notifier).replaceAll([
      _sess('A:s1', 'awaiting_input', ws: 'A:w1'),
      _sess('A:s2', 'idle', ws: 'A:w2'),
      _sess('A:s3', 'dead'),
    ]);
    await tester.tap(find.text('Sessions'));
    await tester.pumpAndSettle();
    expect(find.text('Needs you'), findsOneWidget);
    expect(find.text('name-A:s1'), findsOneWidget);
    expect(find.text('name-A:s2'), findsOneWidget);
    expect(find.text('name-A:s3'), findsNothing);
    expect(find.text('Home'), findsNothing);
    expect(kv.m['leftSidebar.tab'], 'sessions');
  });

  testWidgets('the agent shows only when listed sessions mix agents', (tester) async {
    final c = await _pump(tester, _tree(), kv: _MemKv({'leftSidebar.tab': 'sessions'}));
    c.read(sessionsProvider.notifier).replaceAll([
      _sess('A:s1', 'idle', ws: 'A:w1'),
      _sess('A:s2', 'dead', agent: 'codex'),
    ]);
    await tester.pumpAndSettle();
    expect(find.textContaining('Claude'), findsNothing);
    c.read(sessionsProvider.notifier).replaceAll([
      _sess('A:s1', 'idle', ws: 'A:w1'),
      _sess('A:s2', 'idle', ws: 'A:w2', agent: 'codex'),
    ]);
    await tester.pumpAndSettle();
    expect(find.textContaining('Claude'), findsOneWidget);
    expect(find.textContaining('Codex'), findsOneWidget);
  });

  testWidgets('a saved Sessions tab opens on Sessions', (tester) async {
    await _pump(tester, _tree(), kv: _MemKv({'leftSidebar.tab': 'sessions'}));
    await tester.pumpAndSettle();
    expect(find.text('no live sessions'), findsOneWidget);
  });

  testWidgets('unmounting on the Sessions tab throws nothing', (tester) async {
    final c = ProviderContainer(overrides: [
      gatewayProvider.overrideWithValue(null),
      leftSidebarTabStoreProvider.overrideWithValue(LeftSidebarTabStore(_MemKv())),
    ]);
    addTearDown(c.dispose);
    // Chosen before the drawer mounts, so the Projects filter is never built.
    await c.read(leftSidebarTabProvider.notifier).set(LeftSidebarTab.sessions);
    await tester.pumpWidget(UncontrolledProviderScope(
      container: c,
      child: const MaterialApp(home: Scaffold(body: ProjectDrawer())),
    ));
    await tester.pump();
    await tester.pumpWidget(UncontrolledProviderScope(
      container: c,
      child: const MaterialApp(home: SizedBox()),
    ));
    expect(tester.takeException(), isNull);
  });

  testWidgets('tapping a session asks the shell to open it', (tester) async {
    final c = await _pump(tester, _tree());
    c.read(sessionsProvider.notifier).replaceAll([_sess('A:s1', 'idle', ws: 'A:w1')]);
    await tester.tap(find.text('Sessions'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('name-A:s1'));
    await tester.pump();
    expect(c.read(pendingOpenSessionProvider)?.sessionId, 'A:s1');
    expect(c.read(pendingOpenSessionProvider)?.inHome, isFalse);
  });

  testWidgets('the node row info button opens the node screen', (tester) async {
    await _pump(tester, _tree());
    await tester.tap(find.bySemanticsLabel('Node info'));
    await tester.pumpAndSettle();
    expect(find.byType(NodeScreen), findsOneWidget);
  });

  testWidgets('the info button has a 40 px hit area and does not fold the node', (
    tester,
  ) async {
    final c = await _pump(tester, _tree());
    final button = find.ancestor(
      of: find.byIcon(Icons.info_outline),
      matching: find.byType(InkWell),
    ).first;
    final size = tester.getSize(button);
    expect(size.width, greaterThanOrEqualTo(40));
    expect(size.height, greaterThanOrEqualTo(40));
    await tester.tap(find.bySemanticsLabel('Node info'));
    await tester.pumpAndSettle();
    expect(c.read(treeViewProvider).folded, isNot(contains('A')));
  });

  testWidgets('shows Home, projects, and two-line workspace rows', (
    tester,
  ) async {
    await _pump(tester, _tree());
    expect(find.text('Home'), findsOneWidget);
    expect(find.text('argus'), findsWidgets);
    expect(find.text('registry'), findsOneWidget);
    expect(find.text('feat/registry'), findsOneWidget);
    expect(find.byIcon(Icons.folder_outlined), findsOneWidget); // main worktree
    expect(find.byType(GitBranchIcon), findsNWidgets(2));
    expect(find.byIcon(Icons.push_pin), findsOneWidget);
    expect(find.text('Settings'), findsOneWidget);
  });

  testWidgets('a workspace tap sets the scope; Home clears it', (tester) async {
    final c = await _pump(tester, _tree());
    await tester.tap(find.text('registry'));
    await tester.pump();
    expect(c.read(scopeProvider), 'A:w2');
    await tester.tap(find.text('Home'));
    await tester.pump();
    expect(c.read(scopeProvider), isNull);
  });

  testWidgets('a project tap folds it without a scope change', (tester) async {
    final c = await _pump(tester, _tree());
    await tester.tap(find.text('dotfiles'));
    await tester.pump();
    expect(find.text('dot'), findsNothing);
    expect(c.read(scopeProvider), isNull);
  });

  testWidgets('the git error opens from the warning icon', (tester) async {
    await _pump(tester, _tree());
    await tester.tap(find.byIcon(Icons.warning_amber));
    await tester.pumpAndSettle();
    expect(find.text('git exploded'), findsOneWidget);
  });

  testWidgets('the filter narrows the tree', (tester) async {
    await _pump(tester, _tree());
    await tester.enterText(find.byType(TextField), 'dot');
    await tester.pump();
    expect(find.text('registry'), findsNothing);
    expect(find.text('dotfiles'), findsOneWidget);
    expect(find.text('Home'), findsOneWidget);
  });

  testWidgets('a progress row shows until the first load', (tester) async {
    final c = ProviderContainer(
      overrides: [gatewayProvider.overrideWithValue(null)],
    );
    addTearDown(c.dispose);
    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: c,
        child: const MaterialApp(home: Scaffold(body: ProjectDrawer())),
      ),
    );
    expect(find.byKey(const Key('projects-loading')), findsOneWidget);
    await c
        .read(projectsProvider.notifier)
        .load(FakeGatewayClient((m, p) async => _tree()));
    await tester.pump();
    expect(find.byKey(const Key('projects-loading')), findsNothing);
    expect(find.text('registry'), findsOneWidget);
  });

  testWidgets('long names do not overflow', (tester) async {
    await _pump(
      tester,
      _tree(
        name: 'a-very-long-project-name-that-goes-on-and-on-and-on',
        branch:
            'feature/an-extremely-long-branch-name-for-testing-overflow-in-rows',
      ),
      width: 320,
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets('the menu shows each toggle state as a trailing checkbox', (
    tester,
  ) async {
    final c = await _pump(tester, _tree());
    await tester.tap(find.byIcon(Icons.more_vert));
    await tester.pumpAndSettle();
    expect(find.byType(Checkbox), findsNWidgets(2));
    await tester.tap(find.text('Show hidden'));
    await tester.pumpAndSettle();
    expect(c.read(treeViewProvider).showHidden, isTrue);
    await tester.tap(find.byIcon(Icons.more_vert));
    await tester.pumpAndSettle();
    final hidden = tester.widget<Checkbox>(find.byType(Checkbox).first);
    expect(hidden.value, isTrue);
  });

  testWidgets('a node header folds its projects', (tester) async {
    final tree = _tree();
    (tree['projects'] as List).add({
      'id': 'B:p1',
      'name': 'infra',
      'node_id': 'B',
      'node_label': 'devbox',
      'workspaces': [
        {'id': 'B:w1', 'dir': '/infra', 'branch': 'main'},
      ],
    });
    final c = await _pump(tester, tree);
    expect(find.text('infra'), findsNWidgets(2)); // project and workspace
    await tester.tap(find.text('DEVBOX'));
    await tester.pump();
    expect(find.text('infra'), findsNothing);
    expect(c.read(treeViewProvider).folded, contains('B'));
    expect(c.read(scopeProvider), isNull);
  });

  testWidgets('hidden projects are muted with an icon; gone rows say so', (
    tester,
  ) async {
    final c = await _pump(tester, {
      'projects': [
        {
          'id': 'A:p1',
          'name': 'secret',
          'hidden': true,
          'node_id': 'A',
          'workspaces': [
            {'id': 'A:w1', 'dir': '/s', 'branch': 'main', 'is_main': true},
            {
              'id': 'A:w2',
              'dir': '/s/.wt/old',
              'branch': 'old',
              'is_gone': true,
            },
          ],
        },
      ],
    });
    c.read(treeViewProvider.notifier).toggleHidden();
    c.read(treeViewProvider.notifier).toggleGone();
    await tester.pump();
    expect(find.byIcon(Icons.visibility_off_outlined), findsOneWidget);
    final name = tester.widget<Text>(find.text('secret'));
    expect(name.style?.color, AppColors.dim);
    expect(find.text('old (gone)'), findsOneWidget);
  });

  testWidgets('long-press opens the row action sheet', (tester) async {
    await _pump(tester, _tree());
    await tester.longPress(find.text('registry'));
    await tester.pumpAndSettle();
    expect(find.text('Change target'), findsOneWidget);
    expect(find.text('Force remove'), findsOneWidget);
    await tester.tapAt(const Offset(10, 10));
    await tester.pumpAndSettle();
    expect(find.text('Change target'), findsNothing);

    await tester.longPress(find.text('dotfiles'));
    await tester.pumpAndSettle();
    expect(find.text('New workspace'), findsOneWidget);
    expect(find.text('Rename'), findsOneWidget);
    expect(find.text('Forget'), findsOneWidget);
    expect(find.text('New session'), findsNothing);
  });

  testWidgets('a workspace being removed says so', (tester) async {
    final c = await _pump(tester, _tree());
    c.read(removingProvider.notifier).state = {'A:w2'};
    await tester.pump();
    expect(find.text('removing…'), findsOneWidget);
  });

  testWidgets('the Sessions tab filters on its own text', (tester) async {
    final c = await _pump(tester, _tree());
    c.read(sessionsProvider.notifier).replaceAll([
      _sess('A:s1', 'awaiting_input', ws: 'A:w1'),
      _sess('A:s2', 'idle', ws: 'A:w2'),
    ]);
    await tester.enterText(find.byType(TextField), 'argus');
    await tester.tap(find.text('Sessions'));
    await tester.pumpAndSettle();
    // The Projects filter text does not carry over.
    expect(find.text('name-A:s1'), findsOneWidget);
    expect(find.text('name-A:s2'), findsOneWidget);

    await tester.enterText(find.byType(TextField), 's2');
    await tester.pumpAndSettle();
    expect(find.text('name-A:s1'), findsNothing);
    expect(find.text('name-A:s2'), findsOneWidget);

    await tester.enterText(find.byType(TextField), 'zzz');
    await tester.pumpAndSettle();
    expect(find.text('no sessions match "zzz"'), findsOneWidget);

    await tester.tap(find.text('Projects'));
    await tester.pumpAndSettle();
    expect(find.widgetWithText(TextField, 'argus'), findsOneWidget);
  });

  testWidgets('a long-press on a session shows its details', (tester) async {
    final c = await _pump(tester, _tree());
    c.read(sessionsProvider.notifier).replaceAll([
      Session.fromJson(jsonDecode(jsonEncode({
        'id': 'A:s1',
        'agent': 'claude',
        'name': 'fix-login',
        'status': 'awaiting_input',
        'status_label': 'needs input',
        'source': 'hooked',
        'offline': true,
        'cwd': '/src/argus',
        'branch': 'main',
        'node_label': 'mbp',
        'summary': {'task': 'fix the login bug'},
        'tmux': {
          'server': 'default',
          'pane_id': '%3',
          'session_name': 'work',
          'window_index': 0,
          'current_path': '/src/argus',
        },
      })) as Map<String, dynamic>),
    ]);
    await tester.tap(find.text('Sessions'));
    await tester.pumpAndSettle();
    await tester.longPress(find.text('fix-login'));
    await tester.pumpAndSettle();
    for (final v in [
      'needs input, offline',
      'Claude',
      'fix the login bug',
      '/src/argus',
      'main',
      'mbp',
      'work · %3 · default',
      'A:s1',
    ]) {
      expect(find.text(v), findsOneWidget, reason: v);
    }
    expect(find.text('Last activity'), findsNothing);
  });

  testWidgets('switching tabs keeps the header height', (tester) async {
    await _pump(tester, _tree());
    final before = tester.getTopLeft(find.byType(SegmentedButton<LeftSidebarTab>));
    await tester.tap(find.text('Sessions'));
    await tester.pumpAndSettle();
    expect(tester.getTopLeft(find.byType(SegmentedButton<LeftSidebarTab>)), before);
    expect(find.byTooltip('Tree options').hitTestable(), findsNothing);
  });

  testWidgets('the Sessions tab header opens the spawn dialog', (tester) async {
    await _pump(tester, _tree());
    expect(find.byTooltip('New session'), findsNothing);
    await tester.tap(find.text('Sessions'));
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('New session'));
    await tester.pumpAndSettle();
    expect(find.byType(SpawnDialog), findsOneWidget);
  });
}
