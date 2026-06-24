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
