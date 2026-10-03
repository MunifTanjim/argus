import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../pairing/gateway_store.dart';

enum TerminalEmulator {
  xterm('xterm'),
  ghostty('Ghostty');

  const TerminalEmulator(this.label);
  final String label;
}

/// Font size bounds for live screens, shared by Settings and pinch zoom.
const terminalFontSizeMin = 6.0;
const terminalFontSizeMax = 40.0;
const terminalFontSizeDefault = 12.0;

class TerminalPrefs {
  final TerminalEmulator emulator;

  /// The size a live screen starts at; pinch zoom changes only that screen.
  final double fontSize;

  const TerminalPrefs({
    this.emulator = TerminalEmulator.xterm,
    this.fontSize = terminalFontSizeDefault,
  });

  TerminalPrefs copyWith({TerminalEmulator? emulator, double? fontSize}) =>
      TerminalPrefs(
        emulator: emulator ?? this.emulator,
        fontSize: fontSize ?? this.fontSize,
      );
}

class TerminalStore {
  TerminalStore([this._kv = const FlutterSecureKv()]);
  final SecureKv _kv;

  static const _emulatorKey = 'terminal.emulator';
  static const _fontSizeKey = 'terminal.fontSize';

  Future<TerminalPrefs> load() async {
    final emulator = await _kv.read(_emulatorKey);
    final fontSize = double.tryParse(await _kv.read(_fontSizeKey) ?? '');
    return TerminalPrefs(
      emulator: TerminalEmulator.values.firstWhere((e) => e.name == emulator,
          orElse: () => TerminalEmulator.xterm),
      fontSize: (fontSize ?? terminalFontSizeDefault)
          .clamp(terminalFontSizeMin, terminalFontSizeMax),
    );
  }

  Future<void> setEmulator(TerminalEmulator e) => _kv.write(_emulatorKey, e.name);

  Future<void> setFontSize(double size) =>
      _kv.write(_fontSizeKey, size.toString());
}

final terminalStoreProvider = Provider<TerminalStore>((ref) => TerminalStore());

class TerminalPrefsController extends Notifier<TerminalPrefs> {
  @override
  TerminalPrefs build() {
    // Hydrate async.
    _load();
    return const TerminalPrefs();
  }

  // Set by a change made while the load runs, so the load does not undo it.
  var _emulatorChosen = false;
  var _fontSizeChosen = false;

  Future<void> _load() async {
    try {
      final saved = await ref.read(terminalStoreProvider).load();
      state = TerminalPrefs(
        emulator: _emulatorChosen ? state.emulator : saved.emulator,
        fontSize: _fontSizeChosen ? state.fontSize : saved.fontSize,
      );
    } catch (_) {
      // Keep the default on read failure (e.g. secure storage unavailable).
    }
  }

  Future<void> setEmulator(TerminalEmulator e) async {
    _emulatorChosen = true;
    state = state.copyWith(emulator: e);
    try {
      await ref.read(terminalStoreProvider).setEmulator(e);
    } catch (_) {
      // Persist failure is non-fatal.
    }
  }

  Future<void> setFontSize(double size) async {
    _fontSizeChosen = true;
    final s = size.clamp(terminalFontSizeMin, terminalFontSizeMax);
    state = state.copyWith(fontSize: s);
    try {
      await ref.read(terminalStoreProvider).setFontSize(s);
    } catch (_) {
      // Persist failure is non-fatal.
    }
  }
}

final terminalPrefsProvider =
    NotifierProvider<TerminalPrefsController, TerminalPrefs>(
        TerminalPrefsController.new);
