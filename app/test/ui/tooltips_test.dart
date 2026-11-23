import 'package:argus/models/changes.dart';
import 'package:argus/models/entry.dart';
import 'package:argus/models/enums.dart';
import 'package:argus/models/project.dart';
import 'package:argus/models/session.dart';
import 'package:argus/models/workspace_files.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/pairing/profile_store.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/profiles.dart';
import 'package:argus/state/projects.dart';
import 'package:argus/state/sessions.dart';
import 'package:argus/state/tool_detail.dart';
import 'package:argus/state/transcript.dart';
import 'package:argus/state/transcript_controller.dart';
import 'package:argus/state/workspace.dart';
import 'package:argus/transport/key_library_store.dart';
import 'package:argus/transport/library_key.dart';
import 'package:argus/ui/entry_row.dart';
import 'package:argus/ui/home_shell.dart';
import 'package:argus/ui/key_library_screen.dart';
import 'package:argus/ui/session_detail_screen.dart';
import 'package:argus/ui/workspace_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../support/fake_gateway_client.dart';

class _MemKv implements SecureKv {
  final m = <String, String>{};
  @override
  Future<String?> read(String key) async => m[key];
  @override
  Future<void> write(String key, String value) async => m[key] = value;
  @override
  Future<void> delete(String key) async => m.remove(key);
}

class _Seeded extends TranscriptNotifier {
  @override
  TranscriptState build() => const TranscriptState(
        subId: 'x',
        entries: [Entry(id: 'u', kind: EntryKind.user, text: 'hello')],
        loaded: true,
      );
}

const _ws = WorkspaceNode(
  id: 'A:w1',
  dir: '/src/argus',
  branch: 'main',
  isMain: true,
);
const _project = ProjectNode(
  id: 'A:p1',
  name: 'argus',
  nodeId: 'A',
  workspaces: [_ws],
);

Session _session() => Session.fromJson({
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
      'workspace_id': 'A:w1',
      'node_label': 'mac',
      'node_id': 'A',
    });

class _Projects extends ProjectsNotifier {
  @override
  ProjectsState build() =>
      const ProjectsState(projects: [_project], loaded: true);
}

void _expectAllTooltipped(WidgetTester tester) {
  final missing = <String>[];
  for (final b in tester.widgetList<IconButton>(find.byType(IconButton))) {
    if (b.tooltip == null || b.tooltip!.isEmpty) {
      missing.add('${b.icon} key=${b.key}');
    }
  }
  expect(missing, isEmpty, reason: 'IconButtons without tooltip');
}

Future<void> _wide(WidgetTester tester) async {
  tester.view.physicalSize = const Size(1200, 900);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
}

void main() {
  testWidgets('every IconButton in the home shell has a tooltip',
      (tester) async {
    await _wide(tester);
    final c = ProviderContainer(overrides: [
      gatewayProvider.overrideWithValue(null),
      projectsProvider.overrideWith(_Projects.new),
    ]);
    addTearDown(c.dispose);
    c.read(sessionsProvider.notifier).replaceAll([_session()]);
    await tester.pumpWidget(UncontrolledProviderScope(
      container: c,
      child: const MaterialApp(home: HomeShell()),
    ));
    await tester.pump();
    expect(find.byType(IconButton), findsWidgets);
    _expectAllTooltipped(tester);
  });

  testWidgets('every IconButton in session detail has a tooltip',
      (tester) async {
    await _wide(tester);
    await tester.pumpWidget(ProviderScope(
      overrides: [
        gatewayProvider.overrideWithValue(null),
        projectsProvider.overrideWith(_Projects.new),
        transcriptProvider('mac:%1').overrideWith(_Seeded.new),
      ],
      child: MaterialApp(home: SessionDetailScreen(session: _session())),
    ));
    await tester.pump();
    expect(find.byType(IconButton), findsWidgets);
    _expectAllTooltipped(tester);
  });

  testWidgets('every IconButton in the workspace screen has a tooltip',
      (tester) async {
    await _wide(tester);
    await tester.pumpWidget(ProviderScope(
      overrides: [
        gatewayProvider.overrideWithValue(null),
        workspaceChangedFilesProvider(('A:w1', '')).overrideWith(
          (ref) async =>
              const [ChangedFile(path: 'a.dart', change: 'modified')],
        ),
        workspaceChangedFilesProvider(('A:w1', 'target'))
            .overrideWith((ref) async => const []),
        workspaceCommitsProvider('A:w1')
            .overrideWith((ref) async => const CommitList()),
        workspaceDirProvider(('A:w1', '')).overrideWith(
          (ref) async => const DirListing(
            entries: [DirEntry(name: 'main.dart', path: 'main.dart')],
          ),
        ),
      ],
      child: const MaterialApp(
        home: WorkspaceScreen(project: _project, workspace: _ws),
      ),
    ));
    await tester.pump();
    expect(find.byType(IconButton), findsWidgets);
    _expectAllTooltipped(tester);
  });

  testWidgets('every IconButton in the key library has a tooltip',
      (tester) async {
    final keys = KeyLibraryStore(_MemKv());
    await keys.add(const LibraryKey(id: 'k1', name: 'laptop', pem: 'P'));
    await tester.pumpWidget(ProviderScope(
      overrides: [
        keyLibraryStoreProvider.overrideWithValue(keys),
        profileStoreProvider.overrideWithValue(ProfileStore(_MemKv())),
      ],
      child: const MaterialApp(home: KeyLibraryScreen()),
    ));
    await tester.pumpAndSettle();
    expect(find.byType(IconButton), findsWidgets);
    _expectAllTooltipped(tester);
  });

  testWidgets('the Show more chip shows a click cursor', (tester) async {
    final long = List.generate(30, (i) => 'line $i').join('\n');
    await tester.pumpWidget(ProviderScope(
      overrides: [gatewayProvider.overrideWithValue(null)],
      child: MaterialApp(
        home: Scaffold(
          body: SingleChildScrollView(
            child: EntryRow(
              detailRef: const ToolDetailRef.live('s'),
              entry: Entry(id: 'u', kind: EntryKind.user, text: long),
              expanded: false,
              onToggle: () {},
            ),
          ),
        ),
      ),
    ));
    final regions = tester.widgetList<MouseRegion>(find.ancestor(
      of: find.text('Show more'),
      matching: find.byType(MouseRegion),
    ));
    expect(regions.map((r) => r.cursor), contains(SystemMouseCursors.click));
  });
}
