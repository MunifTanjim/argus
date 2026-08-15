import 'dart:convert';
import 'dart:typed_data';

import '../pairing/gateway_store.dart' show SecureKv;

/// Thrown by [TrustChainStore.load] when the device has anchored a chain before
/// (the "anchored" marker is set) but the stored chain is now missing or
/// undecodable. The caller must fail closed — NOT silently re-TOFU — because a
/// previously-verified device losing its anchor is anomalous (corruption, or an
/// attacker deleting it to force re-anchoring onto a rolled-back chain).
class TrustAnchorLost implements Exception {
  const TrustAnchorLost();
  @override
  String toString() => 'TrustAnchorLost: stored trust anchor is missing or corrupt';
}

/// Persists the last verified trust-log chain (base64) so the client can seed its
/// TrustStore before pulling — the rollback anchor. Web-safe (SecureKv).
class TrustChainStore {
  TrustChainStore(this._kv);

  final SecureKv _kv;
  static const _key = 'e2e_trust_chain';
  // Set once the device has ever persisted a chain, so [load] can tell "first use"
  // (no marker → TOFU) apart from "anchored before but the chain is gone/corrupt"
  // (marker set → fail closed).
  static const _anchoredKey = 'e2e_trust_anchored';

  Future<Uint8List?> load() async {
    final anchored = (await _kv.read(_anchoredKey)) != null;
    final v = await _kv.read(_key);
    if (v != null && v.isNotEmpty) {
      try {
        return Uint8List.fromList(base64.decode(v));
      } catch (_) {
        // Present but undecodable. If we've anchored before, this is a corrupted
        // anchor — fail closed. (Without a prior anchor, treat as first-use TOFU.)
        if (anchored) throw const TrustAnchorLost();
        return null;
      }
    }
    // Chain absent: fail closed if we anchored before, else first-use TOFU.
    if (anchored) throw const TrustAnchorLost();
    return null;
  }

  Future<void> save(Uint8List chain) async {
    await _kv.write(_key, base64.encode(chain));
    await _kv.write(_anchoredKey, '1');
  }

  Future<void> clear() async {
    await _kv.delete(_key);
    await _kv.delete(_anchoredKey);
  }
}
