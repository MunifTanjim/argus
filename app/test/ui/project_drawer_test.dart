import 'package:argus/state/gateway.dart';
import 'package:argus/state/navigation.dart';
import 'package:argus/state/projects.dart';
import 'package:argus/state/projects_api.dart';
import 'package:argus/state/project_tree.dart';
import 'package:argus/ui/project_drawer.dart';
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

Future<ProviderContainer> _pump(
  WidgetTester tester,
  Map<String, dynamic> tree, {
  double width = 400,
}) async {
  tester.view.physicalSize = Size(width, 800);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  final c = ProviderContainer(
    overrides: [gatewayProvider.overrideWithValue(null)],
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
  testWidgets('shows Home, projects, and two-line workspace rows', (
    tester,
  ) async {
    await _pump(tester, _tree());
    expect(find.text('Home'), findsOneWidget);
    expect(find.text('argus'), findsWidgets);
    expect(find.text('registry'), findsOneWidget);
    expect(find.text('feat/registry'), findsOneWidget);
    expect(find.byIcon(Icons.folder_outlined), findsOneWidget); // main worktree
    expect(find.byIcon(Icons.call_split), findsNWidgets(2));
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
    expect(find.text('Remove'), findsOneWidget);
    expect(find.text('Force remove'), findsOneWidget);
    await tester.tapAt(const Offset(10, 10));
    await tester.pumpAndSettle();
    expect(find.text('Force remove'), findsNothing);

    await tester.longPress(find.text('dotfiles'));
    await tester.pumpAndSettle();
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
}
