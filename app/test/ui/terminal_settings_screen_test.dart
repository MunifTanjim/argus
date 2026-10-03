import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/state/terminal_prefs.dart';
import 'package:argus/ui/terminal_settings_screen.dart';

class _FakeKv implements SecureKv {
  _FakeKv(this.m);
  final Map<String, String> m;
  @override
  Future<String?> read(String key) async => m[key];
  @override
  Future<void> write(String key, String value) async => m[key] = value;
  @override
  Future<void> delete(String key) async => m.remove(key);
}

void main() {
  testWidgets('choosing Ghostty saves it', (tester) async {
    final kv = _FakeKv({});
    await tester.pumpWidget(ProviderScope(
      overrides: [terminalStoreProvider.overrideWithValue(TerminalStore(kv))],
      child: const MaterialApp(home: TerminalSettingsScreen()),
    ));
    await tester.pump();
    expect(find.text('xterm'), findsOneWidget);
    await tester.tap(find.text('Ghostty'));
    await tester.pump();
    expect(kv.m['terminal.emulator'], 'ghostty');
  });

  testWidgets('the font size slider saves the size', (tester) async {
    final kv = _FakeKv({});
    await tester.pumpWidget(ProviderScope(
      overrides: [terminalStoreProvider.overrideWithValue(TerminalStore(kv))],
      child: const MaterialApp(home: TerminalSettingsScreen()),
    ));
    await tester.pump();
    final slider = tester.widget<Slider>(find.byType(Slider));
    expect(slider.value, 12);
    slider.onChanged!(17);
    await tester.pump();
    expect(kv.m['terminal.fontSize'], '17.0');
    expect(find.text('17'), findsOneWidget);
  });
}
