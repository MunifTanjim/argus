import 'dart:convert';

import '../pairing/gateway_store.dart';
import 'push_provider.dart';

typedef StoredPushTarget = ({PushTarget target, PushLease lease});

/// PushTargetStore persists the device's current [PushTarget] so it survives an
/// app restart. The distributor only re-emits an endpoint when it changes, so
/// without this the in-memory target is lost on relaunch and the gateway is never
/// re-told about it — leaving the device with no registration until the
/// distributor happens to emit again. Reloading on startup lets the controller
/// re-register the known target as soon as the connection is up.
///
/// The lease travels with the target so a relaunch can tell a live endpoint from
/// one that lapsed while the app was closed.
class PushTargetStore {
  PushTargetStore(this._kv);
  final SecureKv _kv;

  static const _key = 'push_target';

  /// The lease is [PushLease.none] for a payload written before expiries were
  /// recorded, or by a backend that has no expiry.
  Future<StoredPushTarget?> load() async {
    final raw = await _kv.read(_key);
    if (raw == null || raw.isEmpty) return null;
    try {
      final m = jsonDecode(raw) as Map<String, dynamic>;
      final endpoint = m['endpoint'] as String?;
      if (endpoint == null || endpoint.isEmpty) return null;
      return (
        target: PushTarget(
          endpoint,
          p256dh: m['p256dh'] as String?,
          auth: m['auth'] as String?,
        ),
        lease: PushLease.restored(m['expires_at'] as int? ?? 0),
      );
    } catch (_) {
      return null;
    }
  }

  Future<void> save(PushTarget t, {PushLease lease = PushLease.none}) =>
      _kv.write(
        _key,
        jsonEncode({
          ...t.toParams(),
          if (lease.isKnown) 'expires_at': lease.expiresAt,
        }),
      );

  Future<void> clear() => _kv.delete(_key);
}
