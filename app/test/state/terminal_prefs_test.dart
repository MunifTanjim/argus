import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/state/terminal_prefs.dart';

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

ProviderContainer _container(_FakeKv kv) {
  final c = ProviderContainer(
      overrides: [terminalStoreProvider.overrideWithValue(TerminalStore(kv))]);
  addTearDown(c.dispose);
  return c;
}

void main() {
  test('xterm by default; a choice updates the state and is saved', () async {
    final kv = _FakeKv({});
    final c = _container(kv);
    expect(c.read(terminalPrefsProvider).emulator, TerminalEmulator.xterm);
    await c.read(terminalPrefsProvider.notifier).setEmulator(TerminalEmulator.ghostty);
    expect(c.read(terminalPrefsProvider).emulator, TerminalEmulator.ghostty);
    expect((await TerminalStore(kv).load()).emulator, TerminalEmulator.ghostty);
  });

  test('the controller loads the saved choice', () async {
    final c = _container(_FakeKv({'terminal.emulator': 'ghostty'}));
    c.listen(terminalPrefsProvider, (_, _) {});
    await Future<void>.delayed(const Duration(milliseconds: 10));
    expect(c.read(terminalPrefsProvider).emulator, TerminalEmulator.ghostty);
  });

  test('an unknown stored value falls back to xterm', () async {
    final prefs = await TerminalStore(_FakeKv({'terminal.emulator': 'kitty'})).load();
    expect(prefs.emulator, TerminalEmulator.xterm);
  });

  test('font size: 12 by default, saved, and kept in range', () async {
    final kv = _FakeKv({});
    final c = _container(kv);
    expect(c.read(terminalPrefsProvider).fontSize, 12);
    await c.read(terminalPrefsProvider.notifier).setFontSize(18);
    expect(c.read(terminalPrefsProvider).fontSize, 18);
    expect((await TerminalStore(kv).load()).fontSize, 18);
    for (final (raw, want) in [('99', 40.0), ('1', 6.0), ('big', 12.0)]) {
      final prefs = await TerminalStore(_FakeKv({'terminal.fontSize': raw})).load();
      expect(prefs.fontSize, want, reason: raw);
    }
  });

  test('a late load keeps a font change and still applies the saved emulator', () async {
    final c = _container(_FakeKv({'terminal.emulator': 'ghostty', 'terminal.fontSize': '9'}));
    c.listen(terminalPrefsProvider, (_, _) {});
    await c.read(terminalPrefsProvider.notifier).setFontSize(20);
    await Future<void>.delayed(const Duration(milliseconds: 10));
    final prefs = c.read(terminalPrefsProvider);
    expect(prefs.fontSize, 20);
    expect(prefs.emulator, TerminalEmulator.ghostty);
  });
}
