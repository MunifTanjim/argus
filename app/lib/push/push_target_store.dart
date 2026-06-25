import 'dart:convert';

import '../pairing/gateway_store.dart';
import 'push_provider.dart';

/// PushTargetStore persists the device's current [PushTarget] so it survives an
/// app restart. The distributor only re-emits an endpoint when it changes, so
/// without this the in-memory target is lost on relaunch and the gateway is never
/// re-told about it — leaving the device with no registration until the
/// distributor happens to emit again. Reloading on startup lets the controller
/// re-register the known target as soon as the connection is up.
class PushTargetStore {
  PushTargetStore(this._kv);
  final SecureKv _kv;

  static const _key = 'push_target';

  /// The persisted target, or null if none is stored or the payload is unusable.
  Future<PushTarget?> load() async {
    final raw = await _kv.read(_key);
    if (raw == null || raw.isEmpty) return null;
    try {
      final m = jsonDecode(raw) as Map<String, dynamic>;
      final endpoint = m['endpoint'] as String?;
      if (endpoint == null || endpoint.isEmpty) return null;
      return PushTarget(
        endpoint,
        p256dh: m['p256dh'] as String?,
        auth: m['auth'] as String?,
      );
    } catch (_) {
      return null;
    }
  }

  Future<void> save(PushTarget t) => _kv.write(_key, jsonEncode(t.toParams()));

  Future<void> clear() => _kv.delete(_key);
}
