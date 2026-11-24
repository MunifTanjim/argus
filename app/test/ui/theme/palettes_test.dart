import 'dart:math' as math;

import 'package:argus/ui/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

const _all = [
  gruvboxDark,
  gruvboxLight,
  catppuccinMocha,
  catppuccinLatte,
  tokyoNight,
  tokyoNightDay,
  nord,
  nordLight,
  solarizedDark,
  solarizedLight,
  kanagawaWave,
  kanagawaLotus,
];

double _contrast(Color a, Color b) {
  final la = a.computeLuminance();
  final lb = b.computeLuminance();
  return (math.max(la, lb) + 0.05) / (math.min(la, lb) + 0.05);
}

void main() {
  test('paletteFor returns every variant', () {
    for (final id in ArgusPaletteId.values) {
      expect(paletteFor(id, Brightness.dark).brightness, Brightness.dark);
      expect(paletteFor(id, Brightness.light).brightness, Brightness.light);
    }
    expect(paletteFor(ArgusPaletteId.gruvbox, Brightness.dark), gruvboxDark);
    expect(paletteFor(ArgusPaletteId.nord, Brightness.light), nordLight);
  });

  test('every palette has 16 ANSI colors', () {
    for (final p in _all) {
      expect(p.ansi, hasLength(16), reason: p.name);
    }
  });

  test('spot-check upstream values', () {
    expect(catppuccinMocha.bg0, const Color(0xFF1e1e2e));
    expect(catppuccinLatte.bg0, const Color(0xFFeff1f5));
    expect(tokyoNight.bg0, const Color(0xFF1a1b26));
    expect(nord.bg0, const Color(0xFF2e3440));
    expect(solarizedDark.bg0, const Color(0xFF002b36));
    expect(solarizedLight.bg0, const Color(0xFFfdf6e3));
    expect(gruvboxLight.bg0, const Color(0xFFf9f5d7));
    expect(kanagawaWave.bg0, const Color(0xFF1f1f28));
    expect(kanagawaLotus.bg0, const Color(0xFFf2ecbc));
  });

  test('spot-check upstream terminal colors', () {
    expect(tokyoNight.ansi[9], const Color(0xFFff899d));
    expect(tokyoNightDay.ansi[0], const Color(0xFFb4b5b9));
    expect(tokyoNightDay.ansi[14], const Color(0xFF007ea8));
    expect(catppuccinMocha.ansi[7], const Color(0xFFa6adc8));
    expect(catppuccinMocha.ansi[15], const Color(0xFFbac2de));
    expect(catppuccinLatte.ansi[12], const Color(0xFF456eff));
  });

  test('nord light terminal white is visible on its background', () {
    expect(nordLight.ansi[15], isNot(nordLight.terminalBg));
    expect(nordLight.ansi[7], isNot(nordLight.terminalBg));
  });

  test('accentColor maps each choice to the palette hue', () {
    for (final p in _all) {
      expect(accentColor(p, AccentChoice.paletteDefault), isNull);
      expect(accentColor(p, AccentChoice.red), p.red);
      expect(accentColor(p, AccentChoice.orange), p.orange);
      expect(accentColor(p, AccentChoice.yellow), p.yellow);
      expect(accentColor(p, AccentChoice.green), p.green);
      expect(accentColor(p, AccentChoice.aqua), p.aqua);
      expect(accentColor(p, AccentChoice.blue), p.blue);
      expect(accentColor(p, AccentChoice.purple), p.purple);
    }
  });

  test('contrast report (informational)', () {
    final rows = <String>[
      '${'palette'.padRight(18)} ${'token'.padRight(14)} canvas  surface',
    ];
    for (final p in _all) {
      final t = ArgusTokens.from(p);
      final tokens = {
        'text': t.text,
        'textSecondary': t.textSecondary,
        'textDim': t.textDim,
        'blocked': t.blocked,
        'waiting': t.waiting,
        'working': t.working,
        'error': t.error,
      };
      for (final MapEntry(:key, :value) in tokens.entries) {
        final onCanvas = _contrast(value, t.canvas);
        final onSurface = _contrast(value, t.surface);
        for (final r in [onCanvas, onSurface]) {
          expect(r.isFinite && r >= 1, isTrue, reason: '${p.name} $key');
        }
        rows.add(
          '${p.name.padRight(18)} ${key.padRight(14)} '
          '${onCanvas.toStringAsFixed(2).padLeft(6)}  '
          '${onSurface.toStringAsFixed(2).padLeft(7)}',
        );
      }
    }
    // ignore: avoid_print
    print(rows.join('\n'));
  });
}
