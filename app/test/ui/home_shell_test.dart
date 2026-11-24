import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/cupertino.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_riverpod/legacy.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:macos_window_utils/macos_window_utils.dart';
import 'package:macos_window_utils/widgets/macos_toolbar_passthrough.dart';
import 'package:argus/core/result.dart';
import 'package:argus/data/session_repository.dart';
import 'package:argus/models/enums.dart';
import 'package:argus/models/history.dart';
import 'package:argus/models/session.dart';
import 'package:argus/push/notifications.dart';
import 'package:argus/state/control.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/grouping.dart';
import 'package:argus/state/navigation.dart';
import 'package:argus/state/projects.dart';
import 'package:argus/state/push.dart';
import 'package:argus/state/session_filter.dart';
import 'package:argus/state/sessions.dart';
import 'package:argus/state/terminals.dart';
import 'package:argus/transport/connection.dart';
import 'package:argus/transport/gateway_client.dart';
import 'package:argus/ui/history_screen.dart';
import 'package:argus/ui/history_transcript_screen.dart';
import 'package:argus/ui/home_shell.dart';
import 'package:argus/ui/live_screen_screen.dart';
import 'package:argus/ui/mac_menu_bar.dart';
import 'package:argus/ui/project_drawer.dart';
import 'package:argus/ui/resume_action.dart';
import 'package:argus/ui/session_card.dart';
import 'package:argus/ui/sidebar_sessions_list.dart';
import 'package:argus/ui/session_detail_screen.dart';
import 'package:argus/ui/session_list_screen.dart';
import 'package:argus/ui/settings_screen.dart';
import 'package:argus/ui/shell_drawer.dart';
import 'package:argus/ui/spawn_dialog.dart';
import 'package:argus/ui/terminal_list_screen.dart';
import 'package:argus/ui/theme.dart';
import 'package:argus/ui/titlebar.dart';
import 'package:argus/ui/workspace_screen.dart';

import '../support/fake_gateway_client.dart';
import '../support/fake_session_repository.dart';

class _GatedSessionRepository extends FakeSessionRepository {
  _GatedSessionRepository()
      : super(resumeResult: const Result.ok(ResumeOutcome(sessionId: 's9')));

  final gate = Completer<void>();

  @override
  Future<Result<ResumeOutcome>> resume({
    String? nodeId,
    required String agent,
    required String agentSessionId,
    required String cwd,
  }) async {
    await gate.future;
    return super.resume(
      nodeId: nodeId,
      agent: agent,
      agentSessionId: agentSessionId,
      cwd: cwd,
    );
  }
}

Session _session(String id, {String? workspaceId}) => Session(
      id: id,
      workspaceId: workspaceId,
      agent: 'claude',
      tmux: const TmuxLocation(
        server: TmuxServerKind.default_,
        paneId: '',
        sessionName: '',
        windowIndex: 0,
        currentPath: '',
      ),
      status: SessionStatus.awaitingInput,
      source: SessionSource.discovered,
    );

Map<String, dynamic> _tree({bool gone = false, bool withW2 = true}) => {
      'projects': [
        {
          'id': 'A:p1',
          'name': 'argus',
          'node_id': 'A',
          'workspaces': [
            {'id': 'A:w1', 'dir': '/src/argus', 'branch': 'main', 'is_main': true},
            if (withW2)
              {
                'id': 'A:w2',
                'dir': '/src/argus/.worktrees/registry',
                'branch': 'feat/registry',
                'is_gone': gone,
              },
          ],
        },
      ],
    };

Future<ProviderContainer> _shell(
  WidgetTester tester, {
  double width = 400,
  SessionRepository? repo,
  ValueListenable<bool>? fullScreen,
  bool menuBar = false,
  List<NodeRef>? nodes,
}) async {
  tester.view.physicalSize = Size(width, 900);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  final c = ProviderContainer(overrides: [
    gatewayProvider.overrideWithValue(null),
    if (repo != null) sessionRepositoryProvider.overrideWithValue(repo),
    if (nodes != null) ...[
      _nodesProvider.overrideWith((ref) => nodes),
      serverInfoProvider.overrideWith((ref) async =>
          ServerInfo(version: '', nodes: ref.watch(_nodesProvider))),
    ],
  ]);
  addTearDown(c.dispose);
  await c.read(projectsProvider.notifier).load(FakeGatewayClient((m, p) async => _tree()));
  await tester.pumpWidget(UncontrolledProviderScope(
    container: c,
    child: menuBar
        ? MaterialApp(
            builder: (context, child) => MacMenuBar(child: child!),
            home: const HomeShell(),
          )
        : fullScreen == null
        ? const MaterialApp(home: HomeShell())
        : _titlebarApp(fullScreen),
  ));
  await tester.pump();
  return c;
}

final _nodesProvider = StateProvider<List<NodeRef>>((ref) => const []);

Widget _titlebarApp(ValueListenable<bool> fullScreen) {
  final routes = TitlebarRouteObserver();
  addTearDown(routes.dispose);
  return MaterialApp(
    navigatorObservers: [routes],
    builder: (context, child) =>
        TitlebarScope(fullScreen: fullScreen, routes: routes, child: child!),
    home: const HomeShell(),
  );
}

class _CountingTerminals extends TerminalsNotifier {
  int loads = 0;
  @override
  Future<void> load(GatewayClient? client) async => loads++;
}

Future<void> _shellWithNodes(
  WidgetTester tester,
  List<NodeRef> nodes,
  _CountingTerminals terms,
) async {
  tester.view.physicalSize = const Size(400, 900);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  final c = ProviderContainer(overrides: [
    gatewayProvider.overrideWithValue(null),
    serverInfoProvider.overrideWith((ref) async => ServerInfo(version: '', nodes: nodes)),
    terminalsProvider.overrideWith(() => terms),
  ]);
  addTearDown(c.dispose);
  await tester.pumpWidget(UncontrolledProviderScope(
    container: c,
    child: const MaterialApp(home: HomeShell()),
  ));
  await tester.pump();
  await tester.pump();
}

Finder get _terminalsTab => find.descendant(
      of: find.byType(NavigationBar),
      matching: find.text('Terminals'),
    );

Future<void> _back(WidgetTester tester) async {
  await tester.binding.handlePopRoute();
  await tester.pumpAndSettle();
}

// SessionDetailScreen spins forever without a connection, so pump past the
// route transition instead of settling.
Future<void> _pumpRoute(WidgetTester tester) async {
  await tester.pump();
  await tester.pump(const Duration(seconds: 1));
}

// The test binding has no menu plugin.
void _mockMenuChannel(WidgetTester tester) {
  final messenger = tester.binding.defaultBinaryMessenger;
  messenger.setMockMethodCallHandler(SystemChannels.menu, (_) async => null);
  addTearDown(
      () => messenger.setMockMethodCallHandler(SystemChannels.menu, null));
}

PlatformMenuItem _menuItem(WidgetTester tester, String label) {
  Iterable<PlatformMenuItem> all(List<PlatformMenuItem> items) sync* {
    for (final item in items) {
      yield item;
      if (item is PlatformMenu) yield* all(item.menus);
      if (item is PlatformMenuItemGroup) yield* all(item.members);
    }
  }

  final menus =
      tester.widget<PlatformMenuBar>(find.byType(PlatformMenuBar)).menus;
  return all(menus).singleWhere((i) =>
      i is! PlatformMenu && i is! PlatformMenuItemGroup && i.label == label);
}

Future<void> _select(WidgetTester tester, String label) async {
  _menuItem(tester, label).onSelected!();
  await tester.pumpAndSettle();
}

Future<void> _holdOpenDrawer(WidgetTester tester) async {
  final gesture = await tester.startGesture(const Offset(5, 300));
  await tester.pump(const Duration(milliseconds: 350));
  await gesture.moveBy(const Offset(300, 0));
  await gesture.up();
  await _pumpRoute(tester);
}

Future<void> _openSessionIn(
  WidgetTester tester,
  ProviderContainer c,
  String workspaceId,
) async {
  c.read(sessionsProvider.notifier).replaceAll([
    _session('s1', workspaceId: 'A:w1'),
    _session('s2', workspaceId: 'A:w2'),
  ]);
  c.read(scopeProvider.notifier).state = workspaceId;
  await tester.pump();
  await tester.tap(find.byType(SessionCard));
  await _pumpRoute(tester);
  expect(find.byType(SessionDetailScreen), findsOneWidget);
}

Finder _card(String id) =>
    find.byWidgetPredicate((w) => w is SessionCard && w.session.id == id);

String _shownSession(WidgetTester tester) =>
    tester.widget<SessionDetailScreen>(_detailInShell).session.id;

Future<void> _switchTo(
  WidgetTester tester,
  ProviderContainer c,
  String? scope,
) async {
  c.read(scopeProvider.notifier).state = scope;
  await _pumpRoute(tester);
}

Finder get _detailInShell => find.descendant(
      of: find.byType(HomeShell),
      matching: find.byType(SessionDetailScreen),
    );

const _windowChannel = MethodChannel('macos_window_utils/window_manipulator');

// What the native side was last told: the toolbar passthrough rect per id.
final _passthroughRects = <String, Rect>{};

void main() {
  // Before any test: the package creates its init completer on first use, in
  // the zone of that use.
  setUpAll(() async {
    TestWidgetsFlutterBinding.ensureInitialized();
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(_windowChannel, (call) async {
      final args = call.arguments as Map?;
      switch (call.method) {
        case 'updateToolbarPassthroughView':
          _passthroughRects[args!['id'] as String] = Rect.fromLTWH(
            args['x'] as double,
            args['y'] as double,
            args['width'] as double,
            args['height'] as double,
          );
        case 'removeToolbarPassthroughView':
          _passthroughRects.remove(args!['id']);
        case 'isWindowFullscreened':
          return false;
      }
      return true;
    });
    await WindowManipulator.initialize();
  });

  testWidgets('a session tap in the drawer opens it and closes the drawer', (tester) async {
    final c = await _shell(tester);
    c.read(sessionsProvider.notifier).replaceAll([_session('s1')]);
    await tester.tap(find.byIcon(Icons.menu));
    await tester.pumpAndSettle();
    await tester.tap(find.descendant(
      of: find.byType(ProjectDrawer),
      matching: find.text('Sessions'),
    ));
    await tester.pumpAndSettle();
    // _session has no name or tmux session name, so its title is its agent.
    await tester.tap(find.descendant(
      of: find.byType(SidebarSessionsList),
      matching: find.text('claude'),
    ));
    await _pumpRoute(tester);
    expect(_detailInShell, findsOneWidget);
    expect(find.byType(SidebarSessionsList), findsNothing);
  });

  testWidgets('publishes the session shown in the current scope', (tester) async {
    final container = ProviderContainer(
      overrides: [gatewayProvider.overrideWithValue(null)],
    );
    addTearDown(container.dispose);
    container.read(sessionsProvider.notifier).replaceAll([_session('s1')]);
    await tester.pumpWidget(UncontrolledProviderScope(
      container: container,
      child: const MaterialApp(home: HomeShell()),
    ));
    await _pumpRoute(tester);
    expect(container.read(shownSessionProvider), isNull);

    container.read(pendingOpenSessionProvider.notifier).state =
        const PendingOpen('s1');
    await _pumpRoute(tester);
    expect(container.read(shownSessionProvider), 's1');

    await tester.binding.handlePopRoute();
    await _pumpRoute(tester);
    expect(container.read(shownSessionProvider), isNull);
  });

  testWidgets('a screen over the session keeps it shown', (tester) async {
    final c = await _shell(tester);
    await _openSessionIn(tester, c, 'A:w2');
    await _pumpRoute(tester);
    Navigator.of(tester.element(_detailInShell)).push(
      MaterialPageRoute<void>(builder: (_) => const Scaffold(body: Text('over'))),
    );
    await _pumpRoute(tester);
    expect(find.text('over'), findsOneWidget);
    expect(c.read(shownSessionProvider), 's2');

    await tester.binding.handlePopRoute();
    await _pumpRoute(tester);
    expect(c.read(shownSessionProvider), 's2');
    await tester.binding.handlePopRoute();
    await _pumpRoute(tester);
    expect(c.read(shownSessionProvider), isNull);
  });

  testWidgets('the shown session follows the scope', (tester) async {
    final c = await _shell(tester);
    await _openSessionIn(tester, c, 'A:w2');
    await _pumpRoute(tester);
    expect(c.read(shownSessionProvider), 's2');

    await _switchTo(tester, c, null);
    expect(c.read(shownSessionProvider), isNull);

    await _switchTo(tester, c, 'A:w2');
    expect(c.read(shownSessionProvider), 's2');
  });

  testWidgets('a terminal-capable node adds the Terminals tab; opening it loads', (tester) async {
    final terms = _CountingTerminals();
    await _shellWithNodes(tester, const [NodeRef('A', 'home')], terms);
    expect(_terminalsTab, findsOneWidget);
    await tester.tap(_terminalsTab);
    await tester.pump();
    expect(terms.loads, 1);
    expect(find.byType(TerminalListScreen), findsOneWidget);
  });

  testWidgets('a page opens over the shell while both tabs have a button', (tester) async {
    await _shellWithNodes(tester, const [NodeRef('A', 'home')], _CountingTerminals());
    // The drawer sits above the scope navigator, so it pushes on the root one.
    Navigator.of(tester.element(find.byType(SessionListScreen)), rootNavigator: true).push(
      MaterialPageRoute<void>(builder: (_) => const Scaffold(body: Text('page'))),
    );
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    expect(find.text('page'), findsOneWidget);
  });

  testWidgets('no Terminals tab without a terminal-capable node', (tester) async {
    final terms = _CountingTerminals();
    await _shellWithNodes(tester, const [NodeRef('A', 'home', terminalSupported: false)], terms);
    expect(_terminalsTab, findsNothing);
    expect(find.text('History'), findsOneWidget);
  });

  testWidgets('a hidden Terminals tab resets to Sessions', (tester) async {
    final terms = _CountingTerminals();
    await _shellWithNodes(tester, const [NodeRef('A', 'home', terminalSupported: false)], terms);
    final c = ProviderScope.containerOf(tester.element(find.byType(HomeShell)));
    c.read(homeTabProvider.notifier).state = HomeTab.terminals;
    await tester.pump();
    await tester.pump();
    expect(c.read(homeTabProvider), HomeTab.sessions);
  });

  testWidgets('phone: Home tabs, drawer opens from the menu button', (tester) async {
    await _shell(tester);
    expect(find.text('Sessions'), findsWidgets);
    expect(find.text('History'), findsOneWidget);
    expect(find.text('Settings'), findsNothing);
    await tester.tap(find.byIcon(Icons.menu));
    await tester.pumpAndSettle();
    expect(find.text('registry'), findsOneWidget);
    expect(find.text('Settings'), findsOneWidget);
  });

  testWidgets('a workspace tap shows its screen and closes the drawer', (tester) async {
    final c = await _shell(tester);
    await tester.tap(find.byIcon(Icons.menu));
    await tester.pumpAndSettle();
    await tester.tap(find.text('registry'));
    await tester.pumpAndSettle();
    expect(c.read(scopeProvider), 'A:w2');
    expect(find.byType(WorkspaceScreen), findsOneWidget);
    expect(find.text('Changes'), findsOneWidget);
    expect(find.text('Files'), findsOneWidget);
  });

  testWidgets('wide: the drawer is a side panel with no menu button', (tester) async {
    await _shell(tester, width: 1000);
    expect(find.text('registry'), findsOneWidget);
    expect(find.byIcon(Icons.menu), findsNothing);
  });

  group('sidebar background', () {
    Color? panelColor(WidgetTester tester) => tester
        .widget<Material>(find
            .descendant(of: find.byType(ProjectDrawer), matching: find.byType(Material))
            .first)
        .color;
    Color? shellBackground(WidgetTester tester) =>
        tester.widget<Scaffold>(find.byType(Scaffold).first).backgroundColor;

    testWidgets('macOS wide: the side panel is tinted and the main area is canvas',
        (tester) async {
      debugDefaultTargetPlatformOverride = TargetPlatform.macOS;
      addTearDown(() => debugDefaultTargetPlatformOverride = null);
      await _shell(tester, width: 1000);
      expect(panelColor(tester), kSidebarTint);
      expect(shellBackground(tester), Colors.transparent);
      final main = find.byKey(const ValueKey('shell-main'));
      expect(tester.widget<ColoredBox>(main).color, AppColors.canvas);
      expect(find.descendant(of: main, matching: find.byType(Navigator)), findsOneWidget);
      expect(find.descendant(of: main, matching: find.byType(ProjectDrawer)), findsNothing);
    });

    testWidgets('macOS narrow: the modal drawer stays opaque over the content',
        (tester) async {
      debugDefaultTargetPlatformOverride = TargetPlatform.macOS;
      addTearDown(() => debugDefaultTargetPlatformOverride = null);
      await _shell(tester);
      await tester.tap(find.byIcon(Icons.menu));
      await tester.pumpAndSettle();
      expect(panelColor(tester), AppColors.card);
      expect(shellBackground(tester), isNull);
      expect(find.byKey(const ValueKey('shell-main')), findsNothing);
    });

    for (final platform in [TargetPlatform.android, TargetPlatform.linux]) {
      testWidgets('$platform: the side panel background is unchanged', (tester) async {
        debugDefaultTargetPlatformOverride = platform;
        addTearDown(() => debugDefaultTargetPlatformOverride = null);
        await _shell(tester, width: 1000);
        expect(panelColor(tester), AppColors.card);
        expect(shellBackground(tester), isNull);
        expect(find.byKey(const ValueKey('shell-main')), findsNothing);
      });
    }

  });

  group('macOS toolbar layout', () {
    ValueNotifier<bool> mac() {
      debugDefaultTargetPlatformOverride = TargetPlatform.macOS;
      addTearDown(() => debugDefaultTargetPlatformOverride = null);
      final fullScreen = ValueNotifier(false);
      addTearDown(fullScreen.dispose);
      return fullScreen;
    }

    Finder barOf(Type screen) => find
        .descendant(of: find.byType(screen), matching: find.byType(AppBar))
        .first;
    final panelHeader = find.descendant(
      of: find.byType(ProjectDrawer),
      matching: find.byType(TitlebarSpacer),
    );
    final menuButton =
        find.ancestor(of: find.byIcon(Icons.menu), matching: find.byType(IconButton));

    testWidgets(
        'wide: the main area\'s toolbar starts at the main area\'s left edge (inset 0), and the side panel has a 52 px header area at the top',
        (tester) async {
      final c = await _shell(tester, width: 1000, fullScreen: mac());
      expect(tester.getTopLeft(barOf(SessionListScreen)).dx, kSidePanelWidth + 1);
      expect(tester.getRect(panelHeader),
          const Rect.fromLTWH(0, 0, kSidePanelWidth, kMacToolbarHeight));

      c.read(scopeProvider.notifier).state = 'A:w2';
      await tester.pumpAndSettle();
      expect(tester.getTopLeft(barOf(WorkspaceScreen)).dx, kSidePanelWidth + 1);
    });

    testWidgets('wide: a session pushed in the nested navigator has a toolbar with inset 0',
        (tester) async {
      final c = await _shell(tester, width: 1000, fullScreen: mac());
      c.read(sessionsProvider.notifier).replaceAll([_session('s1')]);
      c.read(pendingOpenSessionProvider.notifier).state = const PendingOpen('s1');
      await _pumpRoute(tester);
      expect(tester.getTopLeft(barOf(SessionDetailScreen)).dx, kSidePanelWidth + 1);
    });

    testWidgets('narrow: the main toolbar\'s first child starts at x >= 80', (tester) async {
      await _shell(tester, fullScreen: mac());
      expect(tester.getTopLeft(menuButton).dx, greaterThanOrEqualTo(kTrafficLightsInset));
    });

    testWidgets('Settings pushed on the root navigator: its toolbar content starts at x >= 80',
        (tester) async {
      await _shell(tester, width: 1000, fullScreen: mac());
      await tester.tap(find.descendant(
          of: find.byType(ProjectDrawer), matching: find.text('Settings')));
      await tester.pumpAndSettle();
      expect(tester.getTopLeft(barOf(SettingsScreen)).dx,
          greaterThanOrEqualTo(kTrafficLightsInset));
    });

    testWidgets('full screen: the narrow main toolbar starts at x = 0', (tester) async {
      final fullScreen = mac();
      await _shell(tester, fullScreen: fullScreen);
      fullScreen.value = true;
      await tester.pump();
      expect(tester.getTopLeft(barOf(SessionListScreen)).dx, 0);
    });

    for (final wide in [true, false]) {
      testWidgets(
          '${wide ? 'wide' : 'narrow'}: the side panel header area is not covered by interactive widgets in its top 52 px',
          (tester) async {
        await _shell(tester, width: wide ? 1000 : 400, fullScreen: mac());
        if (!wide) {
          await tester.tap(find.byIcon(Icons.menu));
          await tester.pumpAndSettle();
        }
        expect(tester.getRect(panelHeader).height, kMacToolbarHeight);
        expect(tester.getTopLeft(panelHeader).dy, 0);
        final interactive = find.descendant(
          of: find.byType(ProjectDrawer),
          matching: find.byWidgetPredicate((w) =>
              w is InkResponse ||
              w is IconButton ||
              w is EditableText ||
              w is PopupMenuButton),
        );
        expect(interactive, findsWidgets);
        for (final e in interactive.evaluate()) {
          final rect = tester.getRect(find.byElementPredicate((x) => x == e));
          expect(rect.top, greaterThanOrEqualTo(kMacToolbarHeight), reason: '${e.widget}');
        }
      });
    }

    group('tabs', () {
      final tabs = find.byType(CupertinoSlidingSegmentedControl<int>);
      Finder tabsOf(Type screen) =>
          find.descendant(of: find.byType(screen), matching: tabs);
      Finder segment(Type screen, String label) =>
          find.descendant(of: tabsOf(screen), matching: find.text(label));
      int? selectedOf(WidgetTester tester, Type screen) => tester
          .widget<CupertinoSlidingSegmentedControl<int>>(tabsOf(screen))
          .groupValue;
      const home = NodeRef('A', 'home');
      const noTerminals = NodeRef('A', 'home', terminalSupported: false);

      // As the passthrough group's flush, but History keeps a progress
      // indicator spinning, so it pumps past the transitions instead of
      // settling.
      Future<void> flush(WidgetTester tester) async {
        await _pumpRoute(tester);
        for (var i = 0; i < 3; i++) {
          await tester.pump(const Duration(milliseconds: 20));
          await tester.runAsync(() => Future<void>.delayed(Duration.zero));
        }
        await tester.pump();
      }

      Set<Rect> visible(WidgetTester tester) => find
          .byType(MacosToolbarPassthrough)
          .evaluate()
          .map((e) => tester.getRect(find.byElementPredicate((x) => x == e)))
          .toSet();

      Finder tooltipOf(Type screen, String label) =>
          find.descendant(of: tabsOf(screen), matching: find.byTooltip(label));

      for (final wide in [true, false]) {
        for (final terminals in [false, true]) {
          final labels = ['Sessions', 'History', if (terminals) 'Terminals'];
          testWidgets(
              'macOS ${wide ? '1200 px: full labels' : '500 px: icon-only segments with label tooltips'}${terminals ? ' with terminals' : ''}: Home shows a segmented control with ${labels.join(', ')} in the toolbar row, and no NavigationRail and no NavigationBar',
              (tester) async {
            await _shell(tester,
                width: wide ? 1200 : 500,
                fullScreen: mac(),
                nodes: [terminals ? home : noTerminals]);
            await tester.pump();
            expect(tabsOf(SessionListScreen), findsOneWidget);
            for (final label in ['Sessions', 'History', 'Terminals']) {
              final shown = labels.contains(label);
              expect(segment(SessionListScreen, label),
                  wide && shown ? findsOneWidget : findsNothing, reason: label);
              expect(tooltipOf(SessionListScreen, label),
                  !wide && shown ? findsOneWidget : findsNothing, reason: label);
            }
            // A label is never cut short.
            for (final e in find
                .descendant(of: tabsOf(SessionListScreen), matching: find.byType(RichText))
                .evaluate()) {
              final paragraph = e.renderObject! as RenderParagraph;
              expect(paragraph.didExceedMaxLines, isFalse);
              expect(paragraph.size.width,
                  greaterThanOrEqualTo(paragraph.getMaxIntrinsicWidth(double.infinity) - 0.01));
            }
            final rect = tester.getRect(tabsOf(SessionListScreen));
            expect(rect.height, 28);
            expect(rect.top, greaterThanOrEqualTo(0));
            expect(rect.bottom, lessThanOrEqualTo(kMacToolbarHeight));
            expect(find.byType(NavigationRail), findsNothing);
            expect(find.byType(NavigationBar), findsNothing);
          });
        }
      }

      testWidgets(
          'macOS: tapping History selects the History tab; ⌘2 also selects it and the control follows',
          (tester) async {
        final c = await _shell(tester, width: 1000, fullScreen: mac(), nodes: [home]);
        await tester.pump();
        expect(selectedOf(tester, SessionListScreen), 0);
        await tester.tap(segment(SessionListScreen, 'History'));
        await _pumpRoute(tester);
        expect(c.read(homeTabProvider), HomeTab.history);
        expect(find.byType(HistoryScreen), findsOneWidget);
        expect(selectedOf(tester, HistoryScreen), 1);

        await tester.tap(segment(HistoryScreen, 'Sessions'));
        await _pumpRoute(tester);
        expect(c.read(homeTabProvider), HomeTab.sessions);
        expect(selectedOf(tester, SessionListScreen), 0);

        await tester.sendKeyDownEvent(LogicalKeyboardKey.metaLeft);
        await tester.sendKeyDownEvent(LogicalKeyboardKey.digit2);
        await tester.sendKeyUpEvent(LogicalKeyboardKey.digit2);
        await tester.sendKeyUpEvent(LogicalKeyboardKey.metaLeft);
        await _pumpRoute(tester);
        expect(c.read(homeTabProvider), HomeTab.history);
        expect(selectedOf(tester, HistoryScreen), 1);
      });

      testWidgets(
          'macOS: the Sessions toolbar omits its plain "Sessions" title; the Workspace toolbar keeps its workspace title on the left and shows the four tabs centered',
          (tester) async {
        // Wide enough for the four labels in the test font.
        final c = await _shell(tester, width: 1600, fullScreen: mac(), nodes: [home]);
        await tester.pump();
        expect(
            find.descendant(
                of: find.byType(SessionListScreen), matching: find.text('Sessions')),
            findsOneWidget);
        expect(segment(SessionListScreen, 'Sessions'), findsOneWidget);

        c.read(scopeProvider.notifier).state = 'A:w2';
        await tester.pumpAndSettle();
        final control = tabsOf(WorkspaceScreen);
        expect(control, findsOneWidget);
        for (final label in ['Sessions', 'Changes', 'Files', 'Terminals']) {
          expect(segment(WorkspaceScreen, label), findsOneWidget, reason: label);
        }
        final title = find.descendant(
            of: barOf(WorkspaceScreen), matching: find.text('registry'));
        expect(title, findsOneWidget);
        final rect = tester.getRect(control);
        expect(tester.getRect(title).right, lessThanOrEqualTo(rect.left));
        expect(rect.center.dx,
            moreOrLessEquals(tester.getRect(barOf(WorkspaceScreen)).center.dx));
      });

      testWidgets('macOS: a session pushed in the nested navigator shows no tabs',
          (tester) async {
        final c = await _shell(tester, width: 1000, fullScreen: mac(), nodes: [home]);
        c.read(sessionsProvider.notifier).replaceAll([_session('s1')]);
        c.read(pendingOpenSessionProvider.notifier).state = const PendingOpen('s1');
        await _pumpRoute(tester);
        expect(find.byType(SessionDetailScreen), findsOneWidget);
        expect(tabsOf(SessionDetailScreen), findsNothing);
        expect(tabs, findsNothing);
      });

      testWidgets(
          'macOS: the control is inside the passthrough rects; after switching tabs and after the Terminals tab appears, the sent rects match the control',
          (tester) async {
        _passthroughRects.clear();
        final c = await _shell(tester,
            width: 1000, fullScreen: mac(), nodes: [noTerminals]);
        await flush(tester);
        Rect rectOf(Type screen) => tester.getRect(tabsOf(screen));
        expect(_passthroughRects.values, contains(rectOf(SessionListScreen)));
        expect(_passthroughRects.values.toSet(), visible(tester));

        await tester.tap(segment(SessionListScreen, 'History'));
        await flush(tester);
        expect(_passthroughRects.values, contains(rectOf(HistoryScreen)));
        expect(_passthroughRects.values.toSet(), visible(tester));

        c.read(_nodesProvider.notifier).state = const [home];
        await flush(tester);
        expect(segment(HistoryScreen, 'Terminals'), findsOneWidget);
        expect(_passthroughRects.values, contains(rectOf(HistoryScreen)));
        expect(_passthroughRects.values.toSet(), visible(tester));
      });

      testWidgets(
          'macOS narrow (500 px): the tabs are icon-only segments with label tooltips, inside the bar and clear of its buttons',
          (tester) async {
        await _shell(tester, width: 500, fullScreen: mac(), nodes: [home]);
        await tester.pump();
        for (final label in ['Sessions', 'History', 'Terminals']) {
          expect(tooltipOf(SessionListScreen, label), findsOneWidget);
        }
        expect(
            find.descendant(of: tabsOf(SessionListScreen), matching: find.byType(Text)),
            findsNothing);
        final rect = tester.getRect(tabsOf(SessionListScreen));
        final bar = tester.getRect(barOf(SessionListScreen));
        expect(rect.left, greaterThanOrEqualTo(bar.left));
        expect(rect.right, lessThanOrEqualTo(bar.right));
        expect(rect.left, greaterThanOrEqualTo(tester.getRect(menuButton).right));
        final buttons = find.descendant(
            of: barOf(SessionListScreen), matching: find.byType(IconButton));
        for (final e in buttons.evaluate()) {
          final button = tester.getRect(find.byElementPredicate((x) => x == e));
          expect(button.overlaps(rect), isFalse, reason: '${e.widget}');
        }
        expect(tester.takeException(), isNull);
      });

      testWidgets(
          'macOS 700 px with the Sessions search open: the field keeps 200 px (or its intrinsic width), the tabs go icon-only, and the native rects follow',
          (tester) async {
        _passthroughRects.clear();
        final c = await _shell(tester, width: 700, fullScreen: mac(), nodes: [home]);
        await flush(tester);
        c.read(sessionSearchProvider.notifier).open();
        await flush(tester);
        final field = find.descendant(
            of: barOf(SessionListScreen), matching: find.byType(TextField));
        expect(field, findsOneWidget);
        final intrinsic =
            tester.renderObject<RenderBox>(field).getMaxIntrinsicWidth(double.infinity);
        expect(tester.getSize(field).width,
            greaterThanOrEqualTo(math.min(200, intrinsic)));
        expect(tooltipOf(SessionListScreen, 'Sessions'), findsOneWidget);
        expect(segment(SessionListScreen, 'Sessions'), findsNothing);
        final rect = tester.getRect(tabsOf(SessionListScreen));
        expect(rect.left, greaterThanOrEqualTo(tester.getRect(field).right));
        expect(_passthroughRects.values, contains(rect));
        expect(_passthroughRects.values.toSet(), visible(tester));
      });
    });

    group('passthrough', () {
      setUp(_passthroughRects.clear);

      // Lets debounced passthrough updates (10 ms) and their post-frame
      // resend run.
      Future<void> flush(WidgetTester tester) async {
        await tester.pumpAndSettle();
        for (var i = 0; i < 3; i++) {
          await tester.pump(const Duration(milliseconds: 20));
          await tester.runAsync(() => Future<void>.delayed(Duration.zero));
        }
        await tester.pump();
      }

      Set<Rect> visible(WidgetTester tester) => find
          .byType(MacosToolbarPassthrough)
          .evaluate()
          .map((e) => tester.getRect(find.byElementPredicate((x) => x == e)))
          .toSet();

      testWidgets('narrow: after the drawer opens and after it closes, the native rects are current',
          (tester) async {
        await _shell(tester, fullScreen: mac());
        await flush(tester);
        expect(_passthroughRects.values.toSet(), visible(tester));
        await tester.tap(find.byIcon(Icons.menu));
        await flush(tester);
        expect(_passthroughRects.values.toSet(), visible(tester));
        await tester.tapAt(const Offset(390, 500));
        await flush(tester);
        expect(find.byType(ProjectDrawer), findsNothing);
        expect(_passthroughRects.values.toSet(), visible(tester));
      });

      testWidgets('narrow: the drawer opening and closing under a dialog leaves the rects current',
          (tester) async {
        await _shell(tester, fullScreen: mac());
        await flush(tester);
        showDialog<void>(
          context: tester.element(find.byType(HomeShell)),
          builder: (_) => const Text('dialog'),
        );
        await flush(tester);
        final drawer = tester.state<ModalShellDrawerState>(find.byType(ModalShellDrawer));
        drawer.open();
        await flush(tester);
        final width = tester.getSize(find.byType(ProjectDrawer)).width;
        expect(_passthroughRects.values, contains(Rect.fromLTWH(
            kTrafficLightsInset, 0, width - kTrafficLightsInset, kMacToolbarHeight)));
        expect(_passthroughRects.values.toSet(), visible(tester));
        drawer.close();
        await flush(tester);
        expect(_passthroughRects.values.toSet(), visible(tester));
      });

      testWidgets('after full screen changes the inset, the native rects are current',
          (tester) async {
        final fullScreen = mac();
        await _shell(tester, fullScreen: fullScreen);
        await flush(tester);
        fullScreen.value = true;
        await flush(tester);
        expect(_passthroughRects.values.toSet(), visible(tester));
        fullScreen.value = false;
        await flush(tester);
        expect(_passthroughRects.values.toSet(), visible(tester));
      });

      testWidgets('after a layout switch, the native rects are current', (tester) async {
        await _shell(tester, fullScreen: mac());
        await flush(tester);
        tester.view.physicalSize = const Size(1000, 900);
        await flush(tester);
        expect(_passthroughRects.values.toSet(), visible(tester));
        tester.view.physicalSize = const Size(400, 900);
        await flush(tester);
        expect(_passthroughRects.values.toSet(), visible(tester));
      });

      testWidgets('wide: under a dialog, the side panel header passes through after the traffic lights',
          (tester) async {
        await _shell(tester, width: 1000, fullScreen: mac());
        await flush(tester);
        showDialog<void>(
          context: tester.element(find.byType(HomeShell)),
          builder: (_) => const Text('dialog'),
        );
        await flush(tester);
        expect(_passthroughRects.values, contains(const Rect.fromLTWH(
            kTrafficLightsInset, 0, kSidePanelWidth - kTrafficLightsInset, kMacToolbarHeight)));
        // The main toolbar is after the side panel, at inset 0.
        const left = kSidePanelWidth + 1;
        expect(_passthroughRects.values,
            contains(const Rect.fromLTWH(left, 0, 1000 - left, kMacToolbarHeight)));
      });

      testWidgets('narrow: under a dialog, the open drawer\'s header passes through after the traffic lights',
          (tester) async {
        await _shell(tester, fullScreen: mac());
        await tester.tap(find.byIcon(Icons.menu));
        await flush(tester);
        showDialog<void>(
          context: tester.element(find.byType(HomeShell)),
          builder: (_) => const Text('dialog'),
        );
        await flush(tester);
        final drawer = tester.getSize(find.byType(ProjectDrawer)).width;
        expect(_passthroughRects.values, contains(Rect.fromLTWH(
            kTrafficLightsInset, 0, drawer - kTrafficLightsInset, kMacToolbarHeight)));
      });
    });
  });

  group('macOS menu bar', () {
    void mac() {
      debugDefaultTargetPlatformOverride = TargetPlatform.macOS;
      addTearDown(() => debugDefaultTargetPlatformOverride = null);
    }

    testWidgets('with Settings on top, Back pops it and the shell items do nothing',
        (tester) async {
      mac();
      _mockMenuChannel(tester);
      final c = await _shell(tester, width: 1000, menuBar: true);
      c.read(scopeProvider.notifier).state = 'A:w2';
      await tester.pumpAndSettle();

      await _select(tester, 'Settings…');
      expect(find.byType(SettingsScreen), findsOneWidget);
      await _select(tester, 'Settings…');
      expect(find.byType(SettingsScreen, skipOffstage: false), findsOneWidget);
      await _select(tester, 'History');
      await _select(tester, 'New Session');
      expect(find.byType(SpawnDialog, skipOffstage: false), findsNothing);
      await _select(tester, 'Back');
      expect(find.byType(SettingsScreen, skipOffstage: false), findsNothing);
      expect(c.read(scopeProvider), 'A:w2');

      c.read(scopeProvider.notifier).state = null;
      await tester.pumpAndSettle();
      expect(c.read(homeTabProvider), HomeTab.sessions);
      await _select(tester, 'History');
      expect(c.read(homeTabProvider), HomeTab.history);
    });

    testWidgets('the menu bar outlives the shell; its app items follow it',
        (tester) async {
      mac();
      _mockMenuChannel(tester);
      tester.view.physicalSize = const Size(1000, 900);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      final c = ProviderContainer(overrides: [
        gatewayProvider.overrideWithValue(null),
      ]);
      addTearDown(c.dispose);
      await c
          .read(projectsProvider.notifier)
          .load(FakeGatewayClient((m, p) async => _tree()));
      Future<void> pump(Widget home) async {
        await tester.pumpWidget(UncontrolledProviderScope(
          container: c,
          child: MaterialApp(
            builder: (context, child) => MacMenuBar(child: child!),
            home: home,
          ),
        ));
        await tester.pump();
      }

      await pump(const Text('signed out'));
      expect(_menuItem(tester, 'Back').onSelected, isNull);
      expect(_menuItem(tester, 'Settings…').onSelected, isNull);

      await pump(const HomeShell());
      await _select(tester, 'History');
      expect(c.read(homeTabProvider), HomeTab.history);

      await pump(const Text('signed out'));
      expect(_menuItem(tester, 'Back').onSelected, isNull);
    });
  });

  testWidgets('Settings opens from the drawer footer', (tester) async {
    await _shell(tester, width: 1000);
    await tester.tap(find.text('Settings'));
    await tester.pumpAndSettle();
    expect(find.text('Disconnect'), findsOneWidget);
  });

  testWidgets('back: files up, then Home, then History to Sessions', (tester) async {
    final c = await _shell(tester);
    c.read(scopeProvider.notifier).state = 'A:w2';
    c.read(workspaceTabProvider('A:w2').notifier).state = WorkspaceTab.files;
    c.read(filesPathProvider('A:w2').notifier).state = 'lib/ui';
    await tester.pump();

    await _back(tester);
    expect(c.read(filesPathProvider('A:w2')), 'lib');
    await _back(tester);
    expect(c.read(filesPathProvider('A:w2')), '');
    await _back(tester);
    expect(c.read(scopeProvider), isNull);

    c.read(homeTabProvider.notifier).state = HomeTab.history;
    await tester.pump();
    await _back(tester);
    expect(c.read(homeTabProvider), HomeTab.sessions);
  });

  testWidgets('a removed or gone workspace falls back to Home', (tester) async {
    final c = await _shell(tester);
    c.read(scopeProvider.notifier).state = 'A:w2';
    await tester.pump();
    await c
        .read(projectsProvider.notifier)
        .load(FakeGatewayClient((m, p) async => _tree(gone: true)));
    await tester.pump();
    expect(c.read(scopeProvider), isNull);
    expect(find.text('registry is no longer available'), findsOneWidget);
  });

  testWidgets('error state does not drop the scope', (tester) async {
    final c = await _shell(tester);
    c.read(scopeProvider.notifier).state = 'A:w2';
    await tester.pump();
    await c
        .read(projectsProvider.notifier)
        .load(FakeGatewayClient((m, p) async => throw StateError('blip')));
    await tester.pump();
    expect(c.read(scopeProvider), 'A:w2');
  });

  testWidgets('a partial failure does not drop the scope', (tester) async {
    final c = await _shell(tester);
    c.read(scopeProvider.notifier).state = 'A:w2';
    await tester.pump();
    await c.read(projectsProvider.notifier).load(FakeGatewayClient((m, p) async => {
          'projects': [
            {'id': 'B:p9', 'name': 'infra', 'node_id': 'B', 'workspaces': []},
          ],
          'failed_nodes': ['A'],
        }));
    await tester.pump();
    expect(c.read(scopeProvider), 'A:w2');
    expect(find.textContaining('no longer available'), findsNothing);
  });

  testWidgets('deep-links to a session pending before mount', (tester) async {
    final container = ProviderContainer(
      overrides: [gatewayProvider.overrideWithValue(null)],
    );
    addTearDown(container.dispose);
    // The tap arrives (and sets the pending session) before HomeShell mounts,
    // and the session list is already populated.
    container.read(sessionsProvider.notifier).replaceAll([_session('s1')]);
    container.read(pendingOpenSessionProvider.notifier).state =
        const PendingOpen('s1');

    await tester.pumpWidget(UncontrolledProviderScope(
      container: container,
      child: const MaterialApp(home: HomeShell()),
    ));
    // SessionDetailScreen shows a perpetual spinner without a connection, so
    // settle is impossible; pump past the route transition instead.
    await tester.pump();
    await tester.pump(const Duration(seconds: 1));

    expect(_detailInShell, findsOneWidget);
    expect(container.read(pendingOpenSessionProvider), isNull);
  });

  testWidgets('opens a pending session once the list is fetched', (tester) async {
    final container = ProviderContainer(
      overrides: [gatewayProvider.overrideWithValue(null)],
    );
    addTearDown(container.dispose);
    // Cold start: the tap sets a pending session, but the list has not arrived.
    container.read(pendingOpenSessionProvider.notifier).state =
        const PendingOpen('s1');

    await tester.pumpWidget(UncontrolledProviderScope(
      container: container,
      child: const MaterialApp(home: HomeShell()),
    ));
    await tester.pump();

    // Nothing to open yet; the request must be kept, not discarded.
    expect(find.byType(SessionDetailScreen), findsNothing);
    expect(container.read(pendingOpenSessionProvider)?.sessionId, 's1');

    // The session list is fetched; the pending session opens now.
    container.read(sessionsProvider.notifier).replaceAll([_session('s1')]);
    await tester.pump();
    await tester.pump(const Duration(seconds: 1));

    expect(_detailInShell, findsOneWidget);
    expect(container.read(pendingOpenSessionProvider), isNull);
  });

  testWidgets('phone: a pushed session has no menu button but keeps the drawer reachable', (tester) async {
    final c = await _shell(tester);
    await _openSessionIn(tester, c, 'A:w1');
    expect(_detailInShell, findsOneWidget);
    expect(find.byType(BackButton), findsOneWidget);
    expect(find.byIcon(Icons.menu), findsNothing);

    await _holdOpenDrawer(tester);
    await _pumpRoute(tester);
    expect(find.text('Settings'), findsOneWidget);
    await tester.tap(find.text('registry'));
    await _pumpRoute(tester);
    expect(c.read(scopeProvider), 'A:w2');
    expect(find.text('Settings'), findsNothing);
    expect(find.byType(WorkspaceScreen), findsOneWidget);
  });

  testWidgets('a session opened from Home Sessions stays in the shell', (tester) async {
    final c = await _shell(tester);
    c.read(sessionsProvider.notifier).replaceAll([_session('s1')]);
    await tester.pump();
    await tester.tap(find.byType(SessionCard));
    await _pumpRoute(tester);
    expect(_detailInShell, findsOneWidget);
  });

  testWidgets('wide: a pushed session keeps the side panel', (tester) async {
    final c = await _shell(tester, width: 1000);
    await _openSessionIn(tester, c, 'A:w2');
    expect(_detailInShell, findsOneWidget);
    expect(find.text('registry'), findsWidgets);
    expect(find.text('Settings'), findsOneWidget);
    expect(find.byIcon(Icons.menu), findsNothing);
  });

  testWidgets('a session opened from Home is remembered by its workspace', (tester) async {
    final c = await _shell(tester);
    c.read(sessionsProvider.notifier).replaceAll([
      _session('s1', workspaceId: 'A:w1'),
      _session('s2', workspaceId: 'A:w2'),
    ]);
    await tester.pump();
    await tester.tap(_card('s2'));
    await _pumpRoute(tester);
    expect(_shownSession(tester), 's2');

    await _switchTo(tester, c, 'A:w1');
    expect(find.byType(WorkspaceScreen), findsOneWidget);
    expect(_detailInShell, findsNothing);

    await _switchTo(tester, c, null);
    expect(_detailInShell, findsNothing);
    expect(find.byType(SessionListScreen), findsOneWidget);

    await _switchTo(tester, c, 'A:w2');
    expect(_shownSession(tester), 's2');
  });

  testWidgets('a session with no workspace is remembered by Home', (tester) async {
    final c = await _shell(tester);
    c.read(sessionsProvider.notifier).replaceAll([_session('s0')]);
    await tester.pump();
    await tester.tap(_card('s0'));
    await _pumpRoute(tester);

    await _switchTo(tester, c, 'A:w1');
    expect(_detailInShell, findsNothing);
    await _switchTo(tester, c, null);
    expect(_shownSession(tester), 's0');
  });

  testWidgets('a workspace remembers only the session, not deeper screens', (tester) async {
    final c = await _shell(tester);
    await _openSessionIn(tester, c, 'A:w2');
    Navigator.of(tester.element(find.byType(SessionDetailScreen))).push(
      MaterialPageRoute(
        builder: (_) => LiveScreenScreen(session: _session('s2', workspaceId: 'A:w2')),
      ),
    );
    await _pumpRoute(tester);
    expect(find.byType(LiveScreenScreen), findsOneWidget);

    await _switchTo(tester, c, 'A:w1');
    await _switchTo(tester, c, 'A:w2');
    expect(find.byType(LiveScreenScreen), findsNothing);
    expect(_shownSession(tester), 's2');

    await tester.binding.handlePopRoute();
    await _pumpRoute(tester);
    expect(_detailInShell, findsNothing);
    expect(find.byType(WorkspaceScreen), findsOneWidget);
  });

  testWidgets('Home remembers History, not a past-session transcript', (tester) async {
    final c = await _shell(tester);
    c.read(homeTabProvider.notifier).state = HomeTab.history;
    await tester.pump();
    Navigator.of(tester.element(find.byType(HistoryScreen))).push(
      MaterialPageRoute(
        builder: (_) => const HistoryTranscriptScreen(
          session: HistorySession(
            sessionId: 'h1',
            transcriptPath: '/t',
            lastActivity: '',
            tokens: 0,
            turnCount: 0,
            durationMs: 0,
            resumable: false,
          ),
        ),
      ),
    );
    await _pumpRoute(tester);
    expect(find.byType(HistoryTranscriptScreen), findsOneWidget);

    await _switchTo(tester, c, 'A:w1');
    await _switchTo(tester, c, null);
    expect(find.byType(HistoryTranscriptScreen), findsNothing);
    expect(c.read(homeTabProvider), HomeTab.history);
    expect(find.byType(HistoryScreen), findsOneWidget);
  });

  testWidgets('back out of a session forgets it', (tester) async {
    final c = await _shell(tester);
    c.read(sessionsProvider.notifier).replaceAll([
      _session('s1', workspaceId: 'A:w1'),
      _session('s2', workspaceId: 'A:w2'),
    ]);
    await tester.pump();
    await tester.tap(_card('s2'));
    await _pumpRoute(tester);
    await _switchTo(tester, c, 'A:w1');
    await _switchTo(tester, c, null);
    await tester.tap(_card('s2'));
    await _pumpRoute(tester);
    await tester.binding.handlePopRoute();
    await _pumpRoute(tester);
    expect(_detailInShell, findsNothing);
    expect(c.read(scopeProvider), 'A:w2');

    await _switchTo(tester, c, null);
    expect(_detailInShell, findsNothing);
    await _switchTo(tester, c, 'A:w2');
    expect(_detailInShell, findsNothing);
    expect(find.byType(WorkspaceScreen), findsOneWidget);
  });

  testWidgets('back from a session opened in Home shows its workspace', (tester) async {
    final c = await _shell(tester);
    c.read(sessionsProvider.notifier).replaceAll([
      _session('s1', workspaceId: 'A:w1'),
      _session('s2', workspaceId: 'A:w2'),
    ]);
    await tester.pump();
    await tester.tap(_card('s1'));
    await _pumpRoute(tester);
    await tester.tap(find.byType(BackButton));
    await _pumpRoute(tester);
    expect(c.read(scopeProvider), 'A:w1');
    expect(find.byType(WorkspaceScreen), findsOneWidget);
    expect(_detailInShell, findsNothing);
  });

  testWidgets('back from a drawer session shows its workspace, not the one it opened over', (tester) async {
    final c = await _shell(tester);
    await _openSessionIn(tester, c, 'A:w1');
    await _switchTo(tester, c, 'A:w2');
    expect(_detailInShell, findsNothing);
    // The drawer opens a session in the scope on screen.
    c.read(pendingOpenSessionProvider.notifier).state = const PendingOpen('s1');
    await _pumpRoute(tester);
    expect(_shownSession(tester), 's1');

    await tester.binding.handlePopRoute();
    await _pumpRoute(tester);
    expect(c.read(scopeProvider), 'A:w1');
    // s1 was remembered by A:w1; back from it leaves the bare workspace.
    expect(_detailInShell, findsNothing);
    expect(find.byType(WorkspaceScreen), findsOneWidget);
  });

  testWidgets('back from a session with no workspace stays in Home', (tester) async {
    final c = await _shell(tester);
    c.read(sessionsProvider.notifier).replaceAll([_session('s0')]);
    await tester.pump();
    await tester.tap(_card('s0'));
    await _pumpRoute(tester);
    await tester.binding.handlePopRoute();
    await _pumpRoute(tester);
    expect(c.read(scopeProvider), isNull);
    expect(find.byType(SessionListScreen), findsOneWidget);
    expect(_detailInShell, findsNothing);
  });

  testWidgets('Workspace leaves the session for its workspace', (tester) async {
    final c = await _shell(tester);
    c.read(sessionsProvider.notifier).replaceAll([
      _session('s1', workspaceId: 'A:w1'),
      _session('s2', workspaceId: 'A:w2'),
    ]);
    await tester.pump();
    await tester.tap(_card('s2'));
    await _pumpRoute(tester);
    c.read(workspaceTabProvider('A:w2').notifier).state = WorkspaceTab.files;

    await tester.tap(find.byType(PopupMenuButton<String>));
    await _pumpRoute(tester);
    await tester.tap(find.text('Workspace'));
    await _pumpRoute(tester);
    expect(c.read(scopeProvider), 'A:w2');
    // The workspace keeps its own tab.
    expect(c.read(workspaceTabProvider('A:w2')), WorkspaceTab.files);
    expect(find.byType(WorkspaceScreen), findsOneWidget);
    expect(_detailInShell, findsNothing);

    await _switchTo(tester, c, null);
    expect(_detailInShell, findsNothing);
  });

  testWidgets('Workspace under the session\'s own workspace closes the session', (tester) async {
    final c = await _shell(tester);
    await _openSessionIn(tester, c, 'A:w2');
    await tester.tap(find.byType(PopupMenuButton<String>));
    await _pumpRoute(tester);
    await tester.tap(find.text('Workspace'));
    await _pumpRoute(tester);
    expect(c.read(scopeProvider), 'A:w2');
    expect(find.byType(WorkspaceScreen), findsOneWidget);
    expect(_detailInShell, findsNothing);
  });

  testWidgets('a session opened from memory stays remembered', (tester) async {
    final c = await _shell(tester);
    await _openSessionIn(tester, c, 'A:w2');
    await _switchTo(tester, c, 'A:w1');
    await _switchTo(tester, c, 'A:w2');
    expect(_shownSession(tester), 's2');

    await _switchTo(tester, c, 'A:w1');
    await _switchTo(tester, c, 'A:w2');
    expect(_shownSession(tester), 's2');
  });

  testWidgets('the topmost of stacked session details wins', (tester) async {
    final c = await _shell(tester);
    c.read(sessionsProvider.notifier).replaceAll([
      _session('s1', workspaceId: 'A:w1'),
      _session('s3', workspaceId: 'A:w1'),
    ]);
    await tester.pump();
    await tester.tap(_card('s1'));
    await _pumpRoute(tester);
    Navigator.of(tester.element(_detailInShell))
        .push(sessionDetailRoute(_session('s3', workspaceId: 'A:w1')));
    await _pumpRoute(tester);

    await _switchTo(tester, c, 'A:w1');
    expect(_shownSession(tester), 's3');
  });

  testWidgets('a notification tap replaces the session shown', (tester) async {
    final c = await _shell(tester);
    await _openSessionIn(tester, c, 'A:w2');
    c.read(pendingOpenSessionProvider.notifier).state = const PendingOpen('s1');
    await _pumpRoute(tester);
    expect(_shownSession(tester), 's1');

    await _switchTo(tester, c, 'A:w1');
    expect(_shownSession(tester), 's1');
    await _switchTo(tester, c, 'A:w2');
    expect(_shownSession(tester), 's2');
  });

  testWidgets('back from a tapped session goes to its workspace, not the replaced one', (tester) async {
    final c = await _shell(tester);
    await _openSessionIn(tester, c, 'A:w2');
    c.read(pendingOpenSessionProvider.notifier).state = const PendingOpen('s1');
    await _pumpRoute(tester);
    await tester.binding.handlePopRoute();
    await _pumpRoute(tester);
    expect(c.read(scopeProvider), 'A:w1');
    expect(_detailInShell, findsNothing);
    expect(find.byType(WorkspaceScreen), findsOneWidget);
  });

  testWidgets('a resume replaces the session Home remembers', (tester) async {
    final c = await _shell(tester);
    c.read(sessionsProvider.notifier).replaceAll([_session('s0')]);
    await tester.pump();
    await tester.tap(_card('s0'));
    await _pumpRoute(tester);
    await _switchTo(tester, c, 'A:w1');

    c.read(sessionsProvider.notifier).replaceAll([_session('s0'), _session('s9')]);
    c.read(pendingOpenSessionProvider.notifier).state =
        const PendingOpen('s9', inHome: true);
    // Home's navigator is built a frame before the replacement starts.
    await tester.pump();
    await _pumpRoute(tester);
    expect(_shownSession(tester), 's9');
    await tester.binding.handlePopRoute();
    await _pumpRoute(tester);
    expect(_detailInShell, findsNothing);
    expect(find.byType(SessionListScreen), findsOneWidget);
  });

  testWidgets('a resume in flight opens in Home after a scope switch', (tester) async {
    final repo = _GatedSessionRepository();
    final c = await _shell(tester, repo: repo);
    Navigator.of(tester.element(find.text('History'))).push(MaterialPageRoute(
      builder: (_) => Consumer(
        builder: (context, ref, _) => Scaffold(
          body: ElevatedButton(
            onPressed: () => resumeSession(
              context,
              ref,
              agent: 'claude',
              agentSessionId: 'x',
              cwd: '/src',
            ),
            child: const Text('resume'),
          ),
        ),
      ),
    ));
    await tester.pumpAndSettle();
    await tester.tap(find.text('resume'));
    await tester.pump();

    await _switchTo(tester, c, 'A:w2');
    repo.gate.complete();
    await tester.pumpAndSettle();
    expect(c.read(scopeProvider), isNull);
    expect(find.text('Resuming session…'), findsOneWidget);

    c.read(sessionsProvider.notifier).replaceAll([_session('s9')]);
    await _pumpRoute(tester);
    expect(_shownSession(tester), 's9');
  });

  testWidgets('a session that left the registry is forgotten', (tester) async {
    final c = await _shell(tester);
    await _openSessionIn(tester, c, 'A:w2');
    await _switchTo(tester, c, 'A:w1');
    c.read(sessionsProvider.notifier).replaceAll([_session('s1', workspaceId: 'A:w1')]);
    await tester.pump();
    c.read(sessionsProvider.notifier).replaceAll([
      _session('s1', workspaceId: 'A:w1'),
      _session('s2', workspaceId: 'A:w2'),
    ]);
    await _switchTo(tester, c, 'A:w2');
    expect(_detailInShell, findsNothing);
    expect(find.byType(WorkspaceScreen), findsOneWidget);
  });

  testWidgets('resume from a workspace opens in Home and keeps the workspace session', (tester) async {
    final c = await _shell(tester);
    await _openSessionIn(tester, c, 'A:w2');
    c.read(sessionsProvider.notifier).replaceAll([
      _session('s1', workspaceId: 'A:w1'),
      _session('s2', workspaceId: 'A:w2'),
      _session('s9'),
    ]);
    c.read(pendingOpenSessionProvider.notifier).state =
        const PendingOpen('s9', inHome: true);
    await _pumpRoute(tester);
    expect(c.read(scopeProvider), isNull);
    expect(_shownSession(tester), 's9');
    expect(c.read(pendingOpenSessionProvider), isNull);

    await _switchTo(tester, c, 'A:w2');
    expect(_shownSession(tester), 's2');
  });

  testWidgets('back pops the pushed screen, then follows the shell order', (tester) async {
    final c = await _shell(tester);
    await _openSessionIn(tester, c, 'A:w2');

    await _back(tester);
    expect(find.byType(SessionDetailScreen), findsNothing);
    expect(find.byType(WorkspaceScreen), findsOneWidget);
    expect(c.read(scopeProvider), 'A:w2');

    await _back(tester);
    expect(c.read(scopeProvider), isNull);
  });

  testWidgets('back closes an open drawer before popping', (tester) async {
    final c = await _shell(tester);
    await _openSessionIn(tester, c, 'A:w2');
    await _holdOpenDrawer(tester);
    await _pumpRoute(tester);
    expect(find.text('Settings'), findsOneWidget);

    await tester.binding.handlePopRoute();
    await _pumpRoute(tester);
    expect(find.text('Settings'), findsNothing);
    expect(find.byType(SessionDetailScreen), findsOneWidget);
    expect(c.read(scopeProvider), 'A:w2');
  });

  testWidgets('back at Home closes an open drawer and stays in the app', (tester) async {
    await _shell(tester);
    await tester.tap(find.byIcon(Icons.menu));
    await tester.pumpAndSettle();
    expect(find.text('Settings'), findsOneWidget);

    expect(await tester.binding.handlePopRoute(), isTrue);
    await tester.pumpAndSettle();
    expect(find.byType(Drawer), findsNothing);
    expect(find.byType(HomeShell), findsOneWidget);
  });

  double drawerRight(WidgetTester tester) =>
      tester.getTopRight(find.byType(Drawer)).dx;

  // Android gesture navigation reports a left system gesture inset.
  Future<void> setGestureNav(WidgetTester tester) async {
    tester.view.systemGestureInsets = const FakeViewPadding(left: 30);
    await tester.pump();
  }

  Future<void> swipe(WidgetTester tester) async {
    await tester.dragFrom(const Offset(5, 300), const Offset(300, 0));
    await tester.pumpAndSettle();
  }

  Future<void> tapThroughEdge(WidgetTester tester, ProviderContainer c) async {
    c.read(sessionsProvider.notifier).replaceAll([_session('s1')]);
    await tester.pump();
    final card = tester.getRect(find.byType(SessionCard));
    expect(card.left + 2, lessThan(20));
    await tester.tapAt(Offset(card.left + 2, card.center.dy));
    await _pumpRoute(tester);
    expect(_detailInShell, findsOneWidget);
  }

  group('edge hold', () {
    Future<TestGesture> hold(WidgetTester tester) async {
      final gesture = await tester.startGesture(const Offset(5, 300));
      await tester.pump(const Duration(milliseconds: 350));
      await tester.pumpAndSettle();
      return gesture;
    }

    testWidgets('a quick edge swipe leaves the drawer closed', (tester) async {
      await _shell(tester);
      await setGestureNav(tester);
      await swipe(tester);
      expect(find.byType(Drawer), findsNothing);
    });

    testWidgets('a hold peeks the inset, a pull drags it open', (tester) async {
      await _shell(tester);
      await setGestureNav(tester);
      final gesture = await hold(tester);
      expect(drawerRight(tester), 30);

      await gesture.moveBy(const Offset(100, 0));
      await tester.pump();
      expect(drawerRight(tester), 130);
      await gesture.moveBy(const Offset(150, 0));
      await gesture.up();
      await tester.pumpAndSettle();
      expect(find.text('Settings'), findsOneWidget);
      expect(tester.getTopLeft(find.byType(Drawer)).dx, 0);
    });

    testWidgets(
      'iOS: a hold peeks the drawer',
      (tester) async {
        await _shell(tester);
        await hold(tester);
        expect(drawerRight(tester), 20);
      },
      variant: TargetPlatformVariant.only(TargetPlatform.iOS),
    );

    testWidgets(
      'iOS: a quick edge swipe on a pushed screen goes back',
      (tester) async {
        final c = await _shell(tester);
        await _openSessionIn(tester, c, 'A:w1');
        await tester.dragFrom(const Offset(5, 300), const Offset(300, 0));
        await _pumpRoute(tester);
        expect(_detailInShell, findsNothing);
        expect(find.byType(Drawer), findsNothing);
      },
      variant: TargetPlatformVariant.only(TargetPlatform.iOS),
    );

    testWidgets('releasing a peek closes the drawer', (tester) async {
      await _shell(tester);
      await setGestureNav(tester);
      final gesture = await hold(tester);
      expect(find.byType(Drawer), findsOneWidget);
      await gesture.up();
      await tester.pumpAndSettle();
      expect(find.byType(Drawer), findsNothing);
    });

    testWidgets('a tap at the edge still reaches the content', (tester) async {
      final c = await _shell(tester);
      await setGestureNav(tester);
      await tapThroughEdge(tester, c);
    });
  });

  group('edge swipe', () {
    // Past the drag slop, then 100 more.
    Future<TestGesture> pull(WidgetTester tester) async {
      final gesture = await tester.startGesture(const Offset(5, 300));
      await gesture.moveBy(const Offset(50, 0));
      await gesture.moveBy(const Offset(100, 0));
      await tester.pump();
      return gesture;
    }

    testWidgets('a quick edge swipe drags the drawer open', (tester) async {
      await _shell(tester);
      final gesture = await pull(tester);
      expect(drawerRight(tester), 100);

      await gesture.moveBy(const Offset(150, 0));
      await gesture.up();
      await tester.pumpAndSettle();
      expect(find.text('Settings'), findsOneWidget);
      expect(tester.getTopLeft(find.byType(Drawer)).dx, 0);
    });

    testWidgets('a short fling opens the drawer', (tester) async {
      await _shell(tester);
      await tester.flingFrom(const Offset(5, 300), const Offset(80, 0), 1000);
      await tester.pumpAndSettle();
      expect(find.text('Settings'), findsOneWidget);
    });

    testWidgets('a short edge swipe settles closed', (tester) async {
      await _shell(tester);
      final gesture = await tester.startGesture(const Offset(5, 300));
      for (var i = 0; i < 10; i++) {
        await gesture.moveBy(const Offset(6, 0));
        await tester.pump(const Duration(milliseconds: 100));
      }
      await gesture.up();
      await tester.pumpAndSettle();
      expect(find.byType(Drawer), findsNothing);
    });

    testWidgets('back during a later swipe closes the drawer', (tester) async {
      await _shell(tester);
      await swipe(tester);
      await _back(tester);
      expect(find.byType(Drawer), findsNothing);

      final gesture = await pull(tester);
      expect(await tester.binding.handlePopRoute(), isTrue);
      await tester.pumpAndSettle();
      expect(find.byType(Drawer), findsNothing);
      await gesture.up();
      await tester.pumpAndSettle();
      expect(find.byType(Drawer), findsNothing);
    });

    testWidgets('a swipe dragged back shut leaves back alone', (tester) async {
      await _shell(tester);
      final gesture = await pull(tester);
      await gesture.moveBy(const Offset(-200, 0));
      await gesture.up();
      await tester.pumpAndSettle();
      expect(find.byType(Drawer), findsNothing);
      final route = ModalRoute.of(tester.element(find.byType(ModalShellDrawer)));
      expect(route!.willHandlePopInternally, isFalse);
    });

    testWidgets('switching navigation mode switches the gesture', (tester) async {
      await _shell(tester);
      await setGestureNav(tester);
      await swipe(tester);
      expect(find.byType(Drawer), findsNothing);

      tester.view.resetSystemGestureInsets();
      await tester.pump();
      await swipe(tester);
      expect(find.text('Settings'), findsOneWidget);
    });

    testWidgets('a tap at the edge still reaches the content', (tester) async {
      await tapThroughEdge(tester, await _shell(tester));
    });
  });

  testWidgets('a vanished workspace drops its stack', (tester) async {
    final c = await _shell(tester);
    await _openSessionIn(tester, c, 'A:w2');

    await c
        .read(projectsProvider.notifier)
        .load(FakeGatewayClient((m, p) async => _tree(withW2: false)));
    await tester.pumpAndSettle();
    expect(c.read(scopeProvider), isNull);
    expect(find.byType(SessionDetailScreen), findsNothing);

    await c
        .read(projectsProvider.notifier)
        .load(FakeGatewayClient((m, p) async => _tree()));
    c.read(scopeProvider.notifier).state = 'A:w2';
    await tester.pumpAndSettle();
    expect(find.byType(WorkspaceScreen), findsOneWidget);
    expect(find.byType(SessionDetailScreen), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('resume opens the session inside Home', (tester) async {
    final c = await _shell(
      tester,
      repo: FakeSessionRepository(
        resumeResult: const Result.ok(ResumeOutcome(sessionId: 's9')),
      ),
    );
    c.read(homeTabProvider.notifier).state = HomeTab.history;
    await tester.pump();
    final home = tester.state<NavigatorState>(find
        .descendant(of: find.byType(HomeShell), matching: find.byType(Navigator))
        .first);
    home.push(MaterialPageRoute(
      builder: (_) => Consumer(
        builder: (context, ref, _) => Scaffold(
          body: ElevatedButton(
            onPressed: () => resumeSession(
              context,
              ref,
              agent: 'claude',
              agentSessionId: 'x',
              cwd: '/src',
            ),
            child: const Text('resume'),
          ),
        ),
      ),
    ));
    await tester.pumpAndSettle();
    await tester.tap(find.text('resume'));
    await tester.pumpAndSettle();
    expect(find.text('resume'), findsNothing);
    expect(c.read(homeTabProvider), HomeTab.sessions);

    c.read(sessionsProvider.notifier).replaceAll([_session('s9')]);
    await _pumpRoute(tester);
    expect(_detailInShell, findsOneWidget);
  });

  group('active session', () {
    String? active() => PushNotifications.instance.activeSessionId;

    setUp(() => PushNotifications.instance.setActiveSession(null));
    tearDown(() => PushNotifications.instance.setActiveSession(null));

    Future<void> backgroundAndReturn(WidgetTester tester) async {
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
      await tester.pump();
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
      await tester.pump();
    }

    testWidgets('re-claimed when a route above it pops', (tester) async {
      final c = await _shell(tester);
      await _openSessionIn(tester, c, 'A:w2');
      expect(active(), 's2');
      Navigator.of(tester.element(find.byType(SessionDetailScreen))).push(
        MaterialPageRoute(builder: (_) => const Scaffold(body: Text('above'))),
      );
      await _pumpRoute(tester);

      await backgroundAndReturn(tester);
      expect(active(), isNull);

      await tester.binding.handlePopRoute();
      // maybePop starts the pop a frame later than pop would.
      await tester.pump();
      await _pumpRoute(tester);
      expect(find.text('above'), findsNothing);
      expect(active(), 's2');
    });

    testWidgets('kept when a scope switch reopens the same session', (tester) async {
      final c = await _shell(tester);
      c.read(sessionsProvider.notifier).replaceAll([
        _session('s2', workspaceId: 'A:w2'),
      ]);
      await tester.pump();
      await tester.tap(_card('s2'));
      await _pumpRoute(tester);
      expect(active(), 's2');

      await _switchTo(tester, c, 'A:w2');
      expect(_shownSession(tester), 's2');
      expect(active(), 's2');
    });

    testWidgets('follows the visible scope', (tester) async {
      final c = await _shell(tester);
      await _openSessionIn(tester, c, 'A:w2');
      expect(active(), 's2');

      c.read(scopeProvider.notifier).state = 'A:w1';
      await tester.pumpAndSettle();
      expect(active(), isNull);

      await _openSessionIn(tester, c, 'A:w1');
      expect(active(), 's1');
      await backgroundAndReturn(tester);
      expect(active(), 's1');

      c.read(scopeProvider.notifier).state = 'A:w2';
      await _pumpRoute(tester);
      expect(active(), 's2');
    });
  });

  testWidgets('back respects a pushed route that refuses to pop', (tester) async {
    final c = await _shell(tester);
    c.read(scopeProvider.notifier).state = 'A:w2';
    await tester.pump();
    Navigator.of(tester.element(find.byType(WorkspaceScreen))).push(
      MaterialPageRoute(
        builder: (_) => const PopScope(
          canPop: false,
          child: Scaffold(body: Text('guarded')),
        ),
      ),
    );
    await tester.pumpAndSettle();

    await _back(tester);
    expect(find.text('guarded'), findsOneWidget);
    expect(c.read(scopeProvider), 'A:w2');
  });

  testWidgets('a resumed session switches back to Home after a scope switch', (tester) async {
    final c = await _shell(
      tester,
      repo: FakeSessionRepository(
        resumeResult: const Result.ok(ResumeOutcome(sessionId: 's9')),
      ),
    );
    Navigator.of(tester.element(find.text('History'))).push(MaterialPageRoute(
      builder: (_) => Consumer(
        builder: (context, ref, _) => Scaffold(
          body: ElevatedButton(
            onPressed: () => resumeSession(
              context,
              ref,
              agent: 'claude',
              agentSessionId: 'x',
              cwd: '/src',
            ),
            child: const Text('resume'),
          ),
        ),
      ),
    ));
    await tester.pumpAndSettle();
    await tester.tap(find.text('resume'));
    await tester.pumpAndSettle();

    c.read(scopeProvider.notifier).state = 'A:w2';
    await tester.pumpAndSettle();
    c.read(sessionsProvider.notifier).replaceAll([_session('s9')]);
    await _pumpRoute(tester);
    expect(c.read(scopeProvider), isNull);
    expect(_shownSession(tester), 's9');
  });

  testWidgets('a live screen left by a scope switch shows no detach snackbar', (tester) async {
    final c = await _shell(tester);
    c.read(connStateProvider.notifier).state = ConnState.connected;
    c.read(scopeProvider.notifier).state = 'A:w2';
    await tester.pump();
    Navigator.of(tester.element(find.byType(WorkspaceScreen))).push(
      MaterialPageRoute(
        builder: (_) => LiveScreenScreen(session: _session('s2', workspaceId: 'A:w2')),
      ),
    );
    await _pumpRoute(tester);
    expect(find.byType(LiveScreenScreen), findsOneWidget);
    ScaffoldMessenger.of(tester.element(find.byType(HomeShell))).clearSnackBars();

    c.read(scopeProvider.notifier).state = 'A:w1';
    await tester.pumpAndSettle();
    c.read(connStateProvider.notifier).state = ConnState.reconnecting;
    await _pumpRoute(tester);
    expect(find.text('terminal detached'), findsNothing);
  });

  testWidgets('a pending scope opens when the tree lists it', (tester) async {
    final c = await _shell(tester);
    c.read(pendingScopeProvider.notifier).state = 'A:w3';
    await c
        .read(projectsProvider.notifier)
        .load(FakeGatewayClient((m, p) async => _tree()));
    await tester.pump();
    expect(c.read(scopeProvider), isNull);
    expect(c.read(pendingScopeProvider), 'A:w3');

    final tree = _tree();
    ((tree['projects'] as List).first['workspaces'] as List).add(
        {'id': 'A:w3', 'dir': '/src/argus/.worktrees/new', 'branch': 'new'});
    await c
        .read(projectsProvider.notifier)
        .load(FakeGatewayClient((m, p) async => tree));
    await tester.pump();
    expect(c.read(scopeProvider), 'A:w3');
    expect(c.read(pendingScopeProvider), isNull);
  });

  testWidgets('a manual scope change clears the pending scope', (tester) async {
    final c = await _shell(tester);
    c.read(pendingScopeProvider.notifier).state = 'A:w3';
    c.read(scopeProvider.notifier).state = 'A:w2';
    await tester.pump();
    expect(c.read(pendingScopeProvider), isNull);
  });

  testWidgets('losing the scope to an expected removal shows no notice', (
    tester,
  ) async {
    final c = await _shell(tester);
    c.read(scopeProvider.notifier).state = 'A:w2';
    c.read(expectedGoneProvider.notifier).state = {'A:w2'};
    await tester.pump();
    await c
        .read(projectsProvider.notifier)
        .load(FakeGatewayClient((m, p) async => _tree(withW2: false)));
    await tester.pump();
    expect(c.read(scopeProvider), isNull);
    expect(find.text('registry is no longer available'), findsNothing);
    expect(c.read(expectedGoneProvider), isEmpty);
  });
}
