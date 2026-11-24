import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/state/appearance.dart';
import 'package:argus/state/theme_provider.dart';
import 'package:argus/ui/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

class _FakeKv implements SecureKv {
  final Map<String, String> m = {};
  @override
  Future<String?> read(String key) async => m[key];
  @override
  Future<void> write(String key, String value) async => m[key] = value;
  @override
  Future<void> delete(String key) async => m.remove(key);
}

Future<ProviderContainer> _container() async {
  final c = ProviderContainer(
    overrides: [
      appearanceStoreProvider.overrideWithValue(AppearanceStore(_FakeKv())),
    ],
  );
  await c.read(appearancePrefsProvider.notifier).loaded;
  return c;
}

ArgusTokens _tokens(ThemeData t) => t.extension<ArgusTokens>()!;

void main() {
  test('dark and light themes use the chosen palette', () async {
    final c = await _container();
    addTearDown(c.dispose);
    await c
        .read(appearancePrefsProvider.notifier)
        .setPalette(ArgusPaletteId.catppuccin);
    final t = c.read(argusThemeProvider);
    expect(_tokens(t.dark).canvas, catppuccinMocha.bg0);
    expect(_tokens(t.light).canvas, catppuccinLatte.bg0);
    expect(t.dark.brightness, Brightness.dark);
    expect(t.light.brightness, Brightness.light);
    expect(t.light.colorScheme.brightness, Brightness.light);
  });

  test('accent choice overrides the palette accent', () async {
    final c = await _container();
    addTearDown(c.dispose);
    expect(_tokens(c.read(argusThemeProvider).dark).accent, gruvboxDark.aqua);
    await c.read(appearancePrefsProvider.notifier).setAccent(AccentChoice.red);
    final t = c.read(argusThemeProvider);
    expect(_tokens(t.dark).accent, gruvboxDark.red);
    expect(_tokens(t.light).accent, gruvboxLight.red);
    expect(t.dark.colorScheme.primary, gruvboxDark.red);
  });

  test('radius, density, font, statusStyle reach the theme', () async {
    final c = await _container();
    addTearDown(c.dispose);
    var t = c.read(argusThemeProvider).dark;
    expect(t.visualDensity, VisualDensity.compact);
    expect(
      t.textTheme.bodyMedium!.fontFamily,
      isNot('JetBrainsMonoNerdFontMono'),
    );

    final n = c.read(appearancePrefsProvider.notifier);
    await n.setRadius(CornerRadius.rounded);
    await n.setDensity(AppDensity.comfortable);
    await n.setFont(InterfaceFont.mono);
    await n.setStatusStyle(StatusStyle.dot);
    t = c.read(argusThemeProvider).dark;

    expect(_tokens(t).radius, 12);
    expect(
      t.cardTheme.shape,
      RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
    );
    expect(
      t.dialogTheme.shape,
      RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
    );
    expect(
      t.bottomSheetTheme.shape,
      RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
    );
    expect(
      t.filledButtonTheme.style!.shape!.resolve({}),
      RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
    );
    expect(
      t.outlinedButtonTheme.style!.shape!.resolve({}),
      RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
    );
    expect(
      (t.inputDecorationTheme.border! as OutlineInputBorder).borderRadius,
      BorderRadius.circular(12),
    );
    expect(_tokens(t).density, AppDensity.comfortable);
    expect(t.visualDensity, VisualDensity.standard);
    expect(t.textTheme.bodyMedium!.fontFamily, 'JetBrainsMonoNerdFontMono');
    expect(_tokens(t).statusStyle, StatusStyle.dot);
  });

  test('mode and textScale pass through', () async {
    final c = await _container();
    addTearDown(c.dispose);
    final n = c.read(appearancePrefsProvider.notifier);
    await n.setMode(ThemeMode.system);
    await n.setTextScale(1.15);
    final t = c.read(argusThemeProvider);
    expect(t.mode, ThemeMode.system);
    expect(t.textScale, 1.15);
  });

  test('app bar and navigation bar use tokens', () {
    final theme = buildArgusTheme();
    final t = _tokens(theme);
    expect(theme.appBarTheme.backgroundColor, t.canvas);
    expect(theme.appBarTheme.elevation, 0);
    expect(theme.appBarTheme.scrolledUnderElevation, 0);
    expect(theme.appBarTheme.shape, Border(bottom: BorderSide(color: t.line)));
    final title = theme.appBarTheme.titleTextStyle!;
    expect(title.fontFamily, 'JetBrainsMonoNerdFontMono');
    expect(title.fontSize, 15);
    expect(title.fontWeight, FontWeight.w600);

    final nav = theme.navigationBarTheme;
    expect(nav.backgroundColor, t.canvas);
    expect(nav.indicatorColor, Colors.transparent);
    const sel = {WidgetState.selected};
    final label = nav.labelTextStyle!.resolve(sel)!;
    expect(label.fontFamily, 'JetBrainsMonoNerdFontMono');
    expect(label.fontSize, 11);
    expect(label.color, t.accent);
    expect(nav.labelTextStyle!.resolve({})!.color, t.textDim);
    expect(nav.iconTheme!.resolve(sel)!.color, t.accent);
    expect(nav.iconTheme!.resolve({})!.color, t.textDim);
  });
}
