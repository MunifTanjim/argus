import 'package:flutter/material.dart';

import '../models/enums.dart';
import '../models/session.dart';
import 'agent_badge.dart';
import 'card_layout.dart';
import 'node_header.dart' show NodeName;
import 'relative_time.dart';
import 'status_style.dart';
import 'theme.dart';
import 'usage_format.dart';

class SessionCard extends StatelessWidget {
  const SessionCard({
    super.key,
    required this.session,
    this.onTap,
    this.onLongPress,
    this.showNode = false,
    this.showAgent = false,
  });

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
    final (surface, border) = s.status == SessionStatus.awaitingInput
        ? (context.tokens.waitingSurface, context.tokens.waitingBorder)
        : (context.tokens.surface, context.tokens.line);
    final meta = sessionMetaParts(s, context.tokens, agent: false);
    return StatusCardFrame(
      session: s,
      color: sessionColor(s, context.tokens),
      surface: surface,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(context.tokens.radius),
        side: BorderSide(color: border),
      ),
      onTap: onTap,
      onLongPress: onLongPress,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SessionHeader(
            session: s,
            mark: StatusMark(
              status: s.status,
              color: sessionColor(s, context.tokens),
            ),
            style: TextStyle(
              color: context.tokens.text,
              fontSize: 14,
              fontWeight: FontWeight.w600,
            ),
            trailing: [
              if (showAgent) ...[
                const SizedBox(width: 8),
                AgentBadge(agent: s.agent),
              ],
              if (showNode) ...cardNode(s, context.tokens),
            ],
          ),
          if ((s.summary?.task ?? s.interaction?.message) != null) ...[
            const SizedBox(height: 4),
            Text(
              s.summary?.task ?? s.interaction!.message!,
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(
                color: context.tokens.textSecondary,
                fontSize: 13,
              ),
            ),
          ],
          if (meta.isNotEmpty) ...[
            const SizedBox(height: 6),
            _metaLine(meta, context.tokens, sep: '  ·  ', style: mono),
          ],
        ],
      ),
    );
  }

  Widget _buildQuiet(BuildContext context) {
    final s = session;
    final t = context.tokens;
    final color = sessionColor(s, t);
    final dim =
        s.status == SessionStatus.idle ||
        s.status == SessionStatus.dead ||
        s.status == SessionStatus.discovered;
    final task = s.summary?.task ?? s.interaction?.message;

    // Cards sit on the surface color so they stand out from the canvas.
    return StatusCardFrame(
      session: s,
      color: color,
      surface: t.surface,
      vPad: 9,
      onTap: onTap,
      onLongPress: onLongPress,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SessionHeader(
            session: s,
            mark: StatusMark(status: s.status, color: color),
            style: mono.copyWith(
              color: dim ? t.textDim : t.text,
              fontSize: 13,
              fontWeight: FontWeight.w500,
            ),
            // No agent badge: the meta line leads with the agent.
            trailing: [
              ...cardTime(s, t),
              if (showNode) ...cardNode(s, t),
            ],
          ),
          if (task != null) ...[
            const SizedBox(height: 3),
            Text(
              task,
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(color: t.textDim, fontSize: 12),
            ),
          ],
          const SizedBox(height: 3),
          _metaLine(
            sessionMetaParts(s, t, agent: true),
            t,
            sep: ' · ',
            style: mono.copyWith(fontSize: 11),
          ),
        ],
      ),
    );
  }
}

/// The time since a card's session last moved, for a card header's trailing
/// slot. Empty when the time is unknown.
List<Widget> cardTime(Session s, ArgusTokens t) {
  final time = relativeTime(
    s.summary?.lastActivity ?? s.statusSince?.toIso8601String(),
  );
  if (time.isEmpty) return const [];
  return [
    const SizedBox(width: 8),
    Text(time, style: mono.copyWith(color: t.textDim, fontSize: 11)),
  ];
}

/// A card session's node with the node icon, for a card header's trailing
/// slot. A long name ellipsizes.
List<Widget> cardNode(Session s, ArgusTokens t) => [
  const SizedBox(width: 8),
  Flexible(child: NodeName(s.nodeLabel ?? s.nodeId ?? 'local')),
];

/// The frame of a session or blocked card in every status style: a [color]
/// stripe in the stripe style, the inset, dimming for an offline session, and
/// the tap target.
class StatusCardFrame extends StatelessWidget {
  const StatusCardFrame({
    super.key,
    required this.session,
    required this.color,
    required this.surface,
    required this.child,
    this.shape,
    this.vPad = 12,
    this.onTap,
    this.onLongPress,
  });

  final Session session;
  final Color color;
  final Color surface;

  /// The card's shape. Null is a rounded rectangle at the theme radius.
  final ShapeBorder? shape;
  final double vPad;
  final VoidCallback? onTap;
  final VoidCallback? onLongPress;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final stripe = t.statusStyle == StatusStyle.stripe;
    final body = Padding(
      // The stripe takes 3 px of the inset.
      padding: EdgeInsets.fromLTRB(
        stripe ? kCardInset - 3 : kCardInset,
        vPad,
        12,
        vPad,
      ),
      child: child,
    );
    return Opacity(
      opacity: session.offline ? 0.5 : 1,
      child: Material(
        color: surface,
        shape: shape,
        borderRadius: shape == null ? BorderRadius.circular(t.radius) : null,
        clipBehavior: Clip.antiAlias,
        child: InkWell(
          onTap: onTap,
          onLongPress: onLongPress,
          // A positioned stripe takes the body's height without the extra
          // layout pass of IntrinsicHeight.
          child: stripe
              ? Stack(
                  children: [
                    Padding(padding: const EdgeInsets.only(left: 3), child: body),
                    Positioned(
                      left: 0,
                      top: 0,
                      bottom: 0,
                      width: 3,
                      child: Container(
                        key: const Key('status-stripe'),
                        color: color,
                      ),
                    ),
                  ],
                )
              : body,
        ),
      ),
    );
  }
}

/// The parts of a card's meta line, each with its color: the model in its own
/// color (readable on the palette), the context in its threshold color, and
/// the rest dim. Quiet rows lead with the agent.
List<(String, Color)> sessionMetaParts(
  Session s,
  ArgusTokens t, {
  required bool agent,
}) {
  final sum = s.summary;
  return [
    if (agent) (agentLabel(s.agent), t.textDim),
    if (sum?.modelName != null)
      (sum!.modelName!, modelColor(sum.modelColor, t)),
    if (sum?.hasContext ?? false)
      (fmtContextPct(sum!.contextPct), contextColor(sum.contextPct, t)),
    if ((sum?.tokens ?? 0) > 0) ('${fmtTokens(sum!.tokens)} tok', t.textDim),
  ];
}

Widget _metaLine(
  List<(String, Color)> parts,
  ArgusTokens t, {
  required String sep,
  required TextStyle style,
}) {
  return Text.rich(
    TextSpan(
      style: style.copyWith(color: t.textDim),
      children: [metaSpan(parts, t, sep: sep)],
    ),
    maxLines: 1,
    overflow: TextOverflow.ellipsis,
  );
}

/// A card's status mark: the status glyph, in every status style. Shared by
/// session and blocked cards so the marks match.
class StatusMark extends StatelessWidget {
  const StatusMark({
    super.key,
    required this.status,
    required this.color,
    this.fontSize,
    this.glyph,
  });

  final SessionStatus status;
  final Color color;
  final double? fontSize;

  /// Replaces the status glyph, such as the offline mark.
  final String? glyph;

  @override
  Widget build(BuildContext context) => Text(
    glyph ?? statusGlyph(status),
    textAlign: TextAlign.center,
    style: TextStyle(fontFamily: monoFamily, color: color, fontSize: fontSize),
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
          child: Text(
            session.displayTitle,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: name == null
                ? style
                : mono.copyWith(color: context.tokens.textDim, fontSize: 11),
          ),
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
