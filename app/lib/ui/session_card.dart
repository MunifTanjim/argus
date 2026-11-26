import 'package:flutter/material.dart';

import '../models/enums.dart';
import '../models/session.dart';
import 'agent_badge.dart';
import 'card_layout.dart';
import 'relative_time.dart';
import 'status_style.dart';
import 'theme.dart';
import 'usage_format.dart';

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
    if (context.tokens.statusStyle != StatusStyle.filled) {
      return _buildQuiet(context);
    }
    final s = session;
    final (surface, border) = s.blocked
        ? (context.tokens.blockedSurface, context.tokens.blockedBorder)
        : s.status == SessionStatus.awaitingInput
            ? (context.tokens.waitingSurface, context.tokens.waitingBorder)
            : (context.tokens.surface, context.tokens.line);
    final nodeLabel = s.nodeLabel ?? s.nodeId ?? 'local';


    final card = Container(
      decoration: BoxDecoration(
        color: surface,
        border: Border.all(color: border),
        borderRadius: BorderRadius.circular(context.tokens.radius),
      ),
      // The border is 1 px wide and sits inside the inset.
      padding: const EdgeInsets.fromLTRB(kCardInset - 1, 12, 12, 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SessionHeader(
            session: s,
            mark: Text(statusGlyph(s.status),
                textAlign: TextAlign.center,
                style: TextStyle(
                    fontFamily: monoFamily,
                    color: sessionColor(s, context.tokens))),
            style: TextStyle(
                color: context.tokens.text,
                fontSize: 14,
                fontWeight: FontWeight.w600),
            trailing: [
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
          if ((s.summary?.task ?? s.interaction?.message) != null) ...[
            const SizedBox(height: 4),
            Text(s.summary?.task ?? s.interaction!.message!,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(color: context.tokens.textSecondary, fontSize: 13)),
          ],
          if (sessionMetaParts(s, context.tokens, agent: false).isNotEmpty) ...[
            const SizedBox(height: 6),
            _metaLine(s, context.tokens,
                agent: false, sep: '  ·  ', style: mono),
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

  Widget _buildQuiet(BuildContext context) {
    final s = session;
    final t = context.tokens;
    final style = t.statusStyle;
    final color = sessionColor(s, t);
    final dim = s.status == SessionStatus.idle ||
        s.status == SessionStatus.dead ||
        s.status == SessionStatus.discovered;
    final vPad = t.density == AppDensity.compact ? 9.0 : 13.0;
    final time = relativeTime(s.summary?.lastActivity ??
        s.statusSince?.toIso8601String());
    final task = s.summary?.task ?? (s.blocked ? null : s.interaction?.message);
    final nodeLabel = s.nodeLabel ?? s.nodeId ?? 'local';

    final body = Padding(
      // The stripe takes 3 px of the inset.
      padding: EdgeInsets.fromLTRB(
          style == StatusStyle.stripe ? kCardInset - 3 : kCardInset,
          vPad,
          12,
          vPad),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SessionHeader(
            session: s,
            mark: style == StatusStyle.dot
                ? Center(
                    child: Container(
                      key: const Key('status-dot'),
                      width: 7,
                      height: 7,
                      decoration:
                          BoxDecoration(color: color, shape: BoxShape.circle),
                    ),
                  )
                : Text(statusGlyph(s.status),
                    textAlign: TextAlign.center,
                    style: TextStyle(fontFamily: monoFamily, color: color)),
            style: mono.copyWith(
                color: dim ? t.textDim : t.text,
                fontSize: 13,
                fontWeight: FontWeight.w500),
            trailing: [
              if (showAgent) ...[
                const SizedBox(width: 8),
                AgentBadge(agent: s.agent),
              ],
              if (showNode) ...[
                const SizedBox(width: 8),
                Icon(Icons.dns_outlined, size: 13, color: t.textDim),
                const SizedBox(width: 4),
                Text(nodeLabel, style: mono.copyWith(color: t.textDim, fontSize: 11)),
              ],
              if (time.isNotEmpty) ...[
                const SizedBox(width: 8),
                Text(time, style: mono.copyWith(color: t.textDim, fontSize: 11)),
              ],
            ],
          ),
          if (task != null) ...[
            const SizedBox(height: 3),
            Text(task,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(color: t.textDim, fontSize: 12)),
          ],
          const SizedBox(height: 3),
          _metaLine(s, t,
              agent: true, sep: ' · ', style: mono.copyWith(fontSize: 11)),
          if (s.blocked && s.interaction?.message != null) ...[
            const SizedBox(height: 8),
            Container(
              key: const Key('blocked-prompt'),
              width: double.infinity,
              padding: const EdgeInsets.fromLTRB(10, 6, 10, 6),
              decoration: BoxDecoration(
                color: Color.alphaBlend(t.blocked.withValues(alpha: 0.1), t.canvas),
                border: Border(left: BorderSide(color: t.blocked, width: 2)),
              ),
              child: Text(s.interaction!.message!,
                  maxLines: 3,
                  overflow: TextOverflow.ellipsis,
                  style: mono.copyWith(color: t.text, fontSize: 12)),
            ),
          ],
        ],
      ),
    );

    final row = style == StatusStyle.stripe
        ? IntrinsicHeight(
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Container(key: const Key('status-stripe'), width: 3, color: color),
                Expanded(child: body),
              ],
            ),
          )
        : body;

    // Rows sit on the surface color so they stand out from the canvas. Rows
    // with a gap between them are rounded; touching rows stay square.
    final radius = t.rowGap == 0 ? null : BorderRadius.circular(t.radius);
    return Opacity(
      opacity: s.offline ? 0.5 : 1,
      child: Material(
        color: t.surface,
        borderRadius: radius,
        clipBehavior: radius == null ? Clip.none : Clip.antiAlias,
        child: InkWell(
          onTap: onTap,
          onLongPress: onLongPress,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              row,
              if (t.rowGap == 0) Divider(height: 1, thickness: 1, color: t.line),
            ],
          ),
        ),
      ),
    );
  }
}

/// The parts of a card's meta line, each with its color: the model in its own
/// color (readable on the palette), the context in its threshold color, and
/// the rest dim. Quiet rows lead with the agent.
List<(String, Color)> sessionMetaParts(Session s, ArgusTokens t,
    {required bool agent}) {
  final sum = s.summary;
  return [
    if (agent) (agentLabel(s.agent), t.textDim),
    if (sum?.modelName != null)
      (
        sum!.modelName!,
        readableOn(hexColor(sum.modelColor) ?? t.textDim, t),
      ),
    if (sum?.hasContext ?? false)
      (fmtContextPct(sum!.contextPct), contextColor(sum.contextPct, t)),
    if ((sum?.tokens ?? 0) > 0) ('${sum!.tokens} tok', t.textDim),
  ];
}

Widget _metaLine(Session s, ArgusTokens t,
    {required bool agent, required String sep, required TextStyle style}) {
  final parts = sessionMetaParts(s, t, agent: agent);
  return Text.rich(
    TextSpan(
      style: style.copyWith(color: t.textDim),
      children: [
        for (final (i, (text, color)) in parts.indexed) ...[
          if (i > 0) TextSpan(text: sep),
          TextSpan(text: text, style: TextStyle(color: color)),
        ],
      ],
    ),
    maxLines: 1,
    overflow: TextOverflow.ellipsis,
  );
}

/// A card's header. The first line holds the status [mark], then the repo
/// as a small dim label (or, for an unnamed session, the title), then
/// [trailing] (node, time). A named session's name is the title on the second
/// line, at the card's left edge like the body below it.
class SessionHeader extends StatelessWidget {
  const SessionHeader({
    super.key,
    required this.session,
    required this.mark,
    required this.style,
    this.trailing = const [],
  });

  final Session session;
  final Widget mark;

  /// The style of the main title; the label above it is mono, 11, dim.
  final TextStyle style;
  final List<Widget> trailing;

  @override
  Widget build(BuildContext context) {
    final name = session.displayName;
    final first = Row(
      children: [
        SizedBox(width: kLeadWidth, child: mark),
        const SizedBox(width: kLeadGap),
        Expanded(
          child: name == null
              ? Text(session.displayTitle,
                  maxLines: 1, overflow: TextOverflow.ellipsis, style: style)
              : Text(session.displayTitle,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: mono.copyWith(
                      color: context.tokens.textDim, fontSize: 11)),
        ),
        ...trailing,
      ],
    );
    if (name == null) return first;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      mainAxisSize: MainAxisSize.min,
      children: [
        first,
        Text(name, maxLines: 1, overflow: TextOverflow.ellipsis, style: style),
      ],
    );
  }
}
