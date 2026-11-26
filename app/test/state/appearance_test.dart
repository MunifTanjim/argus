import 'dart:async';

import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/state/appearance.dart';
import 'package:argus/ui/theme/palettes.dart';
import 'package:argus/ui/theme/tokens.dart';
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

class _ThrowingKv extends _FakeKv {
  @override
  Future<String?> read(String key) async => throw StateError('unavailable');
}

class _GatedKv extends _FakeKv {
  _GatedKv(this.gate);
  final Completer<void> gate;
  @override
  Future<String?> read(String key) async {
    final snapshot = m[key];
    await gate.future;
    return snapshot;
  }
}

ProviderContainer _container(SecureKv kv) => ProviderContainer(
  overrides: [appearanceStoreProvider.overrideWithValue(AppearanceStore(kv))],
);

void main() {
  test('defaults', () {
    const p = AppearancePrefs();
    expect(p.palette, ArgusPaletteId.gruvbox);
    expect(p.mode, ThemeMode.dark);
    expect(p.accent, AccentChoice.paletteDefault);
    expect(p.font, InterfaceFont.sans);
    expect(p.textScale, 1.0);
    expect(p.radius, CornerRadius.medium);
    expect(p.statusStyle, StatusStyle.stripe);
    expect(p.verboseTranscript, isFalse);
  });

  test('corner radius values', () {
    expect(CornerRadius.values.map((r) => r.value), [2, 6, 12]);
  });

  test('each setter persists under its key', () async {
    final kv = _FakeKv();
    final c = _container(kv);
    addTearDown(c.dispose);
    final n = c.read(appearancePrefsProvider.notifier);
    await n.loaded;

    await n.setPalette(ArgusPaletteId.catppuccin);
    await n.setMode(ThemeMode.light);
    await n.setAccent(AccentChoice.blue);
    await n.setFont(InterfaceFont.mono);
    await n.setTextScale(1.15);
    await n.setRadius(CornerRadius.rounded);
    await n.setStatusStyle(StatusStyle.dot);
    await n.setVerboseTranscript(true);

    expect(kv.m, {
      'appearance.palette': 'catppuccin',
      'appearance.mode': 'light',
      'appearance.accent': 'blue',
      'appearance.font': 'mono',
      'appearance.textScale': '1.15',
      'appearance.radius': 'rounded',
      'appearance.statusStyle': 'dot',
      'appearance.verboseTranscript': 'true',
    });
    final s = c.read(appearancePrefsProvider);
    expect(s.palette, ArgusPaletteId.catppuccin);
    expect(s.textScale, 1.15);
    expect(s.statusStyle, StatusStyle.dot);
  });

  test('load reads every key', () async {
    final kv = _FakeKv()
      ..m.addAll({
        'appearance.palette': 'tokyoNight',
        'appearance.mode': 'system',
        'appearance.accent': 'purple',
        'appearance.font': 'mono',
        'appearance.textScale': '1.3',
        'appearance.radius': 'sharp',
        'appearance.statusStyle': 'filled',
        'appearance.verboseTranscript': 'true',
      });
    final p = await AppearanceStore(kv).load();
    expect(p.palette, ArgusPaletteId.tokyoNight);
    expect(p.mode, ThemeMode.system);
    expect(p.accent, AccentChoice.purple);
    expect(p.font, InterfaceFont.mono);
    expect(p.textScale, 1.3);
    expect(p.radius, CornerRadius.sharp);
    expect(p.statusStyle, StatusStyle.filled);
    expect(p.verboseTranscript, isTrue);
  });

  test('unknown or corrupt values fall back per option', () async {
    final kv = _FakeKv()
      ..m.addAll({
        'appearance.palette': 'dracula',
        'appearance.textScale': 'big',
        'appearance.mode': 'light',
      });
    final p = await AppearanceStore(kv).load();
    expect(p.palette, ArgusPaletteId.gruvbox);
    expect(p.textScale, 1.0);
    expect(p.mode, ThemeMode.light);
  });

  test('textScale out of range falls back to 1.0', () async {
    for (final raw in ['1.7', '0.5']) {
      final kv = _FakeKv()..m['appearance.textScale'] = raw;
      expect((await AppearanceStore(kv).load()).textScale, 1.0, reason: raw);
    }
  });

  test('textScale in range snaps to a 5% step', () async {
    final kv = _FakeKv()..m['appearance.textScale'] = '1.12';
    expect((await AppearanceStore(kv).load()).textScale, 1.1);
  });

  test('loaded completes after hydrate', () async {
    final kv = _FakeKv()..m['appearance.font'] = 'mono';
    final c = _container(kv);
    addTearDown(c.dispose);
    await c.read(appearancePrefsProvider.notifier).loaded;
    expect(c.read(appearancePrefsProvider).font, InterfaceFont.mono);
  });

  test('loaded completes and defaults stay when read fails', () async {
    final c = _container(_ThrowingKv());
    addTearDown(c.dispose);
    await c.read(appearancePrefsProvider.notifier).loaded;
    expect(c.read(appearancePrefsProvider).palette, ArgusPaletteId.gruvbox);
  });

  test('setter during pending load wins over loaded value', () async {
    final gate = Completer<void>();
    final kv = _GatedKv(gate)
      ..m.addAll({
        'appearance.palette': 'catppuccin',
        'appearance.font': 'mono',
      });
    final c = _container(kv);
    addTearDown(c.dispose);
    final n = c.read(appearancePrefsProvider.notifier);
    final write = n.setPalette(ArgusPaletteId.nord);
    gate.complete();
    await write;
    await n.loaded;
    final s = c.read(appearancePrefsProvider);
    expect(s.palette, ArgusPaletteId.nord);
    expect(s.font, InterfaceFont.mono);
    expect(kv.m['appearance.palette'], 'nord');
  });
}
