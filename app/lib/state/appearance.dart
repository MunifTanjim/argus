import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../pairing/gateway_store.dart';

/// Persisted appearance preferences. A value object (not a bare bool) so more
/// options can be added without changing the provider type.
class AppearancePrefs {
  final bool collapseToolCalls;

  const AppearancePrefs({this.collapseToolCalls = false});

  AppearancePrefs copyWith({bool? collapseToolCalls}) => AppearancePrefs(
        collapseToolCalls: collapseToolCalls ?? this.collapseToolCalls,
      );
}

/// Reads/writes appearance prefs through the app's secure KV. Each option is
/// keyed individually so adding one never migrates a blob.
class AppearanceStore {
  AppearanceStore([this._kv = const FlutterSecureKv()]);
  final SecureKv _kv;

  static const _collapseToolCallsKey = 'appearance.collapseToolCalls';

  Future<AppearancePrefs> load() async {
    final raw = await _kv.read(_collapseToolCallsKey);
    return AppearancePrefs(collapseToolCalls: raw == 'true');
  }

  Future<void> setCollapseToolCalls(bool v) =>
      _kv.write(_collapseToolCallsKey, v ? 'true' : 'false');
}

final appearanceStoreProvider =
    Provider<AppearanceStore>((ref) => AppearanceStore());

class AppearanceController extends Notifier<AppearancePrefs> {
  @override
  AppearancePrefs build() {
    // Hydrate async; a one-frame default before storage loads is acceptable.
    _load();
    return const AppearancePrefs();
  }

  Future<void> _load() async {
    try {
      state = await ref.read(appearanceStoreProvider).load();
    } catch (_) {
      // Keep the default on read failure (e.g. secure storage unavailable).
    }
  }

  Future<void> setCollapseToolCalls(bool v) async {
    // Optimistic: update memory first; a failed persist only costs the value
    // on the next restart.
    state = state.copyWith(collapseToolCalls: v);
    try {
      await ref.read(appearanceStoreProvider).setCollapseToolCalls(v);
    } catch (_) {
      // Persist failure is non-fatal.
    }
  }
}

final appearancePrefsProvider =
    NotifierProvider<AppearanceController, AppearancePrefs>(
        AppearanceController.new);
