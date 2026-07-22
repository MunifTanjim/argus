import 'dart:convert';
import 'dart:typed_data';

import '../pairing/gateway_store.dart' show SecureKv;

/// Persists the last verified trust-log chain (base64) so the client can seed its
/// TrustStore before pulling — the rollback anchor. Web-safe (SecureKv).
class TrustChainStore {
  TrustChainStore(this._kv);

  final SecureKv _kv;
  static const _key = 'e2e_trust_chain';

  Future<Uint8List?> load() async {
    final v = await _kv.read(_key);
    if (v == null || v.isEmpty) return null;
    try {
      return Uint8List.fromList(base64.decode(v));
    } catch (_) {
      return null; // corrupt: treat as absent (fail-closed; re-pulled + re-anchored)
    }
  }

  Future<void> save(Uint8List chain) => _kv.write(_key, base64.encode(chain));
  Future<void> clear() => _kv.delete(_key);
}
