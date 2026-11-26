import 'package:flutter/material.dart';

import 'card_layout.dart';
import 'node_header.dart' show sectionLabelStyle;
import 'theme.dart';

/// A foldable group header in the history lists: an optional icon, the label
/// in the section style, then the group's row count and the fold chevron at the
/// right. With no icon, the label lines up with the rows below.
class HistoryGroupHeader extends StatelessWidget {
  const HistoryGroupHeader({
    super.key,
    required this.label,
    required this.count,
    required this.folded,
    required this.onTap,
    this.icon,
  });

  final IconData? icon;

  final String label;
  final int count;
  final bool folded;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final style = sectionLabelStyle(t.textDim);
    // The gap above the group stays outside the tap area, so the label sits
    // in the middle of what a tap highlights.
    return Padding(
      padding: const EdgeInsets.only(top: 10),
      child: InkWell(
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 6),
          child: Row(
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
              Text('$count', style: style),
              const SizedBox(width: 4),
              Icon(
                folded ? Icons.chevron_right : Icons.expand_more,
                size: 16,
                color: t.textDim,
              ),
            ],
          ),
        ),
      ),
    );
  }
}
