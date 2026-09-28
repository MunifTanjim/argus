import 'package:flutter/material.dart';

import '../models/session.dart';
import '../state/grouping.dart';
import '../transport/connection.dart';
import 'responsive.dart';
import 'session_card.dart';
import 'session_detail_screen.dart';
import 'theme.dart';

/// Sessions grouped into "Needs you" and per-host sections, as session cards.
class SessionSectionsList extends StatelessWidget {
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
  Widget build(BuildContext context) {
    final sections = buildSections(sessions);
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
            child: Text(
              emptyText,
              style: const TextStyle(color: AppColors.dim),
            ),
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

class _SectionHeader extends StatelessWidget {
  const _SectionHeader({required this.section});
  final SessionSection section;

  @override
  Widget build(BuildContext context) {
    final color = section.needsYou ? AppColors.accent : AppColors.dim;
    final label = section.offline
        ? '${section.title} (offline)'
        : section.title;
    return Padding(
      padding: const EdgeInsets.only(bottom: 8, top: 4),
      child: Text(
        '▌ ${label.toUpperCase()}',
        style: TextStyle(
          fontFamily: 'monospace',
          fontSize: 12,
          fontWeight: FontWeight.w700,
          color: color,
        ),
      ),
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
