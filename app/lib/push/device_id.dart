import 'dart:math';

import '../pairing/gateway_store.dart';

/// DeviceIdStore provides a stable per-install id used to key the device's push
/// registration on the gateway (so re-registration replaces the prior endpoint).
/// Generated once and persisted.
class DeviceIdStore {
  DeviceIdStore(this._kv);
  final SecureKv _kv;

  static const _key = 'push_device_id';

  Future<String> getOrCreate() async {
    final existing = await _kv.read(_key);
    if (existing != null && existing.isNotEmpty) return existing;
    final rnd = Random.secure();
    final id = List<int>.generate(16, (_) => rnd.nextInt(256))
        .map((b) => b.toRadixString(16).padLeft(2, '0'))
        .join();
    await _kv.write(_key, id);
    return id;
  }
}
