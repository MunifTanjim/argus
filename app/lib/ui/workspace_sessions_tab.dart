import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../state/gateway.dart';
import '../state/sessions.dart';
import '../transport/connection.dart';
import 'session_sections_list.dart';

class WorkspaceSessionsTab extends ConsumerWidget {
  const WorkspaceSessionsTab({super.key, required this.workspaceId});

  final String workspaceId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final sessions = ref
        .watch(sessionsProvider)
        .values
        .where((s) => s.workspaceId == workspaceId)
        .toList();
    final conn = ref.watch(connStateProvider);
    return Column(
      children: [
        if (conn != ConnState.connected)
          ReconnectBanner(state: conn, message: ref.watch(connErrorProvider)),
        Expanded(
          child: RefreshIndicator(
            onRefresh: () async {
              final client = ref.read(gatewayProvider)?.client;
              if (client == null) return;
              await refreshSessions(
                client,
                ref.read(sessionsProvider.notifier),
              );
            },
            child: SessionSectionsList(
              sessions: sessions,
              emptyText: 'No sessions in this workspace.',
            ),
          ),
        ),
      ],
    );
  }
}
