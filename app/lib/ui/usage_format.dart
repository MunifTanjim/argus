import 'package:flutter/material.dart';

import 'theme.dart';

String fmtTokens(int n) {
  if (n >= 1000000) return '${(n / 1000000).toStringAsFixed(1)}M';
  return n >= 1000 ? '${(n / 1000).toStringAsFixed(1)}k' : '$n';
}

String fmtPct(double pct) => '${pct.round()}%';

/// A context-window usage percentage as `ctx 42%`.
String fmtContextPct(double pct) => 'ctx ${fmtPct(pct)}';

/// Mirrors the TUI thresholds.
Color contextColor(double pct, ArgusTokens t) {
  if (pct >= 80) return t.error;
  if (pct >= 50) return t.warning;
  return t.success;
}

Color? hexColor(String? hex) {
  if (hex == null || hex.length != 7 || !hex.startsWith('#')) return null;
  final v = int.tryParse(hex.substring(1), radix: 16);
  // The mask applies full alpha to the parsed user hex.
  return v == null ? null : Color(0xFF000000 | v);
}
