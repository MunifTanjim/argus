import 'package:flutter/material.dart';

import '../models/enums.dart';
import 'card_layout.dart';
import 'status_style.dart';
import 'theme.dart';

/// The text style of a section label, shared by the foldable group headers.
TextStyle sectionLabelStyle(Color color) => mono.copyWith(
  color: color,
  fontSize: 10,
  fontWeight: FontWeight.w600,
  letterSpacing: 0.8,
);

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
      SizedBox(width: kLeadWidth, child: Center(child: leading)),
      const SizedBox(width: kLeadGap),
      Expanded(
        child: Text(
          label.toUpperCase(),
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: sectionLabelStyle(color ?? context.tokens.textDim),
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

/// A node's name with the node icon, as on the home session cards.
class NodeName extends StatelessWidget {
  const NodeName(this.label, {super.key});

  final String label;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(Icons.dns_outlined, size: 13, color: t.textDim),
        const SizedBox(width: 4),
        Flexible(
          child: Text(
            label,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: mono.copyWith(color: t.textDim, fontSize: 11),
          ),
        ),
      ],
    );
  }
}
