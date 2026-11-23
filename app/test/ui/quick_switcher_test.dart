import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/enums.dart';
import 'package:argus/models/project.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/navigation.dart';
import 'package:argus/state/projects.dart';
import 'package:argus/state/push.dart';
import 'package:argus/state/sessions.dart';
import 'package:argus/ui/home_shell.dart';
import 'package:argus/ui/quick_switcher.dart';

import '../support/fake_gateway_client.dart';

Session _s(
  String id, {
  SessionStatus status = SessionStatus.awaitingInput,
  DateTime? since,
  String? repo,
  String? branch,
}) => Session(
  id: id,
  agent: 'claude',
  repo: repo,
  branch: branch,
  statusSince: since,
  tmux: const TmuxLocation(
    server: TmuxServerKind.default_,
    paneId: '',
    sessionName: '',
    windowIndex: 0,
    currentPath: '',
  ),
  status: status,
  source: SessionSource.discovered,
  inputMode: InputMode.pane,
);

const _projects = [
  ProjectNode(
    id: 'p1',
    name: 'argus',
    workspaces: [
      WorkspaceNode(id: 'w1', dir: '/src/argus', branch: 'main', isMain: true),
      WorkspaceNode(id: 'w2', dir: '/src/argus-feat', branch: 'feat/x'),
      WorkspaceNode(
        id: 'w3',
        dir: '/src/argus-old',
        branch: 'old',
        isGone: true,
      ),
    ],
  ),
];

List<String> _titles(List<SwitcherEntry> es) => [for (final e in es) e.title];

void main() {
  test('switcherEntries drops dead sessions', () {
    final es = switcherEntries(
      [_s('a'), _s('b', status: SessionStatus.dead)],
      const [],
      '',
    );
    expect(_titles(es), ['a']);
  });

  test(
    'switcherEntries puts sessions before workspaces, most recent first',
    () {
      final es = switcherEntries(
        [
          _s('none'),
          _s('old', since: DateTime(2026, 1, 1)),
          _s('new', since: DateTime(2026, 2, 1)),
        ],
        _projects,
        '',
      );
      expect(_titles(es), [
        'new',
        'old',
        'none',
        'argus · argus',
        'argus · argus-feat',
      ]);
    },
  );

  test('switcherEntries matches the branch case-insensitively', () {
    final es = switcherEntries(
      [_s('a', branch: 'feat/y'), _s('b', branch: 'main')],
      _projects,
      'FEAT',
    );
    expect(_titles(es), ['a', 'argus · argus-feat']);
  });

  test('switcherEntries drops gone workspaces', () {
    final es = switcherEntries(const [], _projects, 'old');
    expect(es, isEmpty);
  });

  Future<ProviderContainer> pumpSwitcher(
    WidgetTester tester,
    List<Session> sessions,
  ) async {
    final c = ProviderContainer(
      overrides: [gatewayProvider.overrideWithValue(null)],
    );
    addTearDown(c.dispose);
    await c
        .read(projectsProvider.notifier)
        .load(
          FakeGatewayClient(
            (m, p) async => {
              'projects': [
                {
                  'id': 'p1',
                  'name': 'argus',
                  'workspaces': [
                    {'id': 'w1', 'dir': '/src/argus', 'branch': 'main'},
                  ],
                },
              ],
            },
          ),
        );
    c.read(sessionsProvider.notifier).replaceAll(sessions);
    late BuildContext ctx;
    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: c,
        child: MaterialApp(
          home: Builder(
            builder: (context) {
              ctx = context;
              return const Scaffold();
            },
          ),
        ),
      ),
    );
    showQuickSwitcher(ctx);
    await tester.pumpAndSettle();
    return c;
  }

  testWidgets('Enter on a session row sets pendingOpenSessionProvider', (
    tester,
  ) async {
    final c = await pumpSwitcher(tester, [_s('s1')]);
    await tester.sendKeyEvent(LogicalKeyboardKey.enter);
    await tester.pumpAndSettle();
    expect(c.read(pendingOpenSessionProvider)?.sessionId, 's1');
    expect(find.byType(TextField), findsNothing);
  });

  testWidgets('Enter on a workspace row sets scopeProvider', (tester) async {
    final c = await pumpSwitcher(tester, const []);
    await tester.sendKeyEvent(LogicalKeyboardKey.enter);
    await tester.pumpAndSettle();
    expect(c.read(scopeProvider), 'w1');
  });

  testWidgets('Down then Enter opens the second row', (tester) async {
    final c = await pumpSwitcher(tester, [_s('s1')]);
    await tester.sendKeyEvent(LogicalKeyboardKey.arrowDown);
    await tester.sendKeyEvent(LogicalKeyboardKey.enter);
    await tester.pumpAndSettle();
    expect(c.read(scopeProvider), 'w1');
    expect(c.read(pendingOpenSessionProvider), isNull);
  });

  testWidgets('Down keeps the selected row inside the list', (tester) async {
    await pumpSwitcher(tester, [
      for (var i = 0; i < 20; i++) _s('s$i', repo: 'repo$i'),
    ]);
    final viewport = tester.getRect(find.byType(ListView));
    for (var i = 0; i < 19; i++) {
      await tester.sendKeyEvent(LogicalKeyboardKey.arrowDown);
      await tester.pumpAndSettle();
      final row = tester.getRect(
        find.byWidgetPredicate((w) => w is ListTile && w.selected),
      );
      expect(
        row.top >= viewport.top && row.bottom <= viewport.bottom,
        isTrue,
        reason: 'row ${i + 1} at $row outside $viewport',
      );
    }
  });

  testWidgets('showQuickSwitcher while one is open does not stack another', (
    tester,
  ) async {
    await pumpSwitcher(tester, [_s('s1')]);
    final ctx = tester.element(find.byType(TextField));
    showQuickSwitcher(ctx);
    await tester.pumpAndSettle();
    expect(find.byType(TextField), findsOneWidget);
    await tester.sendKeyEvent(LogicalKeyboardKey.escape);
    await tester.pumpAndSettle();
    expect(find.byType(TextField), findsNothing);
  });

  test('switcherEntries orders equal statusSince by id', () {
    final t = DateTime(2026, 1, 1);
    final es = switcherEntries(
      [_s('b', since: t), _s('a', since: t)],
      const [],
      '',
    );
    expect(_titles(es), ['a', 'b']);
  });

  testWidgets('Esc closes without opening', (tester) async {
    final c = await pumpSwitcher(tester, [_s('s1')]);
    await tester.sendKeyEvent(LogicalKeyboardKey.escape);
    await tester.pumpAndSettle();
    expect(find.byType(TextField), findsNothing);
    expect(c.read(pendingOpenSessionProvider), isNull);
  });

  testWidgets('typing filters the rows', (tester) async {
    await pumpSwitcher(tester, [
      _s('s1', repo: 'alpha'),
      _s('s2', repo: 'beta'),
    ]);
    await tester.enterText(find.byType(TextField), 'bet');
    await tester.pump();
    expect(find.byType(ListTile), findsOneWidget);
    expect(find.text('beta'), findsWidgets);
    expect(find.text('alpha'), findsNothing);
  });

  testWidgets('Cmd+K in HomeShell opens the switcher', (tester) async {
    debugDefaultTargetPlatformOverride = TargetPlatform.macOS;
    addTearDown(() => debugDefaultTargetPlatformOverride = null);
    tester.view.physicalSize = const Size(1200, 800);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final c = ProviderContainer(
      overrides: [gatewayProvider.overrideWithValue(null)],
    );
    addTearDown(c.dispose);
    await c
        .read(projectsProvider.notifier)
        .load(FakeGatewayClient((m, p) async => {'projects': []}));
    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: c,
        child: const MaterialApp(home: HomeShell()),
      ),
    );
    await tester.pump();
    await tester.sendKeyDownEvent(LogicalKeyboardKey.metaLeft);
    await tester.sendKeyDownEvent(LogicalKeyboardKey.keyK);
    await tester.sendKeyUpEvent(LogicalKeyboardKey.keyK);
    await tester.sendKeyUpEvent(LogicalKeyboardKey.metaLeft);
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('quick-switcher-field')), findsOneWidget);
  });
}
