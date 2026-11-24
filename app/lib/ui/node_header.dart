import 'package:flutter/material.dart';

import '../models/enums.dart';
import 'status_style.dart';
import 'theme.dart';

class SectionLabel extends StatelessWidget {
  const SectionLabel({
    super.key,
    required this.leading,
    required this.label,
    this.color,
  });

  final Widget leading;
  final String label;
  final Color? color;

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
            color: color ?? context.tokens.textDim,
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
  const NodeHeader({
    super.key,
    required this.label,
    this.offline = false,
    this.outdated = false,
  });

  final String label;
  final bool offline;

  /// The node runs an argus older than the app's minimum.
  final bool outdated;

  @override
  Widget build(BuildContext context) {
    final marks = [if (offline) 'offline', if (outdated) 'outdated'];
    return SectionLabel(
      leading: Icon(Icons.dns_outlined, size: 14, color: context.tokens.textDim),
      label: marks.isEmpty ? label : '$label (${marks.join(' · ')})',
    );
  }
}

class NeedsYouHeader extends StatelessWidget {
  const NeedsYouHeader({super.key, required this.label, required this.color});

  final String label;

  /// The header color: blocked when any session in the section is.
  final Color color;

  @override
  Widget build(BuildContext context) => SectionLabel(
    leading: Text(
      statusGlyph(SessionStatus.awaitingInput),
      style: TextStyle(
        fontFamily: monoFamily,
        fontSize: 12,
        color: color,
      ),
    ),
    label: label,
    color: color,
  );
}
