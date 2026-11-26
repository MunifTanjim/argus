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
import 'package:argus/ui/details_header.dart';
import 'package:argus/ui/nerd_icon.dart';
import 'package:argus/ui/project_drawer.dart';
import 'package:argus/transport/connection.dart';
import 'package:argus/ui/session_sections_list.dart';
import 'package:argus/ui/settings_screen.dart';
import 'package:argus/ui/spawn_dialog.dart';
import 'package:argus/ui/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../support/fake_gateway_client.dart';

Map<String, dynamic> _tree({
  String branch = 'feat/registry',
  String name = 'argus',
  String dir = '/src/argus/.worktrees/registry',
  Map<String, dynamic>? setup,
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
          'dir': dir,
          'branch': branch,
          'head': '3f9c2a1e',
          'target_branch': 'main',
          'setup': ?setup,
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

Session _sess(
  String id,
  String status, {
  String? ws,
  String agent = 'claude',
  String? lastActivity,
}) => Session.fromJson(
  jsonDecode(
        jsonEncode({
          'id': id,
          'agent': agent,
          'name': 'name-$id',
          'status': status,
          'source': 'hooked',
          'workspace_id': ?ws,
          if (lastActivity != null) 'summary': {'last_activity': lastActivity},
          'tmux': {
            'server': 'argus',
            'pane_id': '%1',
            'session_name': 's',
            'window_index': 0,
            'current_path': '/p',
          },
        }),
      )
      as Map<String, dynamic>,
);

Future<ProviderContainer> _pump(
  WidgetTester tester,
  Map<String, dynamic> tree, {
  double width = 400,
  double height = 800,
  _MemKv? kv,
}) async {
  tester.view.physicalSize = Size(width, height);
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
  testWidgets('the Sessions tab lists live sessions and is remembered', (
    tester,
  ) async {
    final kv = _MemKv();
    final c = await _pump(tester, _tree(), kv: kv);
    c.read(sessionsProvider.notifier).replaceAll([
      _sess('A:s1', 'awaiting_input', ws: 'A:w1'),
      _sess('A:s2', 'idle', ws: 'A:w2'),
      _sess('A:s3', 'dead'),
    ]);
    await tester.tap(find.text('Sessions'));
    await tester.pumpAndSettle();
    expect(find.text('NEEDS YOU'), findsOneWidget);
    expect(find.text('name-A:s1'), findsOneWidget);
    expect(find.text('name-A:s2'), findsOneWidget);
    expect(find.text('name-A:s3'), findsNothing);
    expect(find.text('Home'), findsNothing);
    expect(kv.m['leftSidebar.tab'], 'sessions');
  });

  testWidgets('a Sessions tab group folds like a tree node', (tester) async {
    final c = await _pump(
      tester,
      _tree(),
      kv: _MemKv({'leftSidebar.tab': 'sessions'}),
    );
    c.read(sessionsProvider.notifier).replaceAll([
      _sess('A:s1', 'working', ws: 'A:w1'),
      _sess('A:s2', 'idle', ws: 'A:w2'),
    ]);
    await tester.pumpAndSettle();
    expect(find.byIcon(Icons.expand_more), findsNWidgets(2));

    await tester.tap(find.text('WORKING'));
    await tester.pump();
    expect(find.text('name-A:s1'), findsNothing);
    expect(find.text('name-A:s2'), findsOneWidget);
    expect(find.byIcon(Icons.chevron_right), findsOneWidget);
    // The folded group shows its activity, as a folded tree node does.
    expect(
      find.descendant(
        of: find.ancestor(
          of: find.text('WORKING'),
          matching: find.byType(InkWell),
        ),
        matching: find.text('1'),
      ),
      findsOneWidget,
    );

    // A filter shows every match, folded or not.
    c.read(sidebarSessionsFilterProvider.notifier).state = 'name-A';
    await tester.pump();
    expect(find.text('name-A:s1'), findsOneWidget);
    c.read(sidebarSessionsFilterProvider.notifier).state = '';
    await tester.pump();

    await tester.tap(find.text('WORKING'));
    await tester.pump();
    expect(find.text('name-A:s1'), findsOneWidget);
  });


  testWidgets('the agent shows only when listed sessions mix agents', (
    tester,
  ) async {
    final c = await _pump(
      tester,
      _tree(),
      kv: _MemKv({'leftSidebar.tab': 'sessions'}),
    );
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
    final c = ProviderContainer(
      overrides: [
        gatewayProvider.overrideWithValue(null),
        leftSidebarTabStoreProvider.overrideWithValue(
          LeftSidebarTabStore(_MemKv()),
        ),
      ],
    );
    addTearDown(c.dispose);
    // Chosen before the drawer mounts, so the Projects filter is never built.
    await c.read(leftSidebarTabProvider.notifier).set(LeftSidebarTab.sessions);
    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: c,
        child: const MaterialApp(home: Scaffold(body: ProjectDrawer())),
      ),
    );
    await tester.pump();
    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: c,
        child: const MaterialApp(home: SizedBox()),
      ),
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets('tapping a session asks the shell to open it', (tester) async {
    final c = await _pump(tester, _tree());
    c.read(sessionsProvider.notifier).replaceAll([
      _sess('A:s1', 'idle', ws: 'A:w1'),
    ]);
    await tester.tap(find.text('Sessions'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('name-A:s1'));
    await tester.pump();
    expect(c.read(pendingOpenSessionProvider)?.sessionId, 'A:s1');
    expect(c.read(pendingOpenSessionProvider)?.inHome, isFalse);
  });

  testWidgets('node headers have no info button', (tester) async {
    await _pump(tester, _tree());
    expect(find.bySemanticsLabel('Node info'), findsNothing);
    expect(find.byIcon(Icons.info_outline), findsNothing);
  });

  // The test font draws every glyph as wide as its font size.

  testWidgets('a long workspace name keeps 60% of the line, its branch the '
      'rest', (tester) async {
    await _pump(
      tester,
      _tree(
        dir: '/src/argus/.worktrees/notifications-overhaul',
        branch: 'feat/notifications-overhaul-v2',
      ),
    );
    final name = tester.renderObject<RenderParagraph>(
      find.text('notifications-overhaul'),
    );
    final branch = tester.renderObject<RenderParagraph>(
      find.text('feat/notifications-overhaul-v2'),
    );
    expect(name.didExceedMaxLines, isTrue);
    expect(branch.didExceedMaxLines, isTrue);
    final line = name.size.width + 8 + branch.size.width;
    expect(name.size.width, moreOrLessEquals(line * 0.6, epsilon: 1));
  });


  testWidgets('shows Home, projects, and one-line workspace rows', (
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
    expect(find.byTooltip('Settings'), findsOneWidget);
    final row = find
        .ancestor(of: find.text('registry'), matching: find.byType(InkWell))
        .first;
    expect(tester.getSize(row).height, 40);
    expect(
      tester.getCenter(find.text('feat/registry')).dy,
      tester.getCenter(find.text('registry')).dy,
    );
    // The branch sits at the row's right edge (inside its 8 px padding).
    expect(
      tester.getTopRight(find.text('feat/registry')).dx,
      closeTo(tester.getTopRight(row).dx - 8, 0.5),
    );
  });

  testWidgets('a long branch with pills keeps the workspace name visible', (
    tester,
  ) async {
    final c = await _pump(
      tester,
      _tree(branch: 'feature/an-extremely-long-branch-name'),
      width: 264,
    );
    c.read(sessionsProvider.notifier).replaceAll([
      _sess('A:s1', 'working', ws: 'A:w2'),
      _sess('A:s2', 'awaiting_input', ws: 'A:w2'),
      _sess('A:s3', 'idle', ws: 'A:w2'),
    ]);
    await tester.pump();
    expect(tester.takeException(), isNull);
    expect(tester.getSize(find.text('registry')).width, greaterThan(40));
  });

  testWidgets('a short branch sits at the right edge of its row', (
    tester,
  ) async {
    await _pump(tester, _tree(branch: 'dev'));
    final row = find
        .ancestor(of: find.text('registry'), matching: find.byType(InkWell))
        .first;
    expect(
      tester.getTopRight(find.text('dev')).dx,
      closeTo(tester.getTopRight(row).dx - 8, 0.5),
    );
  });

  testWidgets('a workspace named as its branch shows the name once', (
    tester,
  ) async {
    await _pump(tester, _tree(branch: 'registry'));
    expect(find.text('registry'), findsOneWidget);
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
    await tester.tap(find.byTooltip('Filter options'));
    await tester.pumpAndSettle();
    expect(find.byType(Checkbox), findsNWidgets(2));
    expect(find.text('Refresh'), findsOneWidget);
    await tester.tap(find.text('Show hidden'));
    await tester.pumpAndSettle();
    expect(c.read(treeViewProvider).showHidden, isTrue);
    await tester.tap(find.byTooltip('Filter options'));
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
    expect(name.style?.color, ArgusTokens.fallback.textDim);
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

  testWidgets('the workspace sheet pins Remove and New session', (
    tester,
  ) async {
    final c = await _pump(tester, _tree(), height: 520);
    c.read(sessionsProvider.notifier).replaceAll([
      _sess('A:s1', 'working', ws: 'A:w2'),
    ]);
    await tester.pump();
    await tester.longPress(find.text('registry'));
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    final header = tester.widget<DetailsHeader>(find.byType(DetailsHeader));
    expect(header.label, 'argus');
    expect(header.title, 'registry');
    expect(header.meta.map((m) => m.$1), ['feat/registry → main']);
    expect(find.text('1 working'), findsOneWidget);
    expect(find.text('3f9c2a1'), findsOneWidget);
    final bar = find.byType(DetailsActionBar);
    for (final label in ['Remove', 'New session']) {
      expect(
        find.descendant(of: bar, matching: find.text(label)).hitTestable(),
        findsOneWidget,
        reason: label,
      );
    }
    expect(
      tester.getCenter(find.text('Remove')).dx,
      lessThan(tester.getCenter(find.text('New session')).dx),
    );
    expect(find.text('Change target'), findsOneWidget);
    expect(find.text('Force remove'), findsOneWidget);
  });

  testWidgets('the workspace sheet shows a failed setup', (tester) async {
    await _pump(
      tester,
      _tree(
        setup: {'state': 'failed', 'exit_code': 2, 'command': 'make setup'},
      ),
    );
    await tester.longPress(find.text('registry'));
    await tester.pumpAndSettle();
    expect(find.text('failed (exit 2) · make setup'), findsOneWidget);
  });

  testWidgets('the project sheet pins Forget and shows a git error', (
    tester,
  ) async {
    await _pump(tester, _tree(), height: 520);
    await tester.longPress(find.text('dotfiles'));
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    final header = tester.widget<DetailsHeader>(find.byType(DetailsHeader));
    expect(header.title, 'dotfiles');
    expect(header.label, 'git');
    expect(find.text('git exploded'), findsOneWidget);
    expect(find.text('1'), findsOneWidget);
    expect(
      find
          .descendant(
            of: find.byType(DetailsActionBar),
            matching: find.text('Forget'),
          )
          .hitTestable(),
      findsOneWidget,
    );
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

  testWidgets('the Sessions tab narrows to the shown session\'s workspace', (
    tester,
  ) async {
    final c = await _pump(tester, _tree());
    c.read(sessionsProvider.notifier).replaceAll([
      _sess('A:s1', 'awaiting_input', ws: 'A:w1'),
      _sess('A:s2', 'idle', ws: 'A:w2'),
      _sess('A:s3', 'idle', ws: 'A:w1'),
      _sess('A:s4', 'idle'),
    ]);
    await tester.tap(find.text('Sessions'));
    await tester.pumpAndSettle();
    Future<bool> enabled() async {
      await tester.tap(find.byTooltip('Filter options'));
      await tester.pumpAndSettle();
      final on =
          tester
              .widget<CheckboxMenuButton>(
                find.widgetWithText(CheckboxMenuButton, 'This workspace only'),
              )
              .onChanged !=
          null;
      await tester.tapAt(Offset.zero);
      await tester.pumpAndSettle();
      return on;
    }

    Future<void> toggle() async {
      await tester.tap(find.byTooltip('Filter options'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('This workspace only'));
      await tester.pumpAndSettle();
    }

    final dot = find.byKey(const Key('filter-options-active'));
    // No session shown, or one without a workspace: nothing to narrow to.
    expect(await enabled(), isFalse);
    c.read(shownSessionProvider.notifier).state = 'A:s4';
    await tester.pumpAndSettle();
    expect(await enabled(), isFalse);

    c.read(shownSessionProvider.notifier).state = 'A:s1';
    await tester.pumpAndSettle();
    await toggle();
    expect(dot, findsOneWidget);
    expect(find.text('name-A:s1'), findsOneWidget);
    expect(find.text('name-A:s3'), findsOneWidget);
    expect(find.text('name-A:s2'), findsNothing);
    expect(find.text('name-A:s4'), findsNothing);

    // The text filter looks only inside the workspace.
    await tester.enterText(find.byType(TextField), 's2');
    await tester.pumpAndSettle();
    expect(find.text('no sessions match "s2"'), findsOneWidget);
    await tester.enterText(find.byType(TextField), '');
    await tester.pumpAndSettle();

    // With no session shown, every session is listed again.
    c.read(shownSessionProvider.notifier).state = null;
    await tester.pumpAndSettle();
    expect(dot, findsNothing);
    expect(find.text('name-A:s2'), findsOneWidget);

    c.read(shownSessionProvider.notifier).state = 'A:s2';
    await tester.pumpAndSettle();
    expect(find.text('name-A:s1'), findsNothing);
    expect(find.text('name-A:s2'), findsOneWidget);
    await toggle();
    expect(find.text('name-A:s1'), findsOneWidget);
    expect(find.text('name-A:s4'), findsOneWidget);
  });

  testWidgets('a session row shows its last activity time', (tester) async {
    final c = await _pump(tester, _tree());
    c.read(sessionsProvider.notifier).replaceAll([
      _sess(
        'A:s1',
        'idle',
        ws: 'A:w1',
        lastActivity: DateTime.now()
            .subtract(const Duration(hours: 3))
            .toUtc()
            .toIso8601String(),
      ),
    ]);
    await tester.tap(find.text('Sessions'));
    await tester.pumpAndSettle();
    expect(find.text('3h ago'), findsOneWidget);
  });

  testWidgets('a long-press on a session shows its details', (tester) async {
    final c = await _pump(tester, _tree());
    c.read(sessionsProvider.notifier).replaceAll([
      Session.fromJson(
        jsonDecode(
              jsonEncode({
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
              }),
            )
            as Map<String, dynamic>,
      ),
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
      'work · %3 · default',
      'A:s1',
    ]) {
      expect(find.text(v), findsOneWidget, reason: v);
    }
    // The header shows the branch in its meta line, and the node.
    expect(find.textContaining('⎇ main', findRichText: true), findsOneWidget);
    expect(find.textContaining('mbp', findRichText: true), findsWidgets);
    // No last activity, so the status row has no time.
    expect(find.textContaining('offline ·'), findsNothing);
  });

  testWidgets('switching tabs keeps the header height', (tester) async {
    await _pump(tester, _tree());
    final before = tester.getTopLeft(find.text('Projects'));
    await tester.tap(find.text('Sessions'));
    await tester.pumpAndSettle();
    expect(tester.getTopLeft(find.text('Projects')), before);
    expect(find.byType(SegmentedButton<LeftSidebarTab>), findsNothing);
  });

  testWidgets('the header opens the spawn dialog on both tabs', (tester) async {
    await _pump(tester, _tree());
    expect(find.byTooltip('New session'), findsOneWidget);
    await tester.tap(find.byTooltip('New session'));
    await tester.pumpAndSettle();
    expect(find.byType(SpawnDialog), findsOneWidget);
  });

  testWidgets('the header has text tabs, New session, and no menu', (
    tester,
  ) async {
    await _pump(tester, _tree());
    await tester.tap(find.text('Sessions'));
    await tester.pumpAndSettle();
    expect(find.byIcon(Icons.more_vert), findsNothing);
    expect(find.byTooltip('New session'), findsOneWidget);
    expect(find.text('Argus'), findsNothing);
  });

  testWidgets('the filter bar sits below the list and Settings is a button', (
    tester,
  ) async {
    await _pump(tester, _tree());
    expect(
      tester.getTopLeft(find.byType(TextField)).dy,
      greaterThan(tester.getTopLeft(find.text('Home')).dy),
    );
    expect(find.text('Settings'), findsNothing);
    await tester.tap(find.byTooltip('Settings'));
    await tester.pumpAndSettle();
    expect(find.byType(SettingsScreen), findsOneWidget);
  });

  testWidgets('the clear button shows with text and clears the current tab', (
    tester,
  ) async {
    final c = await _pump(tester, _tree());
    expect(find.byTooltip('Clear filter'), findsNothing);
    await tester.enterText(find.byType(TextField), 'dot');
    await tester.pump();
    await tester.tap(find.byTooltip('Clear filter'));
    await tester.pump();
    expect(c.read(treeViewProvider).filter, '');
    expect(find.text('registry'), findsOneWidget);
    expect(tester.testTextInput.hasAnyClients, isTrue);

    await tester.enterText(find.byType(TextField), 'zzz');
    await tester.tap(find.text('Sessions'));
    await tester.pumpAndSettle();
    expect(find.byTooltip('Clear filter'), findsNothing);
    await tester.enterText(find.byType(TextField), 's2');
    await tester.pump();
    await tester.tap(find.byTooltip('Clear filter'));
    await tester.pump();
    expect(c.read(sidebarSessionsFilterProvider), '');
  });

  testWidgets('the options dot follows the effective filter', (tester) async {
    final c = await _pump(tester, _tree());
    final dot = find.byKey(const Key('filter-options-active'));
    c.read(sessionsProvider.notifier).replaceAll([
      _sess('A:s1', 'idle', ws: 'A:w1'),
    ]);
    await tester.tap(find.text('Sessions'));
    await tester.pumpAndSettle();
    c.read(sidebarSessionsWorkspaceOnlyProvider.notifier).state = true;
    await tester.pump();
    expect(dot, findsNothing);
    c.read(shownSessionProvider.notifier).state = 'A:s1';
    await tester.pump();
    expect(dot, findsOneWidget);

    await tester.tap(find.text('Projects'));
    await tester.pumpAndSettle();
    expect(dot, findsNothing);
    c.read(treeViewProvider.notifier).toggleHidden();
    await tester.pump();
    expect(dot, findsOneWidget);
  });

  testWidgets('the filter bar stays above the keyboard', (tester) async {
    await _pump(tester, _tree());
    tester.view.viewInsets = const FakeViewPadding(bottom: 300);
    await tester.pump();
    expect(
      tester.getBottomLeft(find.byType(TextField)).dy,
      lessThanOrEqualTo(800 - 300),
    );
  });

  testWidgets(
    'the reconnect banner shows above the filter bar only when not connected',
    (tester) async {
      final c = await _pump(tester, _tree());
      c.read(connStateProvider.notifier).state = ConnState.reconnecting;
      await tester.pump();
      expect(find.text('Reconnecting…'), findsOneWidget);
      expect(
        tester.getBottomLeft(find.byType(ReconnectBanner)).dy,
        lessThanOrEqualTo(tester.getTopLeft(find.byType(TextField)).dy),
      );
      c.read(connStateProvider.notifier).state = ConnState.connected;
      await tester.pump();
      expect(find.byType(ReconnectBanner), findsNothing);
    },
  );

  testWidgets('the options menu opens above its button, not over it', (
    tester,
  ) async {
    await _pump(tester, _tree());
    final button = find.byTooltip('Filter options');
    await tester.tap(button);
    await tester.pumpAndSettle();
    expect(
      tester.getBottomLeft(find.text('Refresh')).dy,
      lessThanOrEqualTo(tester.getTopLeft(button).dy),
    );
    expect(button.hitTestable(), findsOneWidget);
  });
}
