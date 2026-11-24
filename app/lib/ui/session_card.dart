import 'package:flutter/material.dart';

import '../models/enums.dart';
import '../models/session.dart';
import 'agent_badge.dart';
import 'status_style.dart';
import 'theme.dart';

class SessionCard extends StatelessWidget {
  const SessionCard(
      {super.key,
      required this.session,
      this.onTap,
      this.onLongPress,
      this.showNode = false,
      this.showAgent = false});

  final Session session;
  final VoidCallback? onTap;
  final VoidCallback? onLongPress;

  /// Show the origin node on the card. Used in the cross-host "Needs you"
  /// section, where the per-node header is replaced and the node would
  /// otherwise be unknowable.
  final bool showNode;

  final bool showAgent;

  @override
  Widget build(BuildContext context) {
    final s = session;
    final (surface, border) = s.blocked
        ? (context.tokens.blockedSurface, context.tokens.blockedBorder)
        : s.status == SessionStatus.awaitingInput
            ? (context.tokens.waitingSurface, context.tokens.waitingBorder)
            : (context.tokens.surface, context.tokens.line);
    final title = s.displayTitle;
    final nodeLabel = s.nodeLabel ?? s.nodeId ?? 'local';

    final meta = <String>[
      if (s.summary?.modelName != null) s.summary!.modelName!,
      if (s.summary?.hasContext ?? false)
        '${s.summary!.contextPct.round()}% ctx',
      if ((s.summary?.tokens ?? 0) > 0) '${s.summary!.tokens} tok',
    ].join('  ·  ');

    final card = Container(
      decoration: BoxDecoration(
        color: surface,
        border: Border.all(color: border),
        borderRadius: BorderRadius.circular(context.tokens.radius),
      ),
      padding: const EdgeInsets.all(12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Text(statusGlyph(s.status),
                  style: TextStyle(
                      fontFamily: monoFamily, color: sessionColor(s, context.tokens))),
              const SizedBox(width: 8),
              Expanded(
                child: Text(title,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                        color: context.tokens.text, fontWeight: FontWeight.w600)),
              ),
              if (showAgent) ...[
                const SizedBox(width: 8),
                AgentBadge(agent: s.agent),
              ],
              if (showNode) ...[
                const SizedBox(width: 8),
                Icon(Icons.dns_outlined, size: 13, color: context.tokens.textDim),
                const SizedBox(width: 4),
                Text(nodeLabel,
                    style: mono.copyWith(color: context.tokens.textDim, fontSize: 11)),
              ],
            ],
          ),
          if (s.displayName != null) ...[
            const SizedBox(height: 2),
            Text(s.displayName!,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: mono.copyWith(color: context.tokens.textDim)),
          ],
          if ((s.summary?.task ?? s.interaction?.message) != null) ...[
            const SizedBox(height: 4),
            Text(s.summary?.task ?? s.interaction!.message!,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(color: context.tokens.textSecondary, fontSize: 13)),
          ],
          if (meta.isNotEmpty) ...[
            const SizedBox(height: 6),
            Text(meta, style: mono.copyWith(color: context.tokens.textDim)),
          ],
        ],
      ),
    );

    return Opacity(
      opacity: s.offline ? 0.5 : 1,
      child: Material(
        color: Colors.transparent,
        child: InkWell(
          borderRadius: BorderRadius.circular(context.tokens.radius),
          onTap: onTap,
          onLongPress: onLongPress,
          child: card,
        ),
      ),
    );
  }
}
