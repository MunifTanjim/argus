import 'dart:convert';

import 'package:argus/data/session_repository.dart';
import 'package:argus/models/project.dart';
import 'package:argus/models/session.dart';
import 'package:argus/models/terminal.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/left_sidebar.dart';
import 'package:argus/state/projects.dart';
import 'package:argus/state/sessions.dart';
import 'package:argus/state/terminals.dart';
import 'package:argus/ui/code_block.dart';
import 'package:argus/ui/project_drawer.dart';
import 'package:argus/ui/session_actions.dart';
import 'package:argus/ui/session_card.dart';
import 'package:argus/ui/session_sections_list.dart';
import 'package:argus/ui/sidebar_sessions_list.dart';
import 'package:argus/ui/terminal_tile.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../support/fake_session_repository.dart';

Session _s(String id) => Session.fromJson(
  jsonDecode(
        jsonEncode({
          'id': id,
          'frontend': 'tmux',
          'agent': 'claude',
          'name': 'Old',
          'status': 'idle',
          'source': 'hooked',
          'node_id': 'A',
          'node_label': 'dev',
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

const _projects = [
  ProjectNode(
    id: 'A:p1',
    name: 'argus',
    nodeId: 'A',
    workspaces: [
      WorkspaceNode(id: 'A:w1', dir: '/src/argus', isMain: true),
      WorkspaceNode(id: 'A:w2', dir: '/src/argus-feat', branch: 'feat/x'),
    ],
  ),
];

class _Projects extends ProjectsNotifier {
  @override
  ProjectsState build() =>
      const ProjectsState(projects: _projects, loaded: true);
}

class _Sessions extends SessionsNotifier {
  @override
  Map<String, Session> build() => {'A:s1': _s('A:s1')};
}

class _Terminals extends TerminalsNotifier {
  @override
  TerminalsState build() => const TerminalsState(
    terminals: [NodeTerminal(id: 'A:1', nodeId: 'A', name: 'my-shell')],
    loaded: true,
  );
}

void _desktop(WidgetTester tester) {
  debugDefaultTargetPlatformOverride = TargetPlatform.macOS;
  addTearDown(() => debugDefaultTargetPlatformOverride = null);
  tester.view.physicalSize = const Size(1200, 800);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
}

Future<void> _pump(WidgetTester tester, Widget body) => tester.pumpWidget(
  ProviderScope(
    overrides: [
      projectsProvider.overrideWith(_Projects.new),
      sessionsProvider.overrideWith(_Sessions.new),
      terminalsProvider.overrideWith(_Terminals.new),
      gatewayProvider.overrideWithValue(null),
      sessionRepositoryProvider.overrideWithValue(FakeSessionRepository()),
    ],
    child: MaterialApp(home: Scaffold(body: body)),
  ),
);

Future<void> _rightClick(WidgetTester tester, Finder f) async {
  await tester.tap(f, buttons: kSecondaryButton);
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('right-click on a SessionCard opens the session actions menu', (
    tester,
  ) async {
    _desktop(tester);
    await _pump(tester, SessionSectionsList(sessions: [_s('A:s1')]));
    await _rightClick(tester, find.text('Old'));
    expect(find.byType(PopupMenuItem<String>), findsWidgets);
    expect(find.text('Rename'), findsOneWidget);
    expect(find.text('Kill'), findsOneWidget);
  });

  testWidgets('right-click on a project row opens the project actions menu', (
    tester,
  ) async {
    _desktop(tester);
    await _pump(tester, const ProjectDrawer());
    await tester.pump();
    await _rightClick(tester, find.text('argus').first);
    expect(find.text('Forget'), findsOneWidget);
  });

  testWidgets(
    'right-click on a workspace row opens the workspace actions menu',
    (tester) async {
      _desktop(tester);
      await _pump(tester, const ProjectDrawer());
      await tester.pump();
      await _rightClick(tester, find.text('feat/x').first);
      expect(find.text('Force remove'), findsOneWidget);
    },
  );

  testWidgets(
    'right-click on a terminal tile opens the terminal actions menu',
    (tester) async {
      _desktop(tester);
      await _pump(
        tester,
        const TerminalTile(
          terminal: NodeTerminal(id: 'A:1', nodeId: 'A', name: 'my-shell'),
        ),
      );
      await _rightClick(tester, find.text('my-shell'));
      expect(find.text('Rename'), findsOneWidget);
      expect(find.text('Kill'), findsOneWidget);
    },
  );

  testWidgets('right-click on a sidebar session opens the details side panel', (
    tester,
  ) async {
    _desktop(tester);
    await _pump(tester, SidebarSessionsList(onOpen: () {}));
    await _rightClick(tester, find.text('Old'));
    expect(find.text('Status'), findsOneWidget);
    expect(find.text('ID'), findsOneWidget);
  });

  testWidgets(
    'a right-click near the bottom-right corner keeps the menu on screen',
    (tester) async {
      _desktop(tester);
      await _pump(
        tester,
        Align(
          alignment: Alignment.bottomRight,
          child: SizedBox(
            width: 300,
            child: Builder(
              builder: (context) => SessionCard(
                session: _s('A:s1'),
                onContextMenu: (pos) =>
                    showSessionActions(context, _s('A:s1'), position: pos),
              ),
            ),
          ),
        ),
      );
      final gesture = await tester.startGesture(
        const Offset(1190, 790),
        kind: PointerDeviceKind.mouse,
        buttons: kSecondaryButton,
      );
      await gesture.up();
      await tester.pumpAndSettle();
      final items = find.byType(PopupMenuItem<String>);
      expect(items, findsWidgets);
      for (final e in items.evaluate()) {
        final r = tester.getRect(find.byWidget(e.widget));
        expect(const Offset(0, 0) & const Size(1200, 800), _contains(r));
      }
    },
  );

  testWidgets('long-press on a SessionCard still opens the same actions', (
    tester,
  ) async {
    await _pump(tester, SessionSectionsList(sessions: [_s('A:s1')]));
    await tester.longPress(find.text('Old'));
    await tester.pumpAndSettle();
    expect(find.text('Rename'), findsOneWidget);
    expect(find.text('Kill'), findsOneWidget);
  });

  for (final right in [true, false]) {
    testWidgets(
      '${right ? 'right' : 'left'}-click on a markdown link anchors the menu at the pointer',
      (tester) async {
        _desktop(tester);
        await _pump(
          tester,
          Align(
            alignment: Alignment.topLeft,
            child: SizedBox(
              width: 400,
              height: 600,
              child: appMarkdown('see [docs](https://argus.test)'),
            ),
          ),
        );
        final at = tester.getCenter(find.text('docs'));
        await tester.tap(
          find.text('docs'),
          buttons: right ? kSecondaryButton : kPrimaryButton,
        );
        await tester.pumpAndSettle();
        expect(find.text('Open link'), findsOneWidget);
        final item = tester.getRect(find.text('Open link'));
        expect((item.left - at.dx).abs(), lessThan(100));
        expect((item.top - at.dy).abs(), lessThan(100));
      },
    );
  }
}

Matcher _contains(Rect r) => _RectContains(r);

class _RectContains extends Matcher {
  _RectContains(this.inner);
  final Rect inner;
  @override
  Description describe(Description d) => d.add('contains $inner');
  @override
  bool matches(Object? item, Map matchState) {
    final outer = item as Rect;
    return outer.left <= inner.left &&
        outer.top <= inner.top &&
        outer.right >= inner.right &&
        outer.bottom >= inner.bottom;
  }
}
