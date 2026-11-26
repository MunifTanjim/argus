import 'package:argus/ui/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// WCAG minimums: 4.5 for body text, 3.0 for dim text and status marks.
const _bodyText = 4.5;
const _uiMark = 3.0;

/// Known failures, as 'palette token background'. Most are upstream colors.
/// Remove an entry when its pair passes; the test fails on a stale entry.
const _allowed = {
  // Upstream comment gray and float-border gray.
  'Kanagawa Lotus textDim canvas',
  'Kanagawa Lotus textDim surface',
  'Kanagawa Lotus textSecondary canvas',
  'Kanagawa Lotus textSecondary surface',
  // Upstream nord11 and the brightened nord3 comment color.
  'Nord error surface',
  'Nord textDim canvas',
  'Nord textDim surface',
  // Solarized is low contrast by design.
  'Solarized Dark error surface',
  'Solarized Dark textDim canvas',
  'Solarized Dark textDim surface',
  'Solarized Dark textSecondary surface',
  'Solarized Light text surface',
  'Solarized Light textDim canvas',
  'Solarized Light textDim surface',
  'Solarized Light textSecondary canvas',
  'Solarized Light textSecondary surface',
  // Upstream fg is 4.52 on bg, and fg_dark is lower.
  'Tokyo Night Day text surface',
  'Tokyo Night Day textSecondary canvas',
  'Tokyo Night Day textSecondary surface',
};

/// The checked pairs of one palette: key, ratio, minimum.
Iterable<(String, double, double)> _pairs(ArgusPalette p) sync* {
  final t = ArgusTokens.from(p);
  final floors = {
    'text': (t.text, _bodyText),
    'textSecondary': (t.textSecondary, _bodyText),
    'textDim': (t.textDim, _uiMark),
    'blocked': (t.blocked, _uiMark),
    'waiting': (t.waiting, _uiMark),
    'working': (t.working, _uiMark),
    'error': (t.error, _uiMark),
  };
  final backgrounds = {'canvas': t.canvas, 'surface': t.surface};
  for (final MapEntry(key: token, value: (fg, floor)) in floors.entries) {
    for (final MapEntry(key: bg, value: bgColor) in backgrounds.entries) {
      yield ('${p.name} $token $bg', contrastRatio(fg, bgColor), floor);
    }
  }
}

void main() {
  final palettes = [
    for (final id in ArgusPaletteId.values)
      for (final b in Brightness.values) paletteFor(id, b),
  ];

  for (final p in palettes) {
    test('${p.name}: text and status colors meet WCAG contrast', () {
      final failures = <String>[];
      final stale = <String>[];
      for (final (key, ratio, floor) in _pairs(p)) {
        final ok = ratio >= floor;
        final allowed = _allowed.contains(key);
        if (!ok && !allowed) {
          failures.add('$key: ${ratio.toStringAsFixed(2)} < $floor');
        } else if (ok && allowed) {
          stale.add('$key: ${ratio.toStringAsFixed(2)} passes now');
        }
      }
      expect(failures, isEmpty, reason: 'Contrast below the minimum.');
      expect(stale, isEmpty, reason: 'Remove these entries from _allowed.');
    });
  }

  test('every allowed entry names a checked pair', () {
    final keys = {
      for (final p in palettes) for (final (key, _, _) in _pairs(p)) key,
    };
    expect(_allowed.difference(keys), isEmpty);
  });
}
