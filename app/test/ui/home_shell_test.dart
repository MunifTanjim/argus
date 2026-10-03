import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
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
import 'package:argus/state/sessions.dart';
import 'package:argus/state/terminals.dart';
import 'package:argus/transport/connection.dart';
import 'package:argus/transport/gateway_client.dart';
import 'package:argus/ui/history_screen.dart';
import 'package:argus/ui/history_transcript_screen.dart';
import 'package:argus/ui/home_shell.dart';
import 'package:argus/ui/live_screen_screen.dart';
import 'package:argus/ui/resume_action.dart';
import 'package:argus/ui/session_card.dart';
import 'package:argus/ui/session_detail_screen.dart';
import 'package:argus/ui/session_list_screen.dart';
import 'package:argus/ui/terminal_list_screen.dart';
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
}) async {
  tester.view.physicalSize = Size(width, 900);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  final c = ProviderContainer(overrides: [
    gatewayProvider.overrideWithValue(null),
    if (repo != null) sessionRepositoryProvider.overrideWithValue(repo),
  ]);
  addTearDown(c.dispose);
  await c.read(projectsProvider.notifier).load(FakeGatewayClient((m, p) async => _tree()));
  await tester.pumpWidget(UncontrolledProviderScope(
    container: c,
    child: const MaterialApp(home: HomeShell()),
  ));
  await tester.pump();
  return c;
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

void main() {
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

    await _switchTo(tester, c, 'A:w2');
    expect(_detailInShell, findsNothing);
    expect(find.byType(WorkspaceScreen), findsOneWidget);
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

  testWidgets('back from a tapped session goes to the root, not the replaced one', (tester) async {
    final c = await _shell(tester);
    await _openSessionIn(tester, c, 'A:w2');
    c.read(pendingOpenSessionProvider.notifier).state = const PendingOpen('s1');
    await _pumpRoute(tester);
    await tester.binding.handlePopRoute();
    await _pumpRoute(tester);
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

  group('edge hold', () {
    Future<TestGesture> hold(WidgetTester tester) async {
      final gesture = await tester.startGesture(const Offset(5, 300));
      await tester.pump(const Duration(milliseconds: 350));
      await tester.pumpAndSettle();
      return gesture;
    }

    double drawerRight(WidgetTester tester) =>
        tester.getTopRight(find.byType(Drawer)).dx;

    testWidgets('a quick edge swipe leaves the drawer closed', (tester) async {
      await _shell(tester);
      await tester.dragFrom(const Offset(5, 300), const Offset(300, 0));
      await tester.pumpAndSettle();
      expect(find.byType(Drawer), findsNothing);
    });

    testWidgets('a hold peeks the drawer, a pull drags it open', (tester) async {
      await _shell(tester);
      final gesture = await hold(tester);
      expect(drawerRight(tester), 20);

      await gesture.moveBy(const Offset(100, 0));
      await tester.pump();
      expect(drawerRight(tester), 120);
      await gesture.moveBy(const Offset(150, 0));
      await gesture.up();
      await tester.pumpAndSettle();
      expect(find.text('Settings'), findsOneWidget);
      expect(tester.getTopLeft(find.byType(Drawer)).dx, 0);
    });

    testWidgets('the peek covers the system gesture inset', (tester) async {
      await _shell(tester);
      tester.view.systemGestureInsets = const FakeViewPadding(left: 30);
      await tester.pump();
      await hold(tester);
      expect(drawerRight(tester), 30);
    });

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
      final gesture = await hold(tester);
      expect(find.byType(Drawer), findsOneWidget);
      await gesture.up();
      await tester.pumpAndSettle();
      expect(find.byType(Drawer), findsNothing);
    });

    testWidgets('a tap at the edge still reaches the content', (tester) async {
      final c = await _shell(tester);
      c.read(sessionsProvider.notifier).replaceAll([_session('s1')]);
      await tester.pump();
      final card = tester.getRect(find.byType(SessionCard));
      expect(card.left + 2, lessThan(20));
      await tester.tapAt(Offset(card.left + 2, card.center.dy));
      await _pumpRoute(tester);
      expect(_detailInShell, findsOneWidget);
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
