import 'package:flutter/material.dart';

import '../state/project_tree.dart';
import 'card_layout.dart';
import 'node_header.dart' show sectionLabelStyle;
import 'theme.dart';

/// A foldable group header: an optional icon, the label in the section style,
/// then the row count or activity badges and the fold chevron at the right.
/// Folded shows `›`, open shows `⌄`, like the project rows in the sidebar.
class FoldHeader extends StatelessWidget {
  const FoldHeader({
    super.key,
    required this.label,
    required this.folded,
    required this.onTap,
    this.icon,
    this.count,
    this.activity,
    this.inset = false,
  });

  final String label;
  final bool folded;
  final VoidCallback onTap;

  /// With no icon, the label lines up with the rows below.
  final IconData? icon;

  /// The group's row count.
  final int? count;

  /// Shown while folded, so a folded group still tells what is in it.
  final Activity? activity;

  /// Inset with a rounded highlight, like the sidebar rows. Otherwise the
  /// highlight spans the full width, like the history rows.
  final bool inset;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final style = sectionLabelStyle(t.textDim);
    final a = activity;
    final badges = folded && a != null && a.live > 0 ? ActivityBadges(a) : null;
    final row = Row(
      children: [
        if (icon != null) ...[
          SizedBox(
            width: kLeadWidth,
            child: Center(child: Icon(icon, size: 14, color: t.textDim)),
          ),
          const SizedBox(width: kLeadGap),
        ],
        Expanded(
          child: Text(
            label.toUpperCase(),
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: style,
          ),
        ),
        if (badges != null) ...[badges, const SizedBox(width: 8)],
        if (count != null) ...[
          Text('$count', style: style),
          const SizedBox(width: 4),
        ],
        Icon(
          folded ? Icons.chevron_right : Icons.expand_more,
          size: 16,
          color: t.textDim,
          semanticLabel: folded ? 'expand' : 'collapse',
        ),
      ],
    );
    // The gap above the group stays outside the tap area, so the label sits
    // in the middle of what a tap highlights.
    return Padding(
      padding: inset
          ? const EdgeInsets.fromLTRB(8, 10, 8, 0)
          : const EdgeInsets.only(top: 10),
      child: InkWell(
        borderRadius: inset ? BorderRadius.circular(t.radius) : null,
        onTap: onTap,
        child: inset
            ? ConstrainedBox(
                constraints: const BoxConstraints(minHeight: 32),
                child: Padding(
                  padding: const EdgeInsets.symmetric(horizontal: 8),
                  child: row,
                ),
              )
            : Padding(
                padding: const EdgeInsets.symmetric(
                  horizontal: 16,
                  vertical: 6,
                ),
                child: row,
              ),
      ),
    );
  }
}

/// Session counts by state, as colored pills: blocked, waiting, working, idle.
class ActivityBadges extends StatelessWidget {
  const ActivityBadges(this.activity, {super.key});
  final Activity activity;

  @override
  Widget build(BuildContext context) => Row(
    mainAxisSize: MainAxisSize.min,
    children: [
      if (activity.blocked > 0)
        _badge(context, '${activity.blocked}', context.tokens.blocked),
      if (activity.waiting > 0)
        _badge(context, '${activity.waiting}', context.tokens.waiting),
      if (activity.working > 0)
        _badge(context, '${activity.working}', context.tokens.workingBadge),
      if (activity.idle > 0)
        _badge(context, '${activity.idle}', context.tokens.textDim),
    ],
  );

  Widget _badge(BuildContext context, String text, Color color) => Container(
    margin: const EdgeInsets.only(left: 4),
    padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
    decoration: BoxDecoration(
      color: color,
      borderRadius: BorderRadius.circular(context.tokens.radiusSmall),
    ),
    child: Text(
      text,
      style: TextStyle(
        color: context.tokens.canvas,
        fontSize: 10,
        fontWeight: FontWeight.w700,
      ),
    ),
  );
}
