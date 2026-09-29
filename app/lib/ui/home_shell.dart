import 'dart:math';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/project.dart';
import '../models/session.dart';
import '../state/navigation.dart';
import '../state/projects.dart';
import '../state/push.dart';
import '../state/sessions.dart';
import 'history_screen.dart';
import 'project_drawer.dart';
import 'route_observer.dart';
import 'session_detail_screen.dart';
import 'session_list_screen.dart';
import 'shell_drawer.dart';
import 'workspace_screen.dart';

const double kSidePanelMinWidth = 900;
const double kSidePanelWidth = 300;

// Material's modal drawer: screen width - 56, at most 400.
const double kDrawerMaxWidth = 400;

class HomeShell extends ConsumerStatefulWidget {
  const HomeShell({super.key});

  @override
  ConsumerState<HomeShell> createState() => _HomeShellState();
}

class _HomeShellState extends ConsumerState<HomeShell> {
  final _scaffoldKey = GlobalKey<ScaffoldState>();
  // Only the current scope (null is Home) has a navigator; entering a scope
  // builds a new one from its remembered session.
  _ScopeNavigator? _nav;
  bool _canPop = false;

  @override
  void initState() {
    super.initState();
    // A tap can set the pending session before this mounts (cold launch from a
    // notification); ref.listen only sees later changes, so open it once here.
    WidgetsBinding.instance.addPostFrameCallback((_) => _openPending());
  }

  // Deep-link a tapped notification's session. Keep the request until its
  // session is actually known, so a cold-launch tap opens once the list is
  // fetched rather than being dropped while the list is still empty.
  void _openPending() {
    if (!mounted) return;
    final pending = ref.read(pendingOpenSessionProvider);
    if (pending == null) return;
    final session = ref.read(sessionsProvider)[pending.sessionId];
    if (session == null) return;
    final scope = ref.read(scopeProvider);
    if (pending.inHome && scope != null) {
      ref.read(scopeProvider.notifier).state = null;
    }
    final target = pending.inHome ? null : scope;
    final nav = _nav?.scope == target ? _nav?.key.currentState : null;
    if (nav == null) {
      // The scope's navigator is built on the next frame.
      WidgetsBinding.instance.addPostFrameCallback((_) => _openPending());
      return;
    }
    ref.read(pendingOpenSessionProvider.notifier).state = null;
    // As in the TUI, a session opened over a session detail replaces it; the
    // replaced one was shown, so it is remembered.
    final shown = _nav!.stack.routes.lastOrNull;
    final shownId = shown == null ? null : sessionDetailRouteId(shown);
    if (shownId == null) {
      nav.push(sessionDetailRoute(session));
      return;
    }
    _remember(shownId);
    nav.pushReplacement(sessionDetailRoute(session));
  }

  // Keeps a session under its own workspace, or Home if it has none.
  void _remember(String id) {
    final session = ref.read(sessionsProvider)[id];
    if (session == null) return;
    final ws = session.workspaceId;
    ref
        .read(rememberedSessionsProvider.notifier)
        .remember(ws == null || ws.isEmpty ? null : ws, id);
  }

  // Before the navigator of the scope being left goes away, keep its session
  // details, as the TUI view memory does. Bottom to top, so the one shown wins
  // for its workspace. Deeper screens are not kept.
  void _onScope(String? prev, String? next) {
    final nav = _nav;
    if (nav == null || nav.scope != prev) return;
    final ids = nav.stack.sessionIds;
    if (ids.isEmpty && nav.stack.routes.length == 1) {
      ref.read(rememberedSessionsProvider.notifier).clear(prev);
    }
    ids.forEach(_remember);
  }

  void _onSessions(Map<String, Session>? prev, Map<String, Session> next) {
    ref
        .read(rememberedSessionsProvider.notifier)
        .retain((_, id) => next.containsKey(id));
    _openPending();
  }

  // A pop in the current scope's navigator (back or the app bar) forgets the
  // session detail it closes.
  void _onPop(_ScopeNavigator nav, Route<dynamic> route) {
    if (nav != _nav || nav.scope != ref.read(scopeProvider)) return;
    final id = sessionDetailRouteId(route);
    if (id != null) ref.read(rememberedSessionsProvider.notifier).forget(id);
  }

  static bool _available(List<ProjectNode> projects, String scope) {
    final hit = lookupWorkspace(projects, scope);
    return hit != null && !hit.$2.isGone;
  }

  // A reload that lost the open workspace (removed, gone, or its node went
  // offline) returns to Home. A failed reload keeps the last list, so it never
  // lands here.
  void _onProjects(ProjectsState? prev, ProjectsState next) {
    if (!next.loaded) return;
    final scope = ref.read(scopeProvider);
    final expected = ref.read(expectedGoneProvider);
    if (scope != null && !_available(next.projects, scope)) {
      final name = prev == null
          ? null
          : lookupWorkspace(prev.projects, scope)?.$2.name;
      ref.read(scopeProvider.notifier).state = null;
      if (!expected.contains(scope)) {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(
          content: Text('${name ?? 'The workspace'} is no longer available'),
        ));
      }
    }
    if (expected.isNotEmpty) {
      ref.read(expectedGoneProvider.notifier).state = {
        for (final id in expected)
          if (lookupWorkspace(next.projects, id) != null) id,
      };
    }
    // After the scope change, which can remember a session of the lost
    // workspace.
    ref
        .read(rememberedSessionsProvider.notifier)
        .retain((s, _) => s == null || _available(next.projects, s));
  }

  void _back() {
    final drawer = _scaffoldKey.currentState;
    if (drawer != null && drawer.isDrawerOpen) {
      drawer.closeDrawer();
      return;
    }
    final scope = ref.read(scopeProvider);
    final nav = _nav?.key.currentState;
    if (nav != null && nav.canPop()) {
      nav.maybePop();
      return;
    }
    if (scope != null) {
      final path = ref.read(filesPathProvider(scope));
      if (ref.read(workspaceTabProvider(scope)) == WorkspaceTab.files &&
          path.isNotEmpty) {
        ref.read(filesPathProvider(scope).notifier).state = parentPath(path);
        return;
      }
      ref.read(scopeProvider.notifier).state = null;
      return;
    }
    if (ref.read(homeTabProvider) != HomeTab.sessions) {
      ref.read(homeTabProvider.notifier).state = HomeTab.sessions;
    }
  }

  bool _onNavigation() {
    final can = _nav?.key.currentState?.canPop() ?? false;
    if (can != _canPop) setState(() => _canPop = can);
    return false;
  }

  _ScopeNavigator _enter(String? scope) {
    final id = ref.read(rememberedSessionsProvider)[scope];
    final session = id == null ? null : ref.read(sessionsProvider)[id];
    _canPop = session != null;
    return _ScopeNavigator(scope, _onPop, session);
  }

  Widget _navigator(_ScopeNavigator nav) {
    Route<dynamic> root(RouteSettings settings) => MaterialPageRoute(
      settings: settings,
      builder: (_) =>
          nav.scope == null ? const _HomeBody() : _WorkspaceRoot(nav.scope!),
    );
    return ShellNavigatorScope(
      observer: nav.observer,
      child: NotificationListener<NavigationNotification>(
        onNotification: (_) => _onNavigation(),
        child: Navigator(
          key: nav.key,
          observers: [nav.observer, nav.stack],
          onGenerateRoute: root,
          onGenerateInitialRoutes: (_, name) => [
            root(RouteSettings(name: name)),
            if (nav.session case final s?) sessionDetailRoute(s),
          ],
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    // Open on a new tap, and re-check when the session list arrives for a tap
    // that pointed at a not-yet-known session.
    ref.listen<PendingOpen?>(pendingOpenSessionProvider, (_, _) => _openPending());
    ref.listen<Map<String, Session>>(sessionsProvider, _onSessions);
    ref.listen<ProjectsState>(projectsProvider, _onProjects);
    ref.listen<String?>(scopeProvider, _onScope);

    final scope = ref.watch(scopeProvider);
    final homeTab = ref.watch(homeTabProvider);
    if (_nav == null || _nav!.scope != scope) _nav = _enter(scope);
    final main = _navigator(_nav!);
    final screenWidth = MediaQuery.sizeOf(context).width;
    final wide = screenWidth >= kSidePanelMinWidth;

    return PopScope(
      canPop: scope == null && homeTab == HomeTab.sessions && !_canPop,
      onPopInvokedWithResult: (didPop, _) {
        if (!didPop) _back();
      },
      child: ShellDrawerScope(
        openDrawer: wide ? null : () => _scaffoldKey.currentState?.openDrawer(),
        child: Scaffold(
          key: _scaffoldKey,
          drawer: wide
              ? null
              : Drawer(
                  width: min(screenWidth - 56, kDrawerMaxWidth),
                  child: const ProjectDrawer(),
                ),
          body: wide
              ? Row(
                  children: [
                    const SizedBox(width: kSidePanelWidth, child: ProjectDrawer()),
                    const VerticalDivider(width: 1),
                    Expanded(child: main),
                  ],
                )
              : main,
        ),
      ),
    );
  }
}

class _ScopeNavigator {
  _ScopeNavigator(
    this.scope,
    void Function(_ScopeNavigator, Route<dynamic>) onPop,
    this.session,
  ) {
    stack = _StackObserver((route) => onPop(this, route));
  }

  final String? scope;
  final Session? session;
  final key = GlobalKey<NavigatorState>();
  final observer = RouteObserver<PageRoute<dynamic>>();
  late final _StackObserver stack;
}

class _StackObserver extends NavigatorObserver {
  _StackObserver(this.onPop);

  final void Function(Route<dynamic>) onPop;
  final routes = <Route<dynamic>>[];

  /// The session details above the root, bottom to top.
  List<String> get sessionIds =>
      [for (final r in routes.skip(1)) ?sessionDetailRouteId(r)];

  @override
  void didPush(Route<dynamic> route, Route<dynamic>? previousRoute) =>
      routes.add(route);

  @override
  void didPop(Route<dynamic> route, Route<dynamic>? previousRoute) {
    routes.remove(route);
    onPop(route);
  }

  @override
  void didRemove(Route<dynamic> route, Route<dynamic>? previousRoute) =>
      routes.remove(route);

  @override
  void didReplace({Route<dynamic>? newRoute, Route<dynamic>? oldRoute}) {
    final i = oldRoute == null ? -1 : routes.indexOf(oldRoute);
    if (i < 0 || newRoute == null) return;
    routes[i] = newRoute;
  }
}

class _WorkspaceRoot extends ConsumerWidget {
  const _WorkspaceRoot(this.scope);

  final String scope;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final hit = lookupWorkspace(ref.watch(projectsProvider).projects, scope);
    if (hit == null) return const SizedBox.shrink();
    return WorkspaceScreen(project: hit.$1, workspace: hit.$2);
  }
}

class _HomeBody extends ConsumerWidget {
  const _HomeBody();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final tab = ref.watch(homeTabProvider);
    return Scaffold(
      body: IndexedStack(
        index: tab.index,
        children: const [SessionListScreen(), HistoryScreen()],
      ),
      bottomNavigationBar: NavigationBar(
        selectedIndex: tab.index,
        onDestinationSelected: (i) =>
            ref.read(homeTabProvider.notifier).state = HomeTab.values[i],
        destinations: const [
          NavigationDestination(icon: Icon(Icons.dashboard_outlined), label: 'Sessions'),
          NavigationDestination(icon: Icon(Icons.history), label: 'History'),
        ],
      ),
    );
  }
}
