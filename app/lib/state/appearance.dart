import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../pairing/gateway_store.dart';
import '../ui/theme/palettes.dart' show AccentChoice, ArgusPaletteId;
import '../ui/theme/tokens.dart' show AppDensity, StatusStyle;

export '../ui/theme/palettes.dart' show AccentChoice;

enum InterfaceFont { sans, mono }

enum CornerRadius {
  sharp(2),
  medium(6),
  rounded(12);

  const CornerRadius(this.value);
  final double value;
}

const textScaleSteps = [0.9, 1.0, 1.15, 1.3];

/// Persisted appearance preferences. A value object so more options can be
/// added without changing the provider type.
class AppearancePrefs {
  final ArgusPaletteId palette;
  final ThemeMode mode;
  final AccentChoice accent;
  final AppDensity density;
  final InterfaceFont font;
  final double textScale;
  final CornerRadius radius;
  final StatusStyle statusStyle;

  /// Expand thinking/tool runs in the transcript by default.
  final bool verboseTranscript;

  const AppearancePrefs({
    this.palette = ArgusPaletteId.gruvbox,
    this.mode = ThemeMode.dark,
    this.accent = AccentChoice.paletteDefault,
    this.density = AppDensity.compact,
    this.font = InterfaceFont.sans,
    this.textScale = 1.0,
    this.radius = CornerRadius.medium,
    this.statusStyle = StatusStyle.stripe,
    this.verboseTranscript = false,
  });

  AppearancePrefs copyWith({
    ArgusPaletteId? palette,
    ThemeMode? mode,
    AccentChoice? accent,
    AppDensity? density,
    InterfaceFont? font,
    double? textScale,
    CornerRadius? radius,
    StatusStyle? statusStyle,
    bool? verboseTranscript,
  }) => AppearancePrefs(
    palette: palette ?? this.palette,
    mode: mode ?? this.mode,
    accent: accent ?? this.accent,
    density: density ?? this.density,
    font: font ?? this.font,
    textScale: textScale ?? this.textScale,
    radius: radius ?? this.radius,
    statusStyle: statusStyle ?? this.statusStyle,
    verboseTranscript: verboseTranscript ?? this.verboseTranscript,
  );
}

/// Reads/writes appearance prefs through the app's secure KV. Each option is
/// keyed individually so adding one never migrates a blob.
class AppearanceStore {
  AppearanceStore([this._kv = const FlutterSecureKv()]);
  final SecureKv _kv;

  static const _paletteKey = 'appearance.palette';
  static const _modeKey = 'appearance.mode';
  static const _accentKey = 'appearance.accent';
  static const _densityKey = 'appearance.density';
  static const _fontKey = 'appearance.font';
  static const _textScaleKey = 'appearance.textScale';
  static const _radiusKey = 'appearance.radius';
  static const _statusStyleKey = 'appearance.statusStyle';
  static const _verboseTranscriptKey = 'appearance.verboseTranscript';

  static T _enumOr<T extends Enum>(List<T> values, String? raw, T fallback) {
    for (final v in values) {
      if (v.name == raw) return v;
    }
    return fallback;
  }

  static double _scaleOr(String? raw, double fallback) {
    final v = double.tryParse(raw ?? '');
    return v != null && textScaleSteps.contains(v) ? v : fallback;
  }

  // The first screen waits for this load, so the keys are read together.
  Future<AppearancePrefs> load() async {
    const d = AppearancePrefs();
    final [
      palette,
      mode,
      accent,
      font,
      density,
      textScale,
      radius,
      statusStyle,
      verboseTranscript,
    ] = await Future.wait([
      _kv.read(_paletteKey),
      _kv.read(_modeKey),
      _kv.read(_accentKey),
      _kv.read(_fontKey),
      _kv.read(_densityKey),
      _kv.read(_textScaleKey),
      _kv.read(_radiusKey),
      _kv.read(_statusStyleKey),
      _kv.read(_verboseTranscriptKey),
    ]);
    return AppearancePrefs(
      palette: _enumOr(ArgusPaletteId.values, palette, d.palette),
      mode: _enumOr(ThemeMode.values, mode, d.mode),
      accent: _enumOr(AccentChoice.values, accent, d.accent),
      density: _enumOr(AppDensity.values, density, d.density),
      font: _enumOr(InterfaceFont.values, font, d.font),
      textScale: _scaleOr(textScale, d.textScale),
      radius: _enumOr(CornerRadius.values, radius, d.radius),
      statusStyle: _enumOr(StatusStyle.values, statusStyle, d.statusStyle),
      verboseTranscript: verboseTranscript == 'true',
    );
  }

  Future<void> setPalette(ArgusPaletteId v) => _kv.write(_paletteKey, v.name);
  Future<void> setMode(ThemeMode v) => _kv.write(_modeKey, v.name);
  Future<void> setAccent(AccentChoice v) => _kv.write(_accentKey, v.name);
  Future<void> setDensity(AppDensity v) => _kv.write(_densityKey, v.name);
  Future<void> setFont(InterfaceFont v) => _kv.write(_fontKey, v.name);
  Future<void> setTextScale(double v) => _kv.write(_textScaleKey, v.toString());
  Future<void> setRadius(CornerRadius v) => _kv.write(_radiusKey, v.name);
  Future<void> setStatusStyle(StatusStyle v) =>
      _kv.write(_statusStyleKey, v.name);
  Future<void> setVerboseTranscript(bool v) =>
      _kv.write(_verboseTranscriptKey, v ? 'true' : 'false');
}

final appearanceStoreProvider =
    Provider<AppearanceStore>((ref) => AppearanceStore());

enum _Pref {
  palette,
  mode,
  accent,
  density,
  font,
  textScale,
  radius,
  statusStyle,
  verboseTranscript,
}

class AppearanceController extends Notifier<AppearancePrefs> {
  late Completer<void> _loaded;

  // Fields a setter changed before the initial load finished; the load must
  // not overwrite them.
  final _touched = <_Pref>{};

  /// Completes after the first load attempt, whether it succeeded or failed.
  Future<void> get loaded => _loaded.future;

  @override
  AppearancePrefs build() {
    _loaded = Completer<void>();
    // Hydrate async; the app shows its first screen only after this loads.
    _load(_loaded);
    return const AppearancePrefs();
  }

  Future<void> _load(Completer<void> done) async {
    try {
      final l = await ref.read(appearanceStoreProvider).load();
      final c = state;
      bool t(_Pref k) => _touched.contains(k);
      state = AppearancePrefs(
        palette: t(_Pref.palette) ? c.palette : l.palette,
        mode: t(_Pref.mode) ? c.mode : l.mode,
        accent: t(_Pref.accent) ? c.accent : l.accent,
        density: t(_Pref.density) ? c.density : l.density,
        font: t(_Pref.font) ? c.font : l.font,
        textScale: t(_Pref.textScale) ? c.textScale : l.textScale,
        radius: t(_Pref.radius) ? c.radius : l.radius,
        statusStyle: t(_Pref.statusStyle) ? c.statusStyle : l.statusStyle,
        verboseTranscript: t(_Pref.verboseTranscript)
            ? c.verboseTranscript
            : l.verboseTranscript,
      );
    } catch (_) {
      // Keep the default on read failure (e.g. secure storage unavailable).
    } finally {
      _touched.clear();
      done.complete();
    }
  }

  // Optimistic: update memory first; a failed persist only costs the value on
  // the next restart.
  Future<void> _persist(Future<void> Function(AppearanceStore s) write) async {
    try {
      await write(ref.read(appearanceStoreProvider));
    } catch (_) {
      // Persist failure is non-fatal.
    }
  }

  Future<void> setPalette(ArgusPaletteId v) {
    _touched.add(_Pref.palette);
    state = state.copyWith(palette: v);
    return _persist((s) => s.setPalette(v));
  }

  Future<void> setMode(ThemeMode v) {
    _touched.add(_Pref.mode);
    state = state.copyWith(mode: v);
    return _persist((s) => s.setMode(v));
  }

  Future<void> setAccent(AccentChoice v) {
    _touched.add(_Pref.accent);
    state = state.copyWith(accent: v);
    return _persist((s) => s.setAccent(v));
  }

  Future<void> setDensity(AppDensity v) {
    _touched.add(_Pref.density);
    state = state.copyWith(density: v);
    return _persist((s) => s.setDensity(v));
  }

  Future<void> setFont(InterfaceFont v) {
    _touched.add(_Pref.font);
    state = state.copyWith(font: v);
    return _persist((s) => s.setFont(v));
  }

  Future<void> setTextScale(double v) {
    _touched.add(_Pref.textScale);
    state = state.copyWith(textScale: v);
    return _persist((s) => s.setTextScale(v));
  }

  Future<void> setRadius(CornerRadius v) {
    _touched.add(_Pref.radius);
    state = state.copyWith(radius: v);
    return _persist((s) => s.setRadius(v));
  }

  Future<void> setStatusStyle(StatusStyle v) {
    _touched.add(_Pref.statusStyle);
    state = state.copyWith(statusStyle: v);
    return _persist((s) => s.setStatusStyle(v));
  }

  Future<void> setVerboseTranscript(bool v) {
    _touched.add(_Pref.verboseTranscript);
    state = state.copyWith(verboseTranscript: v);
    return _persist((s) => s.setVerboseTranscript(v));
  }
}

final appearancePrefsProvider =
    NotifierProvider<AppearanceController, AppearancePrefs>(
        AppearanceController.new);
