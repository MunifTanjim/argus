import 'package:flutter/material.dart';

/// [span]'s size on one line, as [context] draws text, with [style] merged
/// over the default text style.
Size measureText(BuildContext context, InlineSpan span, [TextStyle? style]) {
  final p = TextPainter(
    text: TextSpan(
      style: DefaultTextStyle.of(context).style.merge(style),
      children: [span],
    ),
    maxLines: 1,
    textDirection: Directionality.of(context),
    textScaler: MediaQuery.textScalerOf(context),
  )..layout();
  final size = p.size;
  p.dispose();
  return size;
}
