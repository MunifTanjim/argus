import '../pairing/gateway_store.dart';

/// The PausedUntil sentinel for "off until the user turns it back on". A
/// far-future RFC3339 timestamp keeps the server's send-time check a single
/// comparison. Mirrors the pauseIndefinite constant in the Go push package.
const pauseIndefinite = '9999-12-31T23:59:59Z';

/// Persists this device's push pause preference across app restarts. The app is
/// authoritative: it re-applies the stored preference to every gateway on connect
/// (via push.register) and on toggle (push.setPause), so a node that never heard
/// the pause still honors it.
///
/// The stored value is an RFC3339 timestamp: [pauseIndefinite] for an indefinite
/// pause, or a specific time for a timed pause. Absence means enabled.
class PausePreferenceStore {
  PausePreferenceStore(this._kv);
  final SecureKv _kv;

  static const _key = 'push_pause_until';

  Future<String?> load() async {
    final raw = await _kv.read(_key);
    if (raw == null || raw.isEmpty) return null;
    return raw;
  }

  Future<void> save(String pausedUntil) => _kv.write(_key, pausedUntil);

  Future<void> clear() => _kv.delete(_key);
}
