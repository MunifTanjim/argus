import 'package:flutter/material.dart';

String fmtTokens(int n) {
  if (n >= 1000000) return '${(n / 1000000).toStringAsFixed(1)}M';
  return n >= 1000 ? '${(n / 1000).toStringAsFixed(1)}k' : '$n';
}

String fmtPct(double pct) => '${pct.round()}%';

/// A context-window usage percentage as `ctx 42%`.
String fmtContextPct(double pct) => 'ctx ${fmtPct(pct)}';

/// Mirrors the TUI thresholds.
Color contextColor(double pct) {
  if (pct >= 80) return const Color(0xFFfb4934); // red
  if (pct >= 50) return const Color(0xFFfabd2f); // yellow
  return const Color(0xFFb8bb26); // green
}

Color? hexColor(String? hex) {
  if (hex == null || hex.length != 7 || !hex.startsWith('#')) return null;
  final v = int.tryParse(hex.substring(1), radix: 16);
  return v == null ? null : Color(0xFF000000 | v);
}
