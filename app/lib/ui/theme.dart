import 'package:flutter/material.dart';

/// Gruvbox-dark visual tokens (see docs/superpowers/specs/2026-06-20-argus-mobile-design.md).
class AppColors {
  AppColors._();

  static const canvas = Color(0xFF1d2021); // bg0_h
  static const card = Color(0xFF282828);
  static const border = Color(0xFF3c3836);
  static const text = Color(0xFFebdbb2);
  static const secondary = Color(0xFFd5c4a1);
  static const dim = Color(0xFF928374);
  static const accent = Color(0xFF8ec07c); // aqua — non-status chrome
  static const awaitingSurface = Color(0xFF32281d);
  static const awaitingBorder = Color(0xFF7c5f1e);
  static const error = Color(0xFFfb4934); // gruvbox red — fatal connection state
  static const errorSurface = Color(0xFF3c1f1d);
}

Color teamColor(String? name) {
  switch (name) {
    case 'blue':
      return const Color(0xFF83a598);
    case 'green':
      return const Color(0xFFb8bb26);
    case 'red':
      return const Color(0xFFfb4934);
    case 'yellow':
      return const Color(0xFFfabd2f);
    case 'purple':
      return const Color(0xFFd3869b);
    case 'cyan':
      return const Color(0xFF8ec07c);
    case 'orange':
      return const Color(0xFFfe8019);
    case 'pink':
      return const Color(0xFFf7a1b0);
    default:
      return AppColors.accent;
  }
}

ThemeData buildArgusTheme() {
  final scheme = ColorScheme.fromSeed(
    seedColor: AppColors.accent,
    brightness: Brightness.dark,
  ).copyWith(
    primary: AppColors.accent,
    surface: AppColors.card,
    onSurface: AppColors.text,
  );
  return ThemeData(
    useMaterial3: true,
    brightness: Brightness.dark,
    colorScheme: scheme,
    scaffoldBackgroundColor: AppColors.canvas,
    textTheme: Typography.whiteMountainView.apply(
      bodyColor: AppColors.text,
      displayColor: AppColors.text,
    ),
  );
}
