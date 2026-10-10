import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/project.dart';
import '../models/session.dart';
import '../state/grouping.dart';
import '../state/projects.dart';
import '../state/session_filter.dart';
import '../transport/connection.dart';
import 'agent_badge.dart';
import 'nerd_icon.dart';
import 'node_header.dart';
import 'responsive.dart';
import 'session_card.dart';
import 'session_detail_screen.dart';
import 'status_style.dart';
import 'theme.dart';

/// Sessions grouped into "Needs you" and per-host sections, as session cards.
class SessionSectionsList extends ConsumerWidget {
  const SessionSectionsList({
    super.key,
    required this.sessions,
    this.emptyText = 'No sessions.',
    this.groupBy = GroupBy.host,
  });

  final Iterable<Session> sessions;
  final String emptyText;

  /// The home list's grouping; workspace lists keep host.
  final GroupBy groupBy;

  // Tearing a session down lives on the detail screen's "Kill Session" action,
  // so the list is tap-to-open only.
  Widget _buildCard(
    BuildContext context,
    Session s,
    SessionSection section,
    bool multiAgent,
  ) => SessionCard(
    session: s,
    showNode: section.showNode,
    showAgent: multiAgent,
    onTap: () => Navigator.of(context).push(sessionDetailRoute(s)),
  );

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final activeOnly = ref.watch(activeOnlyProvider);
    final query = ref.watch(sessionSearchProvider) ?? '';
    final shown = sessions
        .where((s) => !activeOnly || isActiveSession(s))
        .where((s) => matchesSessionQuery(s, query))
        .toList();
    final projects = groupBy == GroupBy.project || groupBy == GroupBy.workspace
        ? ref.watch(projectsProvider).projects
        : const <ProjectNode>[];
    final sections = buildSections(shown, by: groupBy, projects: projects);
    final multiAgent =
        sessions.map((s) => s.agent).where((a) => a.isNotEmpty).toSet().length >
        1;
    if (sections.isEmpty) {
      return ListView(
        children: [
          const SizedBox(height: 120),
          Center(
            child: Text(switch (sessions.isNotEmpty) {
              true when query.isNotEmpty => 'No sessions match.',
              true when activeOnly => 'No active sessions.',
              _ => emptyText,
            }, style: const TextStyle(color: AppColors.dim)),
          ),
        ],
      );
    }
    return CenteredBody(
      child: ListView(
        padding: const EdgeInsets.all(12),
        children: [
          for (final section in sections) ...[
            _SectionHeader(section: section),
            for (final s in section.sessions)
              Padding(
                padding: const EdgeInsets.only(bottom: 8),
                child: _buildCard(context, s, section, multiAgent),
              ),
            const SizedBox(height: 8),
          ],
        ],
      ),
    );
  }
}

class ActiveOnlyButton extends ConsumerWidget {
  const ActiveOnlyButton({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final activeOnly = ref.watch(activeOnlyProvider);
    return IconButton(
      isSelected: activeOnly,
      icon: const Icon(Icons.filter_list),
      selectedIcon: const Icon(Icons.filter_list_alt),
      tooltip: activeOnly ? 'Show all sessions' : 'Show active sessions',
      onPressed: () => ref.read(activeOnlyProvider.notifier).toggle(),
    );
  }
}

class GroupByButton extends ConsumerWidget {
  const GroupByButton({super.key});

  static const _labels = {
    GroupBy.host: 'Host',
    GroupBy.project: 'Project',
    GroupBy.workspace: 'Workspace',
    GroupBy.agent: 'Agent',
    GroupBy.status: 'Status',
  };

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final current = ref.watch(groupByProvider);
    return PopupMenuButton<GroupBy>(
      icon: const Icon(Icons.view_agenda_outlined),
      tooltip: 'Group sessions',
      initialValue: current,
      onSelected: (g) => ref.read(groupByProvider.notifier).set(g),
      itemBuilder: (_) => [
        for (final g in GroupBy.values)
          CheckedPopupMenuItem(
            value: g,
            checked: g == current,
            child: Text(_labels[g]!),
          ),
      ],
    );
  }
}

class SessionSearchButton extends ConsumerWidget {
  const SessionSearchButton({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (ref.watch(sessionSearchProvider) != null) {
      return const SizedBox.shrink();
    }
    return IconButton(
      icon: const Icon(Icons.search),
      tooltip: 'Filter sessions',
      onPressed: () => ref.read(sessionSearchProvider.notifier).open(),
    );
  }
}

/// An app bar title that the session search field replaces while it is open.
class SessionSearchTitle extends ConsumerStatefulWidget {
  const SessionSearchTitle({super.key, required this.child});

  final Widget child;

  @override
  ConsumerState<SessionSearchTitle> createState() => _SessionSearchTitleState();
}

class _SessionSearchTitleState extends ConsumerState<SessionSearchTitle> {
  final _controller = TextEditingController();

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final query = ref.watch(sessionSearchProvider);
    if (query == null) {
      _controller.clear();
      return widget.child;
    }
    final search = ref.read(sessionSearchProvider.notifier);
    return TextField(
      controller: _controller,
      autofocus: true,
      decoration: InputDecoration(
        hintText: 'Filter sessions',
        border: InputBorder.none,
        suffixIcon: IconButton(
          icon: const Icon(Icons.close),
          tooltip: 'Clear filter',
          onPressed: search.close,
        ),
      ),
      onChanged: search.set,
    );
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader({required this.section});
  final SessionSection section;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 8, top: 4),
      child: switch (section.icon) {
        SectionIcon.needsYou => NeedsYouHeader(label: section.title),
        SectionIcon.host => NodeHeader(
          label: section.title,
          offline: section.offline,
        ),
        _ => SectionLabel(
          leading: _leading(),
          label: section.offline
              ? '${section.title} (offline)'
              : section.title,
        ),
      },
    );
  }

  // Sections of one agent or status share it, so the first session tells.
  Widget _leading() {
    final first = section.sessions.first;
    Color tint(Color c) => section.other ? AppColors.dim : c;
    return switch (section.icon) {
      SectionIcon.project => const RepoIcon(size: 14, color: AppColors.dim),
      SectionIcon.folder => const Icon(
        Icons.folder_outlined,
        size: 14,
        color: AppColors.dim,
      ),
      SectionIcon.branch => const GitBranchIcon(size: 14, color: AppColors.dim),
      SectionIcon.agent => Icon(
        Icons.smart_toy_outlined,
        size: 14,
        color: tint(agentColor(first.agent)),
      ),
      SectionIcon.status => Text(
        statusGlyph(first.status),
        style: TextStyle(
          fontFamily: 'monospace',
          fontSize: 12,
          color: statusColor(first.status),
        ),
      ),
      SectionIcon.needsYou || SectionIcon.host => const SizedBox.shrink(),
    };
  }
}

class ReconnectBanner extends StatelessWidget {
  const ReconnectBanner({super.key, required this.state, this.message});
  final ConnState state;
  final String? message;

  @override
  Widget build(BuildContext context) {
    final failed = state == ConnState.failed;
    final text = switch (state) {
      ConnState.connecting => 'Connecting…',
      ConnState.reconnecting => 'Reconnecting…',
      ConnState.disconnected => 'Disconnected',
      ConnState.connected => 'Connected',
      // A fatal error (e.g. changed host key): show the actionable message, not
      // a generic label — this is the moment the pin is protecting the user.
      ConnState.failed => message ?? 'Connection failed',
    };
    return Container(
      width: double.infinity,
      color: failed ? AppColors.errorSurface : AppColors.awaitingSurface,
      padding: const EdgeInsets.symmetric(vertical: 6, horizontal: 12),
      child: Text(
        text,
        style: TextStyle(
          color: failed ? AppColors.error : AppColors.secondary,
          fontSize: 12,
        ),
      ),
    );
  }
}

class UnauthorizedBanner extends StatelessWidget {
  const UnauthorizedBanner({super.key, required this.onTap});
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return Material(
      color: AppColors.errorSurface,
      child: InkWell(
        onTap: onTap,
        child: const Padding(
          padding: EdgeInsets.symmetric(vertical: 6, horizontal: 12),
          child: Row(
            children: [
              Expanded(
                child: Text(
                  'Device not authorized',
                  style: TextStyle(color: AppColors.error, fontSize: 12),
                ),
              ),
              Icon(Icons.chevron_right, size: 16, color: AppColors.error),
            ],
          ),
        ),
      ),
    );
  }
}
