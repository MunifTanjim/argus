import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/pagent.dart';
import '../state/agent_sections.dart';
import '../state/left_sidebar.dart';
import '../state/pagents.dart';
import 'agent_screen.dart';
import 'pagent_style.dart';
import 'project_actions.dart';
import 'theme.dart';

/// The left sidebar's Agents tab: the persistent agents by section.
class AgentsSidebarList extends ConsumerWidget {
  const AgentsSidebarList({super.key, required this.onOpen});

  /// Runs after a tap opens an agent (closes a modal drawer).
  final VoidCallback onOpen;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(pagentsProvider);
    final query = ref.watch(sidebarAgentsFilterProvider).trim();
    final groups = buildAgentSections(state, query);
    Future<void> refresh() =>
        ref.read(pagentsProvider.notifier).load(ref.read(pagentsClientProvider));
    if (groups.isEmpty) {
      return RefreshIndicator(
        onRefresh: refresh,
        child: ListView(
          children: [
            const SizedBox(height: 120),
            Center(
              child: Text(
                query.isNotEmpty ? 'no agents match "$query"' : 'no agents',
                style: const TextStyle(color: AppColors.dim),
              ),
            ),
          ],
        ),
      );
    }
    return RefreshIndicator(
      onRefresh: refresh,
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
            for (final a in g.agents)
              _AgentRow(
                agent: a,
                place: agentPlace(state, a),
                unread: state.unread(a.id),
                onTap: () {
                  final ax = ActionContext.of(context);
                  onOpen();
                  ax.navigator.push(agentRoute(a.id));
                },
              ),
          ],
        ],
      ),
    );
  }
}

class _AgentRow extends StatelessWidget {
  const _AgentRow({
    required this.agent,
    required this.place,
    required this.unread,
    required this.onTap,
  });

  final PAgent agent;
  final String place;
  final bool unread;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final (glyph, color) = agentGlyph(agent.status);
    final dim = agent.unreachable;
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 1),
      child: Material(
        color: Colors.transparent,
        borderRadius: BorderRadius.circular(8),
        clipBehavior: Clip.antiAlias,
        child: InkWell(
          onTap: onTap,
          child: Padding(
            padding: const EdgeInsets.fromLTRB(8, 6, 8, 6),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                SizedBox(
                  width: 24,
                  child: Text(
                    glyph,
                    textAlign: TextAlign.center,
                    style: TextStyle(fontFamily: 'monospace', color: color),
                  ),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Row(
                        children: [
                          Flexible(
                            child: Text(
                              agent.name,
                              maxLines: 1,
                              overflow: TextOverflow.ellipsis,
                              style: TextStyle(
                                fontWeight: FontWeight.w500,
                                color: dim ? AppColors.dim : null,
                              ),
                            ),
                          ),
                          if (unread) const UnreadDot(),
                        ],
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
