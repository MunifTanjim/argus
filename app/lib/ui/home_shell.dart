import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/session.dart';
import '../state/push.dart';
import '../state/sessions.dart';
import 'history_screen.dart';
import 'session_detail_screen.dart';
import 'session_list_screen.dart';
import 'settings_screen.dart';

class HomeShell extends ConsumerStatefulWidget {
  const HomeShell({super.key});

  @override
  ConsumerState<HomeShell> createState() => _HomeShellState();
}

class _HomeShellState extends ConsumerState<HomeShell> {
  int _index = 0;

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
    final id = ref.read(pendingPushSessionProvider);
    if (id == null) return;
    final session = ref.read(sessionsProvider)[id];
    if (session == null) return;
    ref.read(pendingPushSessionProvider.notifier).state = null;
    Navigator.of(context).push(
      MaterialPageRoute(builder: (_) => SessionDetailScreen(session: session)),
    );
  }

  @override
  Widget build(BuildContext context) {
    // Open on a new tap, and re-check when the session list arrives for a tap
    // that pointed at a not-yet-known session.
    ref.listen<String?>(pendingPushSessionProvider, (_, __) => _openPending());
    ref.listen<Map<String, Session>>(sessionsProvider, (_, __) => _openPending());

    final tabs = [
      const SessionListScreen(),
      const HistoryScreen(),
      const SettingsScreen(),
    ];
    // The tabs share one route, so back on a non-first tab would otherwise exit
    // the app. Intercept it to return to Sessions first; only exit from Sessions.
    return PopScope(
      canPop: _index == 0,
      onPopInvokedWithResult: (didPop, _) {
        if (!didPop && _index != 0) setState(() => _index = 0);
      },
      child: Scaffold(
        body: IndexedStack(index: _index, children: tabs),
        bottomNavigationBar: NavigationBar(
          selectedIndex: _index,
          onDestinationSelected: (i) => setState(() => _index = i),
          destinations: const [
            NavigationDestination(
                icon: Icon(Icons.dashboard_outlined), label: 'Sessions'),
            NavigationDestination(icon: Icon(Icons.history), label: 'History'),
            NavigationDestination(
                icon: Icon(Icons.settings_outlined), label: 'Settings'),
          ],
        ),
      ),
    );
  }
}
