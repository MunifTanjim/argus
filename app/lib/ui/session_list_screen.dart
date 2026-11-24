import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../state/device_identity.dart';
import '../state/gateway.dart';
import '../state/session_filter.dart';
import '../state/sessions.dart';
import '../transport/connection.dart';
import '../util/platform.dart';
import 'device_identity_screen.dart';
import 'session_sections_list.dart';
import 'shell_drawer.dart';
import 'spawn_dialog.dart';
import 'titlebar.dart';

class SessionListScreen extends ConsumerWidget {
  const SessionListScreen({super.key});

  Future<void> _refresh(WidgetRef ref) async {
    final client = ref.read(gatewayProvider)?.client;
    if (client == null) return;
    await refreshSessions(client, ref.read(sessionsProvider.notifier));
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final sessions = ref.watch(sessionsProvider).values;
    final conn = ref.watch(connStateProvider);
    final connError = ref.watch(connErrorProvider);
    final unauthorized =
        trustStatusOf(ref.watch(trustSummaryProvider)) ==
        TrustStatus.awaitingAuthorization;

    return Scaffold(
      appBar: ArgusAppBar(
        leading: shellMenuButton(context),
        title: const SessionSearchTitle(child: TabTitle('Sessions')),
        actions: [
          if (isDesktop)
            IconButton(
              tooltip: 'New session',
              onPressed: () => Navigator.of(context).push(spawnRoute()),
              icon: const Icon(Icons.add),
            ),
          const SessionSearchButton(),
          const GroupByButton(),
          const ActiveOnlyButton(),
        ],
      ),
      // The home tabs share one route, so their buttons cannot share the
      // default hero tag.
      floatingActionButton: isDesktop
          ? null
          : FloatingActionButton(
              heroTag: null,
              tooltip: 'New session',
              onPressed: () => Navigator.of(context).push(spawnRoute()),
              child: const Icon(Icons.add),
            ),
      body: Column(
        children: [
          if (conn != ConnState.connected)
            ReconnectBanner(state: conn, message: connError),
          if (unauthorized)
            UnauthorizedBanner(
              onTap: () => Navigator.of(context).push(
                MaterialPageRoute(builder: (_) => const DeviceIdentityScreen()),
              ),
            ),
          Expanded(
            child: RefreshIndicator(
              onRefresh: () => _refresh(ref),
              child: SessionSectionsList(
                sessions: sessions,
                groupBy: ref.watch(groupByProvider),
                emptyText: unauthorized
                    ? 'Sessions appear after this device is authorized.'
                    : 'No sessions.',
              ),
            ),
          ),
        ],
      ),
    );
  }
}
