import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/enums.dart';
import '../models/project.dart';
import '../models/session.dart';
import '../state/gateway.dart';
import '../state/left_sidebar.dart';
import '../state/navigation.dart';
import '../state/projects.dart';
import '../state/push.dart';
import '../state/sessions.dart';
import '../state/sidebar_sessions.dart';
import 'agent_badge.dart';
import 'project_actions.dart';
import 'relative_time.dart';
import 'session_actions.dart';
import 'status_style.dart';
import 'theme.dart';

/// The left sidebar's Sessions tab: live sessions by status.
class SidebarSessionsList extends ConsumerWidget {
  const SidebarSessionsList({super.key, required this.onOpen});

  /// Runs after a tap asks the shell to open a session (closes a modal drawer).
  final VoidCallback onOpen;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final sessions = ref.watch(sessionsProvider).values;
    final projects = ref.watch(projectsProvider).projects;
    final shown = ref.watch(shownSessionProvider);
    final query = ref.watch(sidebarSessionsFilterProvider).trim();
    final groups = buildSidebarSections(
      sessions.where(
        (s) => sidebarSessionMatches(s, query, _place(s, projects, false)),
      ),
    );
    if (groups.isEmpty) {
      return RefreshIndicator(
        onRefresh: () => _refresh(ref),
        child: ListView(
          children: [
            const SizedBox(height: 120),
            Center(
              child: Text(
                query.isEmpty
                    ? 'no live sessions'
                    : 'no sessions match "$query"',
                style: const TextStyle(color: AppColors.dim),
              ),
            ),
          ],
        ),
      );
    }
    final showAgent = _mixesAgents([for (final g in groups) ...g.sessions]);
    return RefreshIndicator(
      onRefresh: () => _refresh(ref),
      child: ListView(
        padding: const EdgeInsets.only(bottom: 8),
        children: [
          for (final g in groups) ...[
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
              child: Text(
                g.section.title,
                style: const TextStyle(color: AppColors.dim, fontSize: 12),
              ),
            ),
            for (final s in g.sessions)
              _SessionRow(
                session: s,
                place: _place(s, projects, showAgent),
                selected: s.id == shown,
                onTap: () {
                  ref.read(pendingOpenSessionProvider.notifier).state =
                      PendingOpen(s.id);
                  onOpen();
                },
                onLongPress: () => _showDetails(context, s),
              ),
          ],
        ],
      ),
    );
  }

  static Future<void> _refresh(WidgetRef ref) async {
    final client = ref.read(gatewayProvider)?.client;
    if (client == null) return;
    await refreshSessions(client, ref.read(sessionsProvider.notifier));
  }

  // The app's counterpart of the TUI's hover: what a row cannot fit, and the
  // session's actions.
  static Future<void> _showDetails(BuildContext context, Session s) {
    final ax = ActionContext.of(context);
    final status = s.statusLabel.isNotEmpty ? s.statusLabel : s.status.name;
    final tmux = s.tmux.paneId.isEmpty
        ? ''
        : [
            s.tmux.sessionName,
            s.tmux.paneId,
            switch (s.tmux.server) {
              TmuxServerKind.default_ => 'default',
              TmuxServerKind.argus => 'argus',
              TmuxServerKind.unknown => '',
            },
          ].where((p) => p.isNotEmpty).join(' · ');
    final fields = [
      ('Status', s.offline ? '$status, offline' : status),
      ('Agent', agentLabel(s.agent)),
      ('Task', s.summary?.task ?? ''),
      ('Last activity', relativeTime(s.summary?.lastActivity)),
      ('Dir', s.cwd ?? ''),
      ('Branch', s.branch ?? ''),
      ('Node', s.nodeLabel ?? ''),
      ('Tmux', tmux),
      ('ID', s.id),
    ].where((f) => f.$2.isNotEmpty);
    return showModalBottomSheet<void>(
      context: context,
      useRootNavigator: true,
      builder: (ctx) => SafeArea(
        child: Padding(
          padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                sidebarTitle(s),
                style: const TextStyle(fontWeight: FontWeight.w600),
              ),
              const SizedBox(height: 12),
              for (final (label, value) in fields)
                Padding(
                  padding: const EdgeInsets.only(bottom: 8),
                  child: Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      SizedBox(
                        width: 104,
                        child: Text(
                          label,
                          style: const TextStyle(color: AppColors.dim),
                        ),
                      ),
                      Expanded(child: SelectableText(value)),
                    ],
                  ),
                ),
              ...sessionActionTiles(ctx, ax, s),
            ],
          ),
        ),
      ),
    );
  }

  static bool _mixesAgents(List<Session> listed) =>
      listed.map((s) => s.agent).where((a) => a.isNotEmpty).toSet().length > 1;

  static String _place(Session s, List<ProjectNode> projects, bool showAgent) {
    final hit = (s.workspaceId?.isNotEmpty ?? false)
        ? lookupWorkspace(projects, s.workspaceId!)
        : null;
    final where = hit != null
        ? '${hit.$1.name} / ${hit.$2.name}'
        : (s.repo ?? '');
    final agent = showAgent ? agentLabel(s.agent) : '';
    return [where, agent].where((p) => p.isNotEmpty).join(' · ');
  }
}

class _SessionRow extends StatelessWidget {
  const _SessionRow({
    required this.session,
    required this.place,
    required this.selected,
    required this.onTap,
    required this.onLongPress,
  });

  final Session session;
  final String place;
  final bool selected;
  final VoidCallback onTap;
  final VoidCallback onLongPress;

  @override
  Widget build(BuildContext context) {
    final s = session;
    final dim = s.offline;
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 1),
      child: Material(
        color: selected ? AppColors.border : Colors.transparent,
        borderRadius: BorderRadius.circular(8),
        clipBehavior: Clip.antiAlias,
        child: InkWell(
          onTap: onTap,
          onLongPress: onLongPress,
          child: Padding(
            padding: const EdgeInsets.fromLTRB(8, 6, 8, 6),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                SizedBox(
                  width: 24,
                  child: Text(
                    dim ? '○' : statusGlyph(s.status),
                    textAlign: TextAlign.center,
                    style: TextStyle(
                      fontFamily: 'monospace',
                      color: dim ? AppColors.border : statusColor(s.status),
                    ),
                  ),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        sidebarTitle(s),
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: TextStyle(
                          fontWeight: selected
                              ? FontWeight.w700
                              : FontWeight.w500,
                          color: dim ? AppColors.dim : null,
                        ),
                      ),
                      if (place.isNotEmpty)
                        Text(
                          place,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: const TextStyle(
                            color: AppColors.dim,
                            fontSize: 12,
                          ),
                        ),
                    ],
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
