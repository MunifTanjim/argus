import 'package:flutter/material.dart';

import '../../state/appearance.dart';
import 'palette.dart';
import 'palettes.dart';
import 'tokens.dart';

/// The mono font: identifiers, code, and terminal text.
const monoFamily = 'JetBrainsMonoNerdFontMono';

ThemeData buildArgusTheme({
  ArgusPalette palette = gruvboxDark,
  AppearancePrefs prefs = const AppearancePrefs(),
}) {
  final t = ArgusTokens.from(
    palette,
    statusStyle: prefs.statusStyle,
    radius: prefs.radius.value,
    accent: accentColor(palette, prefs.accent),
  );
  final dark = palette.brightness == Brightness.dark;
  final fontFamily = prefs.font == InterfaceFont.mono ? monoFamily : null;
  final scheme = ColorScheme.fromSeed(
    seedColor: t.accent,
    brightness: palette.brightness,
  ).copyWith(
    primary: t.accent,
    onPrimary: t.onAccent,
    secondary: t.accent,
    onSecondary: t.onAccent,
    secondaryContainer: t.surfaceRaised,
    onSecondaryContainer: t.text,
    surface: t.surface,
    onSurface: t.text,
    surfaceContainerLowest: t.canvas,
    surfaceContainerLow: t.surface,
    surfaceContainer: t.surface,
    surfaceContainerHigh: t.surfaceRaised,
    surfaceContainerHighest: t.surfaceRaised,
    outline: t.textDim,
    outlineVariant: t.line,
    error: t.error,
    onError: moreReadableOn(t.error, t.canvas, t.text),
    inverseSurface: t.text,
    onInverseSurface: t.canvas,
    surfaceTint: Colors.transparent,
  );
  final shape = RoundedRectangleBorder(
    borderRadius: BorderRadius.circular(t.radius),
  );
  return ThemeData(
    useMaterial3: true,
    brightness: palette.brightness,
    colorScheme: scheme,
    scaffoldBackgroundColor: t.canvas,
    fontFamily: fontFamily,
    visualDensity: VisualDensity.compact,
    cardTheme: CardThemeData(shape: shape),
    dialogTheme: DialogThemeData(shape: shape),
    bottomSheetTheme: BottomSheetThemeData(shape: shape),
    inputDecorationTheme: InputDecorationTheme(
      border: OutlineInputBorder(borderRadius: BorderRadius.circular(t.radius)),
    ),
    filledButtonTheme: FilledButtonThemeData(
      style: FilledButton.styleFrom(shape: shape),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      style: OutlinedButton.styleFrom(shape: shape),
    ),
    textButtonTheme: TextButtonThemeData(
      style: TextButton.styleFrom(shape: shape),
    ),
    elevatedButtonTheme: ElevatedButtonThemeData(
      style: ElevatedButton.styleFrom(shape: shape),
    ),
    segmentedButtonTheme: SegmentedButtonThemeData(
      style: SegmentedButton.styleFrom(shape: shape),
    ),
    popupMenuTheme: PopupMenuThemeData(shape: shape),
    menuTheme: MenuThemeData(
      style: MenuStyle(shape: WidgetStatePropertyAll(shape)),
    ),
    dropdownMenuTheme: DropdownMenuThemeData(
      menuStyle: MenuStyle(shape: WidgetStatePropertyAll(shape)),
    ),
    chipTheme: ChipThemeData(shape: shape),
    // A large surface, so its corners run 4 wider, as the composer's do.
    floatingActionButtonTheme: FloatingActionButtonThemeData(
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(t.radiusLarge),
      ),
    ),
    tooltipTheme: TooltipThemeData(
      decoration: BoxDecoration(
        color: t.text,
        borderRadius: BorderRadius.circular(t.radiusSmall),
      ),
    ),
    appBarTheme: AppBarTheme(
      centerTitle: false,
      backgroundColor: t.canvas,
      elevation: 0,
      scrolledUnderElevation: 0,
      shape: Border(bottom: BorderSide(color: t.line)),
      titleTextStyle: TextStyle(
        fontFamily: monoFamily,
        fontSize: 15,
        fontWeight: FontWeight.w600,
        color: t.text,
      ),
    ),
    navigationBarTheme: NavigationBarThemeData(
      backgroundColor: t.canvas,
      indicatorColor: Colors.transparent,
      labelTextStyle: WidgetStateProperty.resolveWith(
        (states) => TextStyle(
          fontFamily: monoFamily,
          fontSize: 11,
          color: states.contains(WidgetState.selected) ? t.accent : t.textDim,
        ),
      ),
      iconTheme: WidgetStateProperty.resolveWith(
        (states) => IconThemeData(
          color: states.contains(WidgetState.selected) ? t.accent : t.textDim,
        ),
      ),
    ),
    textTheme:
        (dark ? Typography.whiteMountainView : Typography.blackMountainView)
            .apply(
              fontFamily: fontFamily,
              bodyColor: t.text,
              displayColor: t.text,
            ),
    extensions: [t],
  );
}
