import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/session.dart';
import '../state/grouping.dart';
import '../state/session_filter.dart';
import '../transport/connection.dart';
import 'node_header.dart';
import 'responsive.dart';
import 'session_card.dart';
import 'session_detail_screen.dart';
import 'theme.dart';

/// Sessions grouped into "Needs you" and per-host sections, as session cards.
class SessionSectionsList extends ConsumerWidget {
  const SessionSectionsList({
    super.key,
    required this.sessions,
    this.emptyText = 'No sessions.',
  });

  final Iterable<Session> sessions;
  final String emptyText;

  // Tearing a session down lives on the detail screen's "Kill Session" action,
  // so the list is tap-to-open only.
  Widget _buildCard(
    BuildContext context,
    Session s,
    SessionSection section,
    bool grouped,
    bool multiAgent,
  ) => SessionCard(
    session: s,
    showNode: section.needsYou && grouped,
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
    final sections = buildSections(shown);
    // When sessions span nodes, the "Needs you" section mixes hosts under one
    // header, so its cards must name their own node.
    final grouped = nodesFromSessions(sessions).isNotEmpty;
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
                child: _buildCard(context, s, section, grouped, multiAgent),
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
      child: section.needsYou
          ? NeedsYouHeader(label: section.title)
          : NodeHeader(label: section.title, offline: section.offline),
    );
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
