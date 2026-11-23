import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/bot.dart';
import '../state/bot_sections.dart';
import '../state/left_sidebar.dart';
import '../state/bots.dart';
import 'bot_screen.dart';
import 'bot_style.dart';
import 'project_actions.dart';
import 'theme.dart';

/// The left sidebar's Bots tab: the bots by section.
class BotsSidebarList extends ConsumerWidget {
  const BotsSidebarList({super.key, required this.onOpen});

  /// Runs after a tap opens a bot (closes a modal drawer).
  final VoidCallback onOpen;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(botsProvider);
    final query = ref.watch(sidebarBotsFilterProvider).trim();
    final groups = buildBotSections(state, query);
    Future<void> refresh() =>
        ref.read(botsProvider.notifier).load(ref.read(botsClientProvider));
    if (groups.isEmpty) {
      return RefreshIndicator(
        onRefresh: refresh,
        child: ListView(
          children: [
            const SizedBox(height: 120),
            Center(
              child: Text(
                query.isNotEmpty ? 'no bots match "$query"' : 'no bots',
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
            for (final a in g.bots)
              _BotRow(
                bot: a,
                place: botPlace(state, a),
                unread: state.unread(a.id),
                onTap: () {
                  final ax = ActionContext.of(context);
                  onOpen();
                  ax.navigator.push(botRoute(a.id));
                },
              ),
          ],
        ],
      ),
    );
  }
}

class _BotRow extends StatelessWidget {
  const _BotRow({
    required this.bot,
    required this.place,
    required this.unread,
    required this.onTap,
  });

  final Bot bot;
  final String place;
  final bool unread;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final (glyph, color) = botGlyph(bot.status);
    final dim = bot.unreachable;
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
                              bot.name,
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
