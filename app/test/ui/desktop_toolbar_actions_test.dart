import 'package:argus/models/project.dart';
import 'package:argus/models/terminal.dart';
import 'package:argus/state/control.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/grouping.dart';
import 'package:argus/state/terminals.dart';
import 'package:argus/state/terminals_api.dart';
import 'package:argus/state/workspace.dart';
import 'package:argus/models/changes.dart';
import 'package:argus/models/workspace_files.dart';
import 'package:argus/transport/gateway_client.dart';
import 'package:argus/ui/session_list_screen.dart';
import 'package:argus/ui/spawn_dialog.dart';
import 'package:argus/ui/terminal_list_screen.dart';
import 'package:argus/ui/workspace_screen.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_riverpod/misc.dart' show Override;
import 'package:flutter_test/flutter_test.dart';

class _Terminals extends TerminalsNotifier {
  @override
  TerminalsState build() => const TerminalsState(loaded: true);
  @override
  Future<void> load(GatewayClient? client) async {}
}

class _Api implements TerminalsApi {
  final created = <(String?, String?)>[];
  @override
  Future<NodeTerminal> create(String? nodeId, {String? workspaceId}) async {
    created.add((nodeId, workspaceId));
    return NodeTerminal(id: '$nodeId:@9', command: 'zsh');
  }

  @override
  Future<void> kill(String id) async {}
  @override
  Future<void> rename(String id, String name) async {}
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

Finder _inAppBar(String tooltip) =>
    find.descendant(of: find.byType(AppBar), matching: find.byTooltip(tooltip));

Future<_Api> _pumpScreen(WidgetTester tester, Widget screen) async {
  final api = _Api();
  await tester.pumpWidget(
    ProviderScope(
      overrides: <Override>[
        gatewayProvider.overrideWithValue(null),
        terminalsProvider.overrideWith(() => _Terminals()),
        terminalsApiProvider.overrideWithValue(api),
        serverInfoProvider.overrideWith(
          (ref) async => ServerInfo(
            version: '',
            nodes: [NodeRef('A', 'alpha', terminalSupported: true)],
          ),
        ),
        workspaceChangedFilesProvider((_ws.id, '')).overrideWith(
          (ref) async => const [
            ChangedFile(path: 'a.dart', change: 'modified'),
          ],
        ),
        workspaceCommitsProvider(
          _ws.id,
        ).overrideWith((ref) async => const CommitList()),
        workspaceDirProvider((
          _ws.id,
          '',
        )).overrideWith((ref) async => const DirListing(entries: [])),
      ],
      child: MaterialApp(home: screen),
    ),
  );
  await tester.pump();
  await tester.pump();
  return api;
}

void _platform(TargetPlatform p) {
  debugDefaultTargetPlatformOverride = p;
  addTearDown(() => debugDefaultTargetPlatformOverride = null);
}

void main() {
  group('on macOS the floating buttons become toolbar actions', () {
    testWidgets('session list', (tester) async {
      _platform(TargetPlatform.macOS);
      await _pumpScreen(tester, const SessionListScreen());
      expect(find.byType(FloatingActionButton), findsNothing);
      expect(_inAppBar('New session'), findsOneWidget);
      await tester.tap(_inAppBar('New session'));
      await tester.pumpAndSettle();
      expect(find.byType(SpawnDialog), findsOneWidget);
    });

    testWidgets('terminal list', (tester) async {
      _platform(TargetPlatform.macOS);
      final api = await _pumpScreen(tester, const TerminalListScreen());
      expect(find.byType(FloatingActionButton), findsNothing);
      expect(_inAppBar('New terminal'), findsOneWidget);
      await tester.tap(_inAppBar('New terminal'));
      await tester.pump();
      await tester.pump();
      expect(api.created, hasLength(1));
    });

    testWidgets('workspace follows the tab', (tester) async {
      _platform(TargetPlatform.macOS);
      final api = await _pumpScreen(
        tester,
        const WorkspaceScreen(project: _project, workspace: _ws),
      );
      expect(find.byType(FloatingActionButton), findsNothing);
      expect(_inAppBar('New session'), findsOneWidget);
      expect(_inAppBar('New terminal'), findsNothing);

      // At 800 px the toolbar tabs are icons, with the labels as tooltips.
      await tester.tap(find.byTooltip('Terminals'));
      await tester.pump();
      expect(find.byType(FloatingActionButton), findsNothing);
      expect(_inAppBar('New session'), findsNothing);
      expect(_inAppBar('New terminal'), findsOneWidget);

      await tester.tap(find.byTooltip('Changes'));
      await tester.pump();
      expect(find.byType(FloatingActionButton), findsNothing);
      expect(_inAppBar('New session'), findsNothing);
      expect(_inAppBar('New terminal'), findsNothing);

      await tester.tap(find.byTooltip('Terminals'));
      await tester.pump();
      await tester.tap(_inAppBar('New terminal'));
      await tester.pump();
      expect(api.created, [('A', 'w2')]);
    });

    testWidgets('workspace session action opens the spawn dialog', (
      tester,
    ) async {
      _platform(TargetPlatform.macOS);
      await _pumpScreen(
        tester,
        const WorkspaceScreen(project: _project, workspace: _ws),
      );
      await tester.tap(_inAppBar('New session'));
      await tester.pumpAndSettle();
      expect(find.byType(SpawnDialog), findsOneWidget);
    });
  });

  group('on Android the floating buttons stay', () {
    testWidgets('session list', (tester) async {
      _platform(TargetPlatform.android);
      await _pumpScreen(tester, const SessionListScreen());
      expect(find.byType(FloatingActionButton), findsOneWidget);
      expect(_inAppBar('New session'), findsNothing);
    });

    testWidgets('terminal list', (tester) async {
      _platform(TargetPlatform.android);
      await _pumpScreen(tester, const TerminalListScreen());
      expect(find.byType(FloatingActionButton), findsOneWidget);
      expect(_inAppBar('New terminal'), findsNothing);
    });

    testWidgets('workspace sessions and terminals tabs', (tester) async {
      _platform(TargetPlatform.android);
      await _pumpScreen(
        tester,
        const WorkspaceScreen(project: _project, workspace: _ws),
      );
      expect(find.byType(FloatingActionButton), findsOneWidget);
      expect(_inAppBar('New session'), findsNothing);
      await tester.tap(find.text('Terminals'));
      await tester.pump();
      expect(find.byType(FloatingActionButton), findsOneWidget);
      expect(_inAppBar('New terminal'), findsNothing);
    });
  });
}
