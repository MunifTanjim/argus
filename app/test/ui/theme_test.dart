import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/state/appearance.dart';
import 'package:argus/ui/theme.dart';

void main() {
  test('palette exposes gruvbox tokens', () {
    final t = ArgusTokens.fallback;
    expect(t.canvas, const Color(0xFF1d2021));
    expect(t.surface, const Color(0xFF282828));
    expect(t.accent, const Color(0xFF8ec07c));
    expect(t.blockedSurface, const Color(0xFF32281d));
  });

  test('theme is dark and uses the canvas background', () {
    final t = buildArgusTheme();
    expect(t.brightness, Brightness.dark);
    expect(t.scaffoldBackgroundColor, ArgusTokens.fallback.canvas);
  });

  for (final p in [gruvboxDark, nordLight]) {
    group('${p.name} ${p.brightness.name} color scheme comes from tokens', () {
      final theme = buildArgusTheme(palette: p);
      final t = theme.extension<ArgusTokens>()!;
      final cs = theme.colorScheme;

      test('fields', () {
        expect(cs.primary, t.accent);
        expect(cs.onPrimary, t.onAccent);
        expect(cs.secondary, t.accent);
        expect(cs.onSecondary, t.onAccent);
        expect(cs.secondaryContainer, t.surfaceRaised);
        expect(cs.onSecondaryContainer, t.text);
        expect(cs.surface, t.surface);
        expect(cs.onSurface, t.text);
        expect(cs.surfaceContainerLowest, t.canvas);
        expect(cs.surfaceContainerLow, t.surface);
        expect(cs.surfaceContainer, t.surface);
        expect(cs.surfaceContainerHigh, t.surfaceRaised);
        expect(cs.surfaceContainerHighest, t.surfaceRaised);
        expect(cs.outline, t.textDim);
        expect(cs.outlineVariant, t.line);
        expect(cs.error, t.error);
        expect(
          contrastRatio(cs.onError, t.error),
          greaterThanOrEqualTo(contrastRatio(t.canvas, t.error)),
        );
        expect(
          contrastRatio(cs.onError, t.error),
          greaterThanOrEqualTo(contrastRatio(t.text, t.error)),
        );
        expect(cs.inverseSurface, t.text);
        expect(cs.onInverseSurface, t.canvas);
        expect(cs.surfaceTint, Colors.transparent);
      });

      Color materialColorOf(WidgetTester tester, Finder f) => tester
          .widget<Material>(
            find.ancestor(of: f, matching: find.byType(Material)).first,
          )
          .color!;

      testWidgets('dialogs, sheets and snackbars', (tester) async {
        await tester.pumpWidget(
          MaterialApp(
            theme: theme,
            home: const Scaffold(body: SizedBox()),
          ),
        );
        final ctx = tester.element(find.byType(SizedBox));

        showDialog<void>(
          context: ctx,
          builder: (_) => const AlertDialog(content: Text('dialog')),
        );
        await tester.pumpAndSettle();
        expect(materialColorOf(tester, find.text('dialog')), t.surfaceRaised);
        Navigator.of(ctx).pop();
        await tester.pumpAndSettle();

        showModalBottomSheet<void>(
          context: ctx,
          builder: (_) => const Text('sheet'),
        );
        await tester.pumpAndSettle();
        expect(materialColorOf(tester, find.text('sheet')), t.surface);
        Navigator.of(ctx).pop();
        await tester.pumpAndSettle();

        ScaffoldMessenger.of(
          ctx,
        ).showSnackBar(const SnackBar(content: Text('snack')));
        await tester.pumpAndSettle();
        expect(materialColorOf(tester, find.text('snack')), t.text);
      });
    });
  }

  test('text and elevated buttons take the corner radius', () {
    final theme = buildArgusTheme(
      prefs: const AppearancePrefs(radius: CornerRadius.rounded),
    );
    for (final style in [
      theme.textButtonTheme.style!,
      theme.elevatedButtonTheme.style!,
    ]) {
      final shape = style.shape!.resolve({}) as RoundedRectangleBorder;
      expect(shape.borderRadius, BorderRadius.circular(12));
    }
  });
}
