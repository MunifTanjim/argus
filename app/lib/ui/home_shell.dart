import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

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
  Widget build(BuildContext context) {
    // Deep-link: when a tapped notification sets a pending session id, open the
    // session once it's known, then clear the request.
    ref.listen<String?>(pendingPushSessionProvider, (_, id) {
      if (id == null) return;
      final session = ref.read(sessionsProvider)[id];
      ref.read(pendingPushSessionProvider.notifier).state = null;
      if (session == null) return;
      Navigator.of(context).push(
        MaterialPageRoute(builder: (_) => SessionDetailScreen(session: session)),
      );
    });

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
