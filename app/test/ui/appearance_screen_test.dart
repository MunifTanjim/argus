import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/state/appearance.dart';
import 'package:argus/ui/appearance_screen.dart';
import 'package:argus/ui/session_card.dart';
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
      '100%',
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
    expect(find.byKey(const Key('appearance-preview')), findsNothing);
    expect(find.byType(SessionCard), findsNothing);
  });

  testWidgets('choosing a palette persists it', (tester) async {
    final kv = await _pump(tester);
    await tester.tap(find.text('Nord'));
    await tester.pumpAndSettle();
    expect(kv.m['appearance.palette'], 'nord');
  });

  testWidgets('choosing a status style persists it', (tester) async {
    final kv = await _pump(tester);
    await tester.tap(find.text('Dot'));
    await tester.pumpAndSettle();
    expect(kv.m['appearance.statusStyle'], 'dot');
  });

  testWidgets('text size slider persists the size on release', (tester) async {
    final kv = await _pump(tester);
    final slider = find.byKey(const Key('text-scale-slider'));
    expect(tester.widget<Slider>(slider).value, 1.0);

    // Drag from the 100% thumb to the right end of the track.
    final thumb = tester.getCenter(slider);
    final rect = tester.getRect(slider);
    final g = await tester.startGesture(
      Offset(rect.left + (rect.width - 48) * (0.2 / 0.7) + 24, thumb.dy),
    );
    await g.moveTo(Offset(rect.right, thumb.dy));
    await tester.pump();
    expect(find.text('150%'), findsOneWidget);
    expect(kv.m['appearance.textScale'], isNull);

    await g.up();
    await tester.pumpAndSettle();
    expect(kv.m['appearance.textScale'], '1.5');
    expect(tester.widget<Slider>(slider).value, 1.5);
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
