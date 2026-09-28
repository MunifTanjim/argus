import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/enums.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/navigation.dart';
import 'package:argus/state/projects.dart';
import 'package:argus/state/push.dart';
import 'package:argus/state/sessions.dart';
import 'package:argus/ui/home_shell.dart';
import 'package:argus/ui/session_detail_screen.dart';
import 'package:argus/ui/workspace_screen.dart';

import '../support/fake_gateway_client.dart';

Session _session(String id) => Session(
      id: id,
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

Future<ProviderContainer> _shell(WidgetTester tester, {double width = 400}) async {
  tester.view.physicalSize = Size(width, 900);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  final c = ProviderContainer(overrides: [gatewayProvider.overrideWithValue(null)]);
  addTearDown(c.dispose);
  await c.read(projectsProvider.notifier).load(FakeGatewayClient((m, p) async => _tree()));
  await tester.pumpWidget(UncontrolledProviderScope(
    container: c,
    child: const MaterialApp(home: HomeShell()),
  ));
  await tester.pump();
  return c;
}

Future<void> _back(WidgetTester tester) async {
  await tester.binding.handlePopRoute();
  await tester.pumpAndSettle();
}

void main() {
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
    container.read(pendingOpenSessionProvider.notifier).state = 's1';

    await tester.pumpWidget(UncontrolledProviderScope(
      container: container,
      child: const MaterialApp(home: HomeShell()),
    ));
    // SessionDetailScreen shows a perpetual spinner without a connection, so
    // settle is impossible; pump past the route transition instead.
    await tester.pump();
    await tester.pump(const Duration(seconds: 1));

    expect(find.byType(SessionDetailScreen), findsOneWidget);
    expect(container.read(pendingOpenSessionProvider), isNull);
  });

  testWidgets('opens a pending session once the list is fetched', (tester) async {
    final container = ProviderContainer(
      overrides: [gatewayProvider.overrideWithValue(null)],
    );
    addTearDown(container.dispose);
    // Cold start: the tap sets a pending session, but the list has not arrived.
    container.read(pendingOpenSessionProvider.notifier).state = 's1';

    await tester.pumpWidget(UncontrolledProviderScope(
      container: container,
      child: const MaterialApp(home: HomeShell()),
    ));
    await tester.pump();

    // Nothing to open yet; the request must be kept, not discarded.
    expect(find.byType(SessionDetailScreen), findsNothing);
    expect(container.read(pendingOpenSessionProvider), 's1');

    // The session list is fetched; the pending session opens now.
    container.read(sessionsProvider.notifier).replaceAll([_session('s1')]);
    await tester.pump();
    await tester.pump(const Duration(seconds: 1));

    expect(find.byType(SessionDetailScreen), findsOneWidget);
    expect(container.read(pendingOpenSessionProvider), isNull);
  });
}
