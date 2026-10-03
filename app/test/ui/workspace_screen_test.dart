import 'dart:async';

import 'package:argus/models/changes.dart';
import 'package:argus/models/project.dart';
import 'package:argus/models/workspace_files.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/navigation.dart';
import 'package:argus/state/workspace.dart';
import 'package:argus/transport/connection.dart';
import 'package:argus/ui/file_view_screen.dart';
import 'package:argus/ui/session_sections_list.dart';
import 'package:argus/ui/workspace_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

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

Future<ProviderContainer> _pump(
  WidgetTester tester, {
  WorkspaceNode ws = _ws,
  ProjectNode project = _project,
}) async {
  final c = ProviderContainer(
    overrides: [
      gatewayProvider.overrideWithValue(null),
      workspaceChangedFilesProvider((ws.id, '')).overrideWith(
        (ref) async => const [ChangedFile(path: 'a.dart', change: 'modified')],
      ),
      workspaceChangedFilesProvider((
        ws.id,
        'target',
      )).overrideWith((ref) async => const []),
      workspaceCommitsProvider(
        ws.id,
      ).overrideWith((ref) async => const CommitList()),
      workspaceDirProvider((ws.id, '')).overrideWith(
        (ref) async => const DirListing(
          entries: [
            DirEntry(name: 'lib', path: 'lib', isDir: true),
            DirEntry(name: 'main.dart', path: 'main.dart'),
          ],
        ),
      ),
      workspaceDirProvider((ws.id, 'lib')).overrideWith(
        (ref) async => const DirListing(
          path: 'lib',
          entries: [DirEntry(name: 'app.dart', path: 'lib/app.dart')],
        ),
      ),
      workspaceDirProvider((
        ws.id,
        'broken',
      )).overrideWith((ref) async => throw StateError('no such folder')),
      workspaceDirProvider((ws.id, 'links')).overrideWith(
        (ref) async => const DirListing(
          path: 'links',
          entries: [
            DirEntry(
              name: 'cfg',
              path: 'links/cfg',
              symlink: true,
              target: '../etc/cfg',
            ),
          ],
        ),
      ),
      workspaceFileProvider((ws.id, 'main.dart')).overrideWith(
        (ref) async =>
            const FileContent(path: 'main.dart', content: 'void main() {}'),
      ),
      workspaceFileProvider((ws.id, 'bin.dat')).overrideWith(
        (ref) async => const FileContent(path: 'bin.dat', notShown: true),
      ),
    ],
  );
  addTearDown(c.dispose);
  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: c,
      child: MaterialApp(
        home: WorkspaceScreen(project: project, workspace: ws),
      ),
    ),
  );
  await tester.pump();
  return c;
}

void main() {
  testWidgets('header shows name, project, branch, and target', (tester) async {
    await _pump(tester);
    expect(find.text('registry'), findsOneWidget);
    expect(find.text('argus · feat/registry → main'), findsOneWidget);
    expect(find.text('No sessions in this workspace.'), findsOneWidget);
    expect(find.byTooltip('New session'), findsOneWidget);
  });

  testWidgets('tabs switch and are remembered per workspace', (tester) async {
    final c = await _pump(tester);
    await tester.tap(find.text('Changes'));
    await tester.pumpAndSettle();
    expect(c.read(workspaceTabProvider('A:w2')), WorkspaceTab.changes);
    expect(find.textContaining('a.dart'), findsOneWidget);
    await tester.tap(find.text('vs main'));
    await tester.pumpAndSettle();
    expect(c.read(changesAgainstProvider('A:w2')), 'target');
    expect(find.text('No changes against main.'), findsOneWidget);
  });

  testWidgets('no target disables vs target', (tester) async {
    const ws = WorkspaceNode(id: 'A:w9', dir: '/x', branch: 'b');
    const project = ProjectNode(id: 'A:p9', name: 'x', workspaces: [ws]);
    final c = ProviderContainer(
      overrides: [
        gatewayProvider.overrideWithValue(null),
        workspaceChangedFilesProvider((
          'A:w9',
          '',
        )).overrideWith((ref) async => const []),
        workspaceCommitsProvider(
          'A:w9',
        ).overrideWith((ref) async => const CommitList()),
      ],
    );
    addTearDown(c.dispose);
    c.read(workspaceTabProvider('A:w9').notifier).state = WorkspaceTab.changes;
    c.read(changesAgainstProvider('A:w9').notifier).state = 'target';
    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: c,
        child: const MaterialApp(
          home: WorkspaceScreen(project: project, workspace: ws),
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('No uncommitted changes.'), findsOneWidget);
    await tester.tap(find.text('vs target'));
    await tester.pumpAndSettle();
    expect(
      c.read(changesAgainstProvider('A:w9')),
      'target',
    ); // unchanged: disabled
    expect(find.text('No uncommitted changes.'), findsOneWidget);
  });

  testWidgets('plain projects have no git changes', (tester) async {
    const ws = WorkspaceNode(id: 'A:w8', dir: '/notes');
    const project = ProjectNode(
      id: 'A:p8',
      name: 'notes',
      kind: 'plain',
      workspaces: [ws],
    );
    final c = await _pump(tester, ws: ws, project: project);
    c.read(workspaceTabProvider('A:w8').notifier).state = WorkspaceTab.changes;
    await tester.pump();
    expect(find.text('Not a git repository.'), findsOneWidget);
  });

  testWidgets('files: folders open in place, files push the file view', (
    tester,
  ) async {
    final c = await _pump(tester);
    await tester.tap(find.text('Files'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('lib'));
    await tester.pumpAndSettle();
    expect(c.read(filesPathProvider('A:w2')), 'lib');
    expect(find.text('app.dart'), findsOneWidget);
    await tester.tap(find.byKey(const Key('files-root'))); // breadcrumb root
    await tester.pumpAndSettle();
    expect(c.read(filesPathProvider('A:w2')), '');
    await tester.tap(find.text('main.dart'));
    await tester.pumpAndSettle();
    expect(find.byType(FileViewScreen), findsOneWidget);
    expect(find.textContaining('void main'), findsWidgets);
  });

  testWidgets('sessions tab shows the reconnect banner when disconnected', (
    tester,
  ) async {
    final c = await _pump(tester);
    expect(find.byType(ReconnectBanner), findsOneWidget);
    expect(find.text('Disconnected'), findsOneWidget);
    c.read(connStateProvider.notifier).state = ConnState.connected;
    await tester.pump();
    expect(find.byType(ReconnectBanner), findsNothing);
  });

  testWidgets('files: a failed folder shows the error with Retry', (
    tester,
  ) async {
    final c = await _pump(tester);
    c.read(workspaceTabProvider('A:w2').notifier).state = WorkspaceTab.files;
    c.read(filesPathProvider('A:w2').notifier).state = 'broken';
    await tester.pumpAndSettle();
    expect(find.textContaining('Could not open this folder'), findsOneWidget);
    expect(find.textContaining('no such folder'), findsOneWidget);
    expect(find.text('Retry'), findsOneWidget);
  });

  testWidgets('files: a symlink shows its target', (tester) async {
    final c = await _pump(tester);
    c.read(workspaceTabProvider('A:w2').notifier).state = WorkspaceTab.files;
    c.read(filesPathProvider('A:w2').notifier).state = 'links';
    await tester.pumpAndSettle();
    expect(find.text('cfg'), findsOneWidget);
    expect(find.text('→ ../etc/cfg'), findsOneWidget);
    expect(find.byIcon(Icons.link), findsOneWidget);
  });

  testWidgets('files: each breadcrumb has a phone-sized tap target', (
    tester,
  ) async {
    final c = await _pump(tester);
    c.read(workspaceTabProvider('A:w2').notifier).state = WorkspaceTab.files;
    c.read(filesPathProvider('A:w2').notifier).state = 'lib';
    await tester.pumpAndSettle();
    expect(
      tester.getSize(find.byKey(const Key('files-root'))).height,
      greaterThanOrEqualTo(40),
    );
    expect(
      tester
          .getSize(
            find
                .ancestor(of: find.text('lib'), matching: find.byType(InkWell))
                .first,
          )
          .height,
      greaterThanOrEqualTo(40),
    );
  });

  testWidgets('file view shows the not-shown notice', (tester) async {
    await _pump(tester);
    final nav = tester.state<NavigatorState>(find.byType(Navigator));
    nav.push(
      MaterialPageRoute(
        builder: (_) =>
            const FileViewScreen(workspaceId: 'A:w2', path: 'bin.dat'),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('Binary or too large to show.'), findsOneWidget);
  });

  testWidgets('vs target lists files flat, with no staged groups', (
    tester,
  ) async {
    final c = ProviderContainer(
      overrides: [
        gatewayProvider.overrideWithValue(null),
        workspaceChangedFilesProvider((_ws.id, 'target')).overrideWith(
          (ref) async => const [
            ChangedFile(path: 'a.dart', change: 'modified'),
            ChangedFile(path: 'b.dart', change: 'added'),
            ChangedFile(path: 'n.txt', change: 'untracked'),
          ],
        ),
        workspaceCommitsProvider(_ws.id).overrideWith(
          (ref) async => const CommitList(
            commits: [
              Commit(sha: 's1', short: 's1', subject: 'x', author: 'me', unixSec: 0),
            ],
          ),
        ),
      ],
    );
    addTearDown(c.dispose);
    c.read(workspaceTabProvider(_ws.id).notifier).state = WorkspaceTab.changes;
    c.read(changesAgainstProvider(_ws.id).notifier).state = 'target';
    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: c,
        child: const MaterialApp(
          home: WorkspaceScreen(project: _project, workspace: _ws),
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.textContaining('CHANGED VS MAIN (3)'), findsOneWidget);
    expect(find.textContaining('UNSTAGED'), findsNothing);
    expect(find.textContaining('UNTRACKED'), findsNothing);
    expect(find.textContaining('COMMITS VS MAIN (1)'), findsOneWidget);
  });

  testWidgets('the Changes switch keeps its margin while loading', (
    tester,
  ) async {
    final pending = Completer<List<ChangedFile>>();
    final c = ProviderContainer(
      overrides: [
        gatewayProvider.overrideWithValue(null),
        workspaceChangedFilesProvider(
          (_ws.id, ''),
        ).overrideWith((ref) => pending.future),
        workspaceCommitsProvider(
          _ws.id,
        ).overrideWith((ref) async => const CommitList()),
      ],
    );
    addTearDown(c.dispose);
    c.read(workspaceTabProvider(_ws.id).notifier).state = WorkspaceTab.changes;
    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: c,
        child: const MaterialApp(
          home: WorkspaceScreen(project: _project, workspace: _ws),
        ),
      ),
    );
    await tester.pump();
    final loadingLeft = tester
        .getTopLeft(find.byType(SegmentedButton<String>))
        .dx;
    pending.complete(const []);
    await tester.pumpAndSettle();
    final loadedLeft = tester
        .getTopLeft(find.byType(SegmentedButton<String>))
        .dx;
    expect(loadingLeft, loadedLeft);
    expect(loadingLeft, greaterThan(0));
  });

  testWidgets('the ⋮ menu lists the workspace actions', (tester) async {
    await _pump(tester);
    await tester.tap(find.byTooltip('Workspace actions'));
    await tester.pumpAndSettle();
    expect(find.text('Change target'), findsOneWidget);
    expect(find.text('Remove'), findsOneWidget);
    expect(find.text('Force remove'), findsOneWidget);
  });
}
