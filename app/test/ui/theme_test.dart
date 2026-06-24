import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/ui/theme.dart';

void main() {
  test('palette exposes gruvbox tokens', () {
    expect(AppColors.canvas, const Color(0xFF1d2021));
    expect(AppColors.card, const Color(0xFF282828));
    expect(AppColors.accent, const Color(0xFF8ec07c));
    expect(AppColors.awaitingSurface, const Color(0xFF32281d));
  });

  test('theme is dark and uses the canvas background', () {
    final t = buildArgusTheme();
    expect(t.brightness, Brightness.dark);
    expect(t.scaffoldBackgroundColor, AppColors.canvas);
  });
}
