import 'package:flutter/foundation.dart' show debugDefaultTargetPlatformOverride;
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/ui/theme.dart';

double _contrast(Color a, Color b) {
  final la = a.computeLuminance(), lb = b.computeLuminance();
  final (hi, lo) = la > lb ? (la, lb) : (lb, la);
  return (hi + 0.05) / (lo + 0.05);
}

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

  group('phone theme', () {
    for (final platform in [TargetPlatform.android, TargetPlatform.iOS]) {
      test('is unchanged on $platform', () {
        final t = buildArgusTheme(platform: platform);
        // whiteMountainView leaves sizes null; Material fills in the 14 pt body size.
        final base = Typography.whiteMountainView;
        expect(t.textTheme.bodyMedium!.fontSize, base.bodyMedium!.fontSize);
        expect(t.textTheme.titleLarge!.fontSize, base.titleLarge!.fontSize);
        expect(t.textTheme.bodySmall!.fontSize, base.bodySmall!.fontSize);
        expect(t.visualDensity, VisualDensity.standard);
        expect(t.splashFactory, ThemeData().splashFactory);
        expect(t.materialTapTargetSize, MaterialTapTargetSize.padded);
        expect(t.filledButtonTheme.style, isNull);
        expect(t.appBarTheme.toolbarHeight, isNull);
        expect(t.snackBarTheme.behavior, isNull);
        expect(t.listTileTheme.dense, isNull);
        expect(t.snackBarTheme, const SnackBarThemeData());
      });
    }
  });

  group('desktop theme', () {
    final desktops = [TargetPlatform.macOS, TargetPlatform.linux];

    for (final platform in desktops) {
      group('$platform', () {
        final t = buildArgusTheme(platform: platform);

        test('uses the desktop scale', () {
          expect(t.textTheme.bodyLarge!.fontSize, 14);
          expect(t.textTheme.bodyMedium!.fontSize, 13);
          expect(t.textTheme.bodySmall!.fontSize, 11);
          expect(t.textTheme.labelLarge!.fontSize, 13);
          expect(t.textTheme.labelLarge!.fontWeight, FontWeight.w500);
          expect(t.textTheme.labelMedium!.fontSize, 12);
          expect(t.textTheme.labelSmall!.fontSize, 11);
          expect(t.textTheme.titleSmall!.fontSize, 13);
          expect(t.textTheme.titleSmall!.fontWeight, FontWeight.w600);
          expect(t.textTheme.titleMedium!.fontSize, 14);
          expect(t.textTheme.titleMedium!.fontWeight, FontWeight.w600);
          expect(t.textTheme.titleLarge!.fontSize, 16);
          expect(t.textTheme.titleLarge!.fontWeight, FontWeight.w600);
          expect(t.textTheme.headlineSmall!.fontSize, 20);
          expect(t.textTheme.headlineSmall!.fontWeight, FontWeight.w600);
          expect(t.textTheme.bodyMedium!.color, AppColors.text);
        });

        test('is compact and has no ripple', () {
          expect(t.visualDensity, VisualDensity.compact);
          expect(t.splashFactory, NoSplash.splashFactory);
          expect(t.materialTapTargetSize, MaterialTapTargetSize.shrinkWrap);
          expect(t.listTileTheme.dense, true);
          expect(t.listTileTheme.minTileHeight, 28);
          expect(t.listTileTheme.minVerticalPadding, 2);
          expect(
            t.listTileTheme.contentPadding,
            const EdgeInsets.symmetric(horizontal: 12),
          );
        });

        test('buttons are small and flat', () {
          for (final style in [
            t.filledButtonTheme.style!,
            t.outlinedButtonTheme.style!,
            t.textButtonTheme.style!,
            t.iconButtonTheme.style!,
          ]) {
            expect(style.minimumSize!.resolve({}), const Size(0, 28));
            final shape = style.shape!.resolve({}) as RoundedRectangleBorder;
            expect(shape.borderRadius, BorderRadius.circular(6));
          }
          for (final style in [
            t.filledButtonTheme.style!,
            t.outlinedButtonTheme.style!,
            t.textButtonTheme.style!,
          ]) {
            expect(
              style.padding!.resolve({}),
              const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
            );
            final text = style.textStyle!.resolve({})!;
            expect(text.fontSize, 13);
            expect(text.fontWeight, FontWeight.w500);
          }
          expect(t.iconButtonTheme.style!.iconSize!.resolve({}), 18);
        });

        test('app bar, snackbar, dialog, menu and scrollbar', () {
          expect(t.appBarTheme.toolbarHeight, 40);
          expect(t.appBarTheme.backgroundColor, AppColors.canvas);
          expect(t.appBarTheme.scrolledUnderElevation, 0);
          expect(t.appBarTheme.surfaceTintColor, Colors.transparent);
          expect(t.appBarTheme.titleTextStyle!.fontSize, 14);
          expect(t.appBarTheme.titleTextStyle!.fontWeight, FontWeight.w600);
          expect(t.appBarTheme.centerTitle, false);
          expect(t.snackBarTheme.behavior, SnackBarBehavior.floating);
          expect(t.snackBarTheme.width, 360);
          expect(t.snackBarTheme.contentTextStyle!.fontSize, 13);
          expect(
            (t.snackBarTheme.shape as RoundedRectangleBorder).borderRadius,
            BorderRadius.circular(8),
          );
          expect(
            (t.dialogTheme.shape as RoundedRectangleBorder).borderRadius,
            BorderRadius.circular(8),
          );
          expect(
            (t.popupMenuTheme.shape as RoundedRectangleBorder).borderRadius,
            BorderRadius.circular(8),
          );
          expect(t.popupMenuTheme.textStyle!.fontSize, 13);
          expect(t.scrollbarTheme.thickness!.resolve({}), 6);
        });

        test('snackbar text and action are readable on its background', () {
          final bg = t.snackBarTheme.backgroundColor!;
          expect(bg, AppColors.card);
          final text = t.snackBarTheme.contentTextStyle!.color!;
          expect(text, AppColors.text);
          expect(_contrast(text, bg), greaterThanOrEqualTo(4.5));
          final action = t.snackBarTheme.actionTextColor!;
          expect(action, AppColors.accent);
          expect(_contrast(action, bg), greaterThanOrEqualTo(4.5));
        });
      });
    }

    test('uses the platform font', () {
      final t = buildArgusTheme(platform: TargetPlatform.macOS);
      expect(
        t.textTheme.bodyMedium!.fontFamily,
        Typography.material2021(
          platform: TargetPlatform.macOS,
        ).white.bodyMedium!.fontFamily,
      );
    });

    test('defaults to the current platform', () {
      debugDefaultTargetPlatformOverride = TargetPlatform.macOS;
      addTearDown(() => debugDefaultTargetPlatformOverride = null);
      expect(buildArgusTheme().textTheme.bodyMedium!.fontSize, 13);
    });
  });
}
