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
import 'session_detail_screen.dart';
import 'session_list_screen.dart';
import 'shell_drawer.dart';
import 'workspace_screen.dart';

const double kSidePanelMinWidth = 900;
const double kSidePanelWidth = 300;

class HomeShell extends ConsumerStatefulWidget {
  const HomeShell({super.key});

  @override
  ConsumerState<HomeShell> createState() => _HomeShellState();
}

class _HomeShellState extends ConsumerState<HomeShell> {
  final _scaffoldKey = GlobalKey<ScaffoldState>();

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
    final id = ref.read(pendingOpenSessionProvider);
    if (id == null) return;
    final session = ref.read(sessionsProvider)[id];
    if (session == null) return;
    ref.read(pendingOpenSessionProvider.notifier).state = null;
    Navigator.of(context).push(
      MaterialPageRoute(builder: (_) => SessionDetailScreen(session: session)),
    );
  }

  // A reload that lost the open workspace (removed, gone, or its node went
  // offline) returns to Home. A failed reload keeps the last list, so it never
  // lands here.
  void _onProjects(ProjectsState? prev, ProjectsState next) {
    final scope = ref.read(scopeProvider);
    if (scope == null || !next.loaded) return;
    final hit = lookupWorkspace(next.projects, scope);
    if (hit != null && !hit.$2.isGone) return;
    final name = prev == null ? null : lookupWorkspace(prev.projects, scope)?.$2.name;
    ref.read(scopeProvider.notifier).state = null;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(
      content: Text('${name ?? 'The workspace'} is no longer available'),
    ));
  }

  void _back() {
    final scope = ref.read(scopeProvider);
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

  @override
  Widget build(BuildContext context) {
    // Open on a new tap, and re-check when the session list arrives for a tap
    // that pointed at a not-yet-known session.
    ref.listen<String?>(pendingOpenSessionProvider, (_, __) => _openPending());
    ref.listen<Map<String, Session>>(sessionsProvider, (_, __) => _openPending());
    ref.listen<ProjectsState>(projectsProvider, _onProjects);

    final scope = ref.watch(scopeProvider);
    final homeTab = ref.watch(homeTabProvider);
    final projects = ref.watch(projectsProvider).projects;
    final hit = scope == null ? null : lookupWorkspace(projects, scope);
    final main = hit == null
        ? const _HomeBody()
        : WorkspaceScreen(key: ValueKey(scope), project: hit.$1, workspace: hit.$2);
    final wide = MediaQuery.sizeOf(context).width >= kSidePanelMinWidth;

    return PopScope(
      canPop: scope == null && homeTab == HomeTab.sessions,
      onPopInvokedWithResult: (didPop, _) {
        if (!didPop) _back();
      },
      child: ShellDrawerScope(
        openDrawer: wide ? null : () => _scaffoldKey.currentState?.openDrawer(),
        child: Scaffold(
          key: _scaffoldKey,
          drawer: wide ? null : const Drawer(child: ProjectDrawer()),
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
