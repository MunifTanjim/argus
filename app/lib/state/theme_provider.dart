import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../ui/theme/build_theme.dart';
import '../ui/theme/palettes.dart';
import 'appearance.dart';

/// Light and dark themes built from the appearance prefs, plus the mode and
/// text scale the app applies on top.
final argusThemeProvider =
    Provider<
      ({ThemeData light, ThemeData dark, ThemeMode mode, double textScale})
    >((ref) {
      final themes = ref.watch(_themesProvider);
      return (
        light: themes.light,
        dark: themes.dark,
        mode: ref.watch(appearancePrefsProvider.select((p) => p.mode)),
        textScale: ref.watch(
          appearancePrefsProvider.select((p) => p.textScale),
        ),
      );
    });

/// The themes, rebuilt only when a pref they use changes. A new theme starts
/// the app's theme animation, so the mode, the text scale, and the
/// transcript pref must not rebuild them.
final _themesProvider = Provider<({ThemeData light, ThemeData dark})>((ref) {
  ref.watch(
    appearancePrefsProvider.select(
      (p) => (p.palette, p.accent, p.font, p.density, p.radius, p.statusStyle),
    ),
  );
  final prefs = ref.read(appearancePrefsProvider);
  return (
    light: buildArgusTheme(
      palette: paletteFor(prefs.palette, Brightness.light),
      prefs: prefs,
    ),
    dark: buildArgusTheme(
      palette: paletteFor(prefs.palette, Brightness.dark),
      prefs: prefs,
    ),
  );
});
