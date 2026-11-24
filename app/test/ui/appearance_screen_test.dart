import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/state/appearance.dart';
import 'package:argus/ui/appearance_screen.dart';
import 'package:argus/ui/session_card.dart';
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

Future<_FakeKv> _pump(WidgetTester tester) async {
  final kv = _FakeKv();
  tester.view.physicalSize = const Size(800, 3000);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        appearanceStoreProvider.overrideWithValue(AppearanceStore(kv)),
      ],
      child: const MaterialApp(home: AppearanceScreen()),
    ),
  );
  await tester.pumpAndSettle();
  return kv;
}

void main() {
  testWidgets('shows every setting under its heading', (tester) async {
    await _pump(tester);
    for (final t in [
      'Theme',
      'Layout',
      'Content',
      'Palette',
      'Mode',
      'Accent',
      'Density',
      'Text size',
      'Corner radius',
      'Status style',
      'Interface font',
      'Verbose transcript',
      'Gruvbox',
      'Catppuccin',
      'Tokyo Night',
      'Nord',
      'Solarized',
      'System',
      'Dark',
      'Light',
      'Compact',
      'Comfortable',
      '90%',
      '100%',
      '115%',
      '130%',
      'Sharp',
      'Medium',
      'Rounded',
      'Stripe',
      'Dot',
      'Filled',
      'Sans',
      'Mono',
    ]) {
      expect(find.text(t), findsOneWidget, reason: t);
    }
    expect(find.byKey(const Key('appearance-preview')), findsOneWidget);
    expect(
      find.descendant(
        of: find.byKey(const Key('appearance-preview')),
        matching: find.byType(SessionCard),
      ),
      findsNWidgets(3),
    );
  });

  testWidgets('choosing a palette persists and recolors the preview', (
    tester,
  ) async {
    final kv = await _pump(tester);
    await tester.tap(find.text('Nord'));
    await tester.pumpAndSettle();
    expect(kv.m['appearance.palette'], 'nord');
    final ctx = tester.element(
      find.descendant(
        of: find.byKey(const Key('appearance-preview')),
        matching: find.byType(SessionCard).first,
      ),
    );
    expect(ctx.tokens.canvas, nord.bg0);
  });

  testWidgets('status style change updates the preview', (tester) async {
    final kv = await _pump(tester);
    final dot = find.descendant(
      of: find.byKey(const Key('appearance-preview')),
      matching: find.byKey(const Key('status-dot')),
    );
    expect(dot, findsNothing);
    await tester.tap(find.text('Dot'));
    await tester.pumpAndSettle();
    expect(kv.m['appearance.statusStyle'], 'dot');
    expect(dot, findsWidgets);
  });

  testWidgets('verbose transcript toggle persists', (tester) async {
    final kv = await _pump(tester);
    expect(find.text('Verbose transcript'), findsOneWidget);
    expect(find.text('Collapse tool calls'), findsNothing);
    expect(
      tester.widget<SwitchListTile>(find.byType(SwitchListTile)).value,
      isFalse,
    );

    await tester.tap(find.text('Verbose transcript'));
    await tester.pumpAndSettle();
    expect(
      tester.widget<SwitchListTile>(find.byType(SwitchListTile)).value,
      isTrue,
    );
    expect(kv.m['appearance.verboseTranscript'], 'true');
  });
}
