import 'package:flutter/foundation.dart' show defaultTargetPlatform;
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
  static const blocked = Color(0xFFfabd2f); // gruvbox yellow — blocked on a prompt
  static const blockedSurface = awaitingSurface;
  static const blockedBorder = awaitingBorder;
  static const waiting = Color(0xFF83a598); // gruvbox blue — finished its turn
  static const waitingSurface = Color(0xFF1f2b2c);
  static const waitingBorder = Color(0xFF45707a);
  static const error = Color(
    0xFFfb4934,
  ); // gruvbox red — fatal connection state
  static const errorSurface = Color(0xFF3c1f1d);
  static const notice = Color(0xFFfabd2f); // gruvbox yellow — prompt notices
  static const link = Color(
    0xFF83a598,
  ); // gruvbox blue — branches, paths, hunk headers
}

const mono = TextStyle(fontFamily: 'monospace', fontSize: 12);

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

bool _isDesktopPlatform(TargetPlatform platform) =>
    platform != TargetPlatform.android && platform != TargetPlatform.iOS;

ThemeData buildArgusTheme({TargetPlatform? platform}) {
  final target = platform ?? defaultTargetPlatform;
  final scheme =
      ColorScheme.fromSeed(
        seedColor: AppColors.accent,
        brightness: Brightness.dark,
      ).copyWith(
        primary: AppColors.accent,
        surface: AppColors.card,
        onSurface: AppColors.text,
      );
  if (_isDesktopPlatform(target)) return _desktopTheme(target, scheme);
  return ThemeData(
    useMaterial3: true,
    brightness: Brightness.dark,
    colorScheme: scheme,
    scaffoldBackgroundColor: AppColors.canvas,
    appBarTheme: const AppBarTheme(centerTitle: false),
    textTheme: Typography.whiteMountainView.apply(
      bodyColor: AppColors.text,
      displayColor: AppColors.text,
    ),
  );
}

ThemeData _desktopTheme(TargetPlatform platform, ColorScheme scheme) {
  final base = Typography.material2021(platform: platform).white.apply(
    bodyColor: AppColors.text,
    displayColor: AppColors.text,
  );
  TextStyle sized(TextStyle? style, double size, [FontWeight? weight]) =>
      style!.copyWith(fontSize: size, fontWeight: weight);
  final merged = base.copyWith(
    bodyLarge: sized(base.bodyLarge, 14),
    bodyMedium: sized(base.bodyMedium, 13),
    bodySmall: sized(base.bodySmall, 11),
    labelLarge: sized(base.labelLarge, 13, FontWeight.w500),
    labelMedium: sized(base.labelMedium, 12),
    labelSmall: sized(base.labelSmall, 11),
    titleSmall: sized(base.titleSmall, 13, FontWeight.w600),
    titleMedium: sized(base.titleMedium, 14, FontWeight.w600),
    titleLarge: sized(base.titleLarge, 16, FontWeight.w600),
    headlineSmall: sized(base.headlineSmall, 20, FontWeight.w600),
  );

  final buttonShape = WidgetStatePropertyAll<OutlinedBorder>(
    RoundedRectangleBorder(borderRadius: BorderRadius.circular(6)),
  );
  const buttonMinimumSize = WidgetStatePropertyAll(Size(0, 28));
  const buttonPadding = WidgetStatePropertyAll(
    EdgeInsets.symmetric(horizontal: 12, vertical: 6),
  );
  const buttonText = WidgetStatePropertyAll(
    TextStyle(fontSize: 13, fontWeight: FontWeight.w500),
  );
  final dialogShape = RoundedRectangleBorder(
    borderRadius: BorderRadius.circular(8),
  );

  return ThemeData(
    useMaterial3: true,
    brightness: Brightness.dark,
    platform: platform,
    colorScheme: scheme,
    scaffoldBackgroundColor: AppColors.canvas,
    textTheme: merged,
    visualDensity: VisualDensity.compact,
    materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
    splashFactory: NoSplash.splashFactory,
    hoverColor: AppColors.text.withValues(alpha: 0.06),
    highlightColor: AppColors.text.withValues(alpha: 0.08),
    listTileTheme: const ListTileThemeData(
      dense: true,
      minVerticalPadding: 2,
      contentPadding: EdgeInsets.symmetric(horizontal: 12),
      minTileHeight: 28,
    ),
    filledButtonTheme: FilledButtonThemeData(
      style: ButtonStyle(
        minimumSize: buttonMinimumSize,
        padding: buttonPadding,
        shape: buttonShape,
        textStyle: buttonText,
      ),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      style: ButtonStyle(
        minimumSize: buttonMinimumSize,
        padding: buttonPadding,
        shape: buttonShape,
        textStyle: buttonText,
      ),
    ),
    textButtonTheme: TextButtonThemeData(
      style: ButtonStyle(
        minimumSize: buttonMinimumSize,
        padding: buttonPadding,
        shape: buttonShape,
        textStyle: buttonText,
      ),
    ),
    iconButtonTheme: IconButtonThemeData(
      style: ButtonStyle(
        minimumSize: buttonMinimumSize,
        shape: buttonShape,
        iconSize: const WidgetStatePropertyAll(18),
      ),
    ),
    appBarTheme: AppBarTheme(
      centerTitle: false,
      toolbarHeight: 40,
      backgroundColor: AppColors.canvas,
      scrolledUnderElevation: 0,
      surfaceTintColor: Colors.transparent,
      titleTextStyle: merged.titleMedium!.copyWith(color: AppColors.text),
    ),
    snackBarTheme: SnackBarThemeData(
      behavior: SnackBarBehavior.floating,
      width: 360,
      shape: dialogShape,
      contentTextStyle: merged.bodyMedium,
    ),
    dialogTheme: DialogThemeData(
      shape: dialogShape,
      titleTextStyle: merged.titleMedium,
      contentTextStyle: merged.bodyMedium,
      insetPadding: const EdgeInsets.symmetric(horizontal: 32, vertical: 24),
    ),
    popupMenuTheme: PopupMenuThemeData(
      shape: dialogShape,
      textStyle: merged.bodyMedium,
      menuPadding: const EdgeInsets.symmetric(vertical: 4),
    ),
    scrollbarTheme: const ScrollbarThemeData(
      thickness: WidgetStatePropertyAll(6),
    ),
  );
}
