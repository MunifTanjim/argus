import 'package:flutter/material.dart';

import 'theme.dart';

String fmtTokens(int n) {
  if (n >= 1000000) return '${(n / 1000000).toStringAsFixed(1)}M';
  return n >= 1000 ? '${(n / 1000).toStringAsFixed(1)}k' : '$n';
}

String fmtPct(double pct) => '${pct.round()}%';

/// A context-window usage percentage as `42% ctx`.
String fmtContextPct(double pct) => '${fmtPct(pct)} ctx';

/// Mirrors the TUI thresholds.
Color contextColor(double pct, ArgusTokens t) {
  if (pct >= 80) return t.error;
  if (pct >= 50) return t.warning;
  return t.success;
}

/// A model's own color, readable on the palette; dim when unknown.
Color modelColor(String? hex, ArgusTokens t) =>
    readableOn(hexColor(hex) ?? t.textDim, t);

/// A `#rrggbb` color; the leading `#` is optional.
Color? hexColor(String? hex) {
  if (hex == null) return null;
  final h = hex.startsWith('#') ? hex.substring(1) : hex;
  if (h.length != 6) return null;
  final v = int.tryParse(h, radix: 16);
  // The mask applies full alpha to the parsed user hex.
  return v == null ? null : Color(0xFF000000 | v);
}

/// [parts] joined by a dim [sep], each part in its own color.
TextSpan metaSpan(
  Iterable<(String, Color)> parts,
  ArgusTokens t, {
  String sep = ' · ',
}) {
  final dim = TextStyle(color: t.textDim);
  return TextSpan(
    children: [
      for (final (i, (text, color)) in parts.indexed) ...[
        if (i > 0) TextSpan(text: sep, style: dim),
        TextSpan(
          text: text,
          style: TextStyle(color: color),
        ),
      ],
    ],
  );
}
