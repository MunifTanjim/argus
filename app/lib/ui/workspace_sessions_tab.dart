import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/project.dart';
import '../state/gateway.dart';
import '../state/projects.dart';
import '../state/sessions.dart';
import '../transport/connection.dart';
import 'project_actions.dart';
import 'session_sections_list.dart';
import 'setup_card.dart';

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
    final hit = lookupWorkspace(
      ref.watch(projectsProvider).projects,
      workspaceId,
    );
    return Column(
      children: [
        if (conn != ConnState.connected)
          ReconnectBanner(state: conn, message: ref.watch(connErrorProvider)),
        if (hit != null)
          SetupCard(
            project: hit.$1,
            workspace: hit.$2,
            onRunAgain: () => runSetup(ActionContext.of(context), hit.$2),
            onFullLog: () => openSetupLog(ActionContext.of(context), hit.$2),
          ),
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
