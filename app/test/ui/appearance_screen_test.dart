import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/state/appearance.dart';
import 'package:argus/ui/appearance_screen.dart';
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

void main() {
  testWidgets('verbose transcript toggle persists', (tester) async {
    final kv = _FakeKv();
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          appearanceStoreProvider.overrideWithValue(AppearanceStore(kv)),
        ],
        child: const MaterialApp(home: AppearanceScreen()),
      ),
    );
    await tester.pumpAndSettle();
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
