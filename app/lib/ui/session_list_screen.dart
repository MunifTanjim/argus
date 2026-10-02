import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../state/device_identity.dart';
import '../state/gateway.dart';
import '../state/sessions.dart';
import '../transport/connection.dart';
import 'device_identity_screen.dart';
import 'session_sections_list.dart';
import 'shell_drawer.dart';
import 'spawn_dialog.dart';

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
      appBar: AppBar(
        leading: shellMenuButton(context),
        title: const SessionSearchTitle(child: Text('Sessions')),
        actions: const [SessionSearchButton(), ActiveOnlyButton()],
      ),
      floatingActionButton: FloatingActionButton.extended(
        onPressed: () => showSpawnDialog(context, ref),
        icon: const Icon(Icons.add),
        label: const Text('New session'),
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
