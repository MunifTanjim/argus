import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/project.dart';
import 'package:argus/models/terminal.dart';
import 'package:argus/state/control.dart';
import 'package:argus/state/grouping.dart';
import 'package:argus/state/projects.dart';
import 'package:argus/state/terminals.dart';
import 'package:argus/state/terminals_api.dart';
import 'package:argus/transport/gateway_client.dart';
import 'package:argus/ui/details_header.dart';
import 'package:argus/ui/terminal_list_screen.dart';

class _Seeded extends TerminalsNotifier {
  _Seeded(this.seed);
  final TerminalsState seed;
  var loads = 0;
  @override
  TerminalsState build() => seed;
  @override
  Future<void> load(GatewayClient? client) async => loads++;
}

class _FakeApi implements TerminalsApi {
  Object? failure;
  final killed = <String>[];
  final renamed = <(String, String)>[];
  @override
  Future<NodeTerminal> create(String? nodeId, {String? workspaceId}) async =>
      NodeTerminal(id: '${nodeId ?? 'A'}:@9', command: 'zsh');
  @override
  Future<void> kill(String id) async {
    if (failure != null) throw failure!;
    killed.add(id);
  }

  @override
  Future<void> rename(String id, String name) async => renamed.add((id, name));
}

const _terms = [
  NodeTerminal(
    id: 'A:@1',
    nodeId: 'A',
    nodeLabel: 'home',
    command: 'zsh',
    cwd: '~',
  ),
  NodeTerminal(
    id: 'A:@2',
    nodeId: 'A',
    nodeLabel: 'home',
    name: 'build',
    command: 'make',
    cwd: '~/src',
    attached: true,
  ),
];

late _Seeded _notifier;

class _Projects extends ProjectsNotifier {
  _Projects(this.projects);
  final List<ProjectNode> projects;
  @override
  ProjectsState build() => ProjectsState(projects: projects, loaded: true);
}

Future<_FakeApi> _pump(
  WidgetTester tester,
  List<NodeTerminal> terms, {
  List<NodeRef> nodes = const [],
  TerminalsState? state,
  List<ProjectNode> projects = const [],
}) async {
  final api = _FakeApi();
  _notifier = _Seeded(state ?? TerminalsState(terminals: terms, loaded: true));
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        terminalsProvider.overrideWith(() => _notifier),
        terminalsApiProvider.overrideWithValue(api),
        projectsProvider.overrideWith(() => _Projects(projects)),
        serverInfoProvider.overrideWith(
          (ref) async => ServerInfo(version: '', nodes: nodes),
        ),
      ],
      child: const MaterialApp(home: TerminalListScreen()),
    ),
  );
  await tester.pump();
  return api;
}

void main() {
  testWidgets('lists terminals with title and cwd', (tester) async {
    await _pump(tester, _terms);
    expect(find.text('zsh'), findsOneWidget);
    expect(find.text('build'), findsOneWidget);
    expect(find.text('~/src'), findsOneWidget);
  });

  testWidgets('empty list says so', (tester) async {
    await _pump(tester, const []);
    expect(find.text('No terminals.'), findsOneWidget);
  });

  testWidgets('shows loading before the first load', (tester) async {
    await _pump(tester, const [], state: const TerminalsState());
    expect(find.byKey(const Key('terminals-loading')), findsOneWidget);
    expect(find.text('No terminals.'), findsNothing);
  });

  testWidgets('shows a load error', (tester) async {
    await _pump(tester, const [], state: const TerminalsState(error: 'down'));
    expect(find.text('Could not load terminals: down'), findsOneWidget);
  });

  testWidgets('a failed kill reloads the list', (tester) async {
    final api = await _pump(tester, _terms);
    api.failure = StateError('unknown terminal: @1');
    await tester.longPress(find.text('zsh'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Kill'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(FilledButton, 'Kill'));
    await tester.pumpAndSettle();
    expect(find.textContaining('kill failed'), findsOneWidget);
    expect(_notifier.loads, 1);
  });

  testWidgets('long-press kill asks, then kills', (tester) async {
    final api = await _pump(tester, _terms);
    await tester.longPress(find.text('zsh'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Kill'));
    await tester.pumpAndSettle();
    expect(find.text('Kill terminal zsh?'), findsOneWidget);
    await tester.tap(find.widgetWithText(FilledButton, 'Kill'));
    await tester.pumpAndSettle();
    expect(api.killed, ['A:@1']);
  });

  testWidgets('long-press rename sends the new name', (tester) async {
    final api = await _pump(tester, _terms);
    await tester.longPress(find.text('zsh'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Rename'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField), 'logs');
    await tester.pump();
    await tester.tap(find.text('Save'));
    await tester.pumpAndSettle();
    expect(api.renamed, [('A:@1', 'logs')]);
  });

  testWidgets('names the node when several nodes can run terminals', (
    tester,
  ) async {
    await _pump(
      tester,
      const [
        NodeTerminal(
          id: 'A:@1',
          nodeId: 'A',
          nodeLabel: 'alpha',
          command: 'zsh',
        ),
      ],
      nodes: const [NodeRef('A', 'alpha'), NodeRef('B', 'beta')],
    );
    await tester.pump();
    expect(find.text('ALPHA'), findsOneWidget);
    expect(find.byIcon(Icons.dns_outlined), findsOneWidget);
    expect(find.byTooltip('New terminal'), findsOneWidget);
  });

  testWidgets('a terminal on an outdated node is marked and takes no actions', (
    tester,
  ) async {
    await _pump(
      tester,
      const [
        NodeTerminal(
          id: 'A:@1',
          nodeId: 'A',
          nodeLabel: 'alpha',
          command: 'zsh',
        ),
      ],
      nodes: const [
        NodeRef('A', 'alpha', version: '0.1.1'),
        NodeRef('B', 'beta'),
      ],
    );
    await tester.pump();
    expect(find.text('ALPHA (OUTDATED)'), findsOneWidget);
    expect(
      tester.widget<ListTile>(find.widgetWithText(ListTile, 'zsh')).enabled,
      isFalse,
    );
    await tester.longPress(find.text('zsh'), warnIfMissed: false);
    await tester.pumpAndSettle();
    expect(find.text('Kill'), findsNothing);
  });

  testWidgets('a tap on a node header folds and unfolds its terminals', (
    tester,
  ) async {
    await _pump(
      tester,
      const [
        NodeTerminal(
          id: 'A:@1',
          nodeId: 'A',
          nodeLabel: 'alpha',
          command: 'zsh',
        ),
        NodeTerminal(
          id: 'B:@1',
          nodeId: 'B',
          nodeLabel: 'beta',
          command: 'htop',
        ),
      ],
      nodes: const [NodeRef('A', 'alpha'), NodeRef('B', 'beta')],
    );
    await tester.pump();
    expect(find.text('zsh'), findsOneWidget);

    await tester.tap(find.text('ALPHA'));
    await tester.pump();
    expect(find.text('zsh'), findsNothing);
    expect(find.text('htop'), findsOneWidget);
    expect(find.byIcon(Icons.chevron_right), findsOneWidget);

    await tester.tap(find.text('ALPHA'));
    await tester.pump();
    expect(find.text('zsh'), findsOneWidget);
  });

  testWidgets('the terminal sheet shows its details and pins Kill and Rename', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(400, 520));
    addTearDown(() => tester.binding.setSurfaceSize(null));
    await _pump(
      tester,
      const [
        NodeTerminal(
          id: 'A:@2',
          nodeId: 'A',
          nodeLabel: 'home',
          name: 'build',
          command: 'make',
          cwd: '~/src/argus/.worktrees/registry',
          attached: true,
          workspaceId: 'A:w2',
        ),
      ],
      projects: [
        ProjectNode.fromJson({
          'id': 'A:p1',
          'name': 'argus',
          'node_id': 'A',
          'workspaces': [
            {'id': 'A:w2', 'dir': '/src/argus/.worktrees/registry'},
          ],
        }),
      ],
    );
    await tester.longPress(find.text('build'));
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    final header = tester.widget<DetailsHeader>(find.byType(DetailsHeader));
    expect(header.label, 'argus / registry');
    expect(header.title, 'build');
    expect(header.node, 'home');
    expect(header.meta.map((m) => m.$1), ['open elsewhere']);
    for (final v in ['make', '~/src/argus/.worktrees/registry', 'A:@2']) {
      expect(
        find.descendant(of: find.byType(BottomSheet), matching: find.text(v)),
        findsOneWidget,
        reason: v,
      );
    }
    final bar = find.byType(DetailsActionBar);
    expect(
      find.descendant(of: bar, matching: find.text('Kill')).hitTestable(),
      findsOneWidget,
    );
    expect(
      find.descendant(of: bar, matching: find.text('Rename')).hitTestable(),
      findsOneWidget,
    );
    expect(
      tester.getCenter(find.text('Kill')).dx,
      lessThan(tester.getCenter(find.text('Rename')).dx),
    );
  });
}
