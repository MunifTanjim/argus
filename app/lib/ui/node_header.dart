import 'package:flutter/material.dart';

import '../models/enums.dart';
import 'status_style.dart';
import 'theme.dart';

class SectionLabel extends StatelessWidget {
  const SectionLabel({
    super.key,
    required this.leading,
    required this.label,
    this.color = AppColors.dim,
  });

  final Widget leading;
  final String label;
  final Color color;

  @override
  Widget build(BuildContext context) => Row(
    children: [
      SizedBox(width: 14, child: Center(child: leading)),
      const SizedBox(width: 6),
      Expanded(
        child: Text(
          label.toUpperCase(),
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: TextStyle(
            color: color,
            fontSize: 11,
            fontWeight: FontWeight.w600,
            letterSpacing: 0.8,
          ),
        ),
      ),
    ],
  );
}

class NodeHeader extends StatelessWidget {
  const NodeHeader({super.key, required this.label, this.offline = false});

  final String label;
  final bool offline;

  @override
  Widget build(BuildContext context) => SectionLabel(
    leading: const Icon(Icons.dns_outlined, size: 14, color: AppColors.dim),
    label: offline ? '$label (offline)' : label,
  );
}

class NeedsYouHeader extends StatelessWidget {
  const NeedsYouHeader({super.key, required this.label});

  final String label;

  @override
  Widget build(BuildContext context) => SectionLabel(
    leading: Text(
      statusGlyph(SessionStatus.awaitingInput),
      style: TextStyle(
        fontFamily: 'monospace',
        fontSize: 12,
        color: statusColor(SessionStatus.awaitingInput),
      ),
    ),
    label: label,
    color: AppColors.accent,
  );
}
