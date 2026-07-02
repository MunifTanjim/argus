import 'dart:convert';

import '../pairing/gateway_store.dart';

/// Persists pinned SSH host-key fingerprints, keyed by "host:port". A host may
/// serve several key types and the negotiated type can differ between connects,
/// so each type is pinned independently (mirroring OpenSSH's per-type
/// known_hosts). The stored value is a JSON map of key type -> fingerprint.
class HostKeyStore {
  HostKeyStore(this._kv);
  final SecureKv _kv;

  String _k(String hostPort) => 'ssh_hostkey_$hostPort';

  Future<Map<String, String>> _read(String hostPort) async {
    final raw = await _kv.read(_k(hostPort));
    if (raw == null || raw.isEmpty) return {};
    // Tolerate a legacy/corrupt value: pre-fix versions stored a bare
    // fingerprint string, not JSON. Never throw here — this runs inside
    // onVerifyHostKey, where a throw aborts the handshake as an opaque
    // "connection closed before authentication". Treat it as unpinned so TOFU
    // re-pins in the current per-type format.
    try {
      final decoded = jsonDecode(raw);
      if (decoded is Map) return decoded.cast<String, String>();
    } on FormatException {
      // fall through
    }
    return {};
  }

  Future<String?> pinned(String hostPort, String type) async =>
      (await _read(hostPort))[type];

  Future<void> pin(String hostPort, String type, String fingerprint) async {
    final m = await _read(hostPort)..[type] = fingerprint;
    await _kv.write(_k(hostPort), jsonEncode(m));
  }

  /// Drops all pinned types for a host so the next connect re-pins.
  Future<void> forget(String hostPort) => _kv.delete(_k(hostPort));
}

enum HostKeyDecision { accept, reject }

/// Trust-on-first-use: pin an unseen (host, type), accept a matching one, reject
/// a changed one (a changed key is never silently accepted or re-pinned). With
/// [pinUnseen] false, an unseen key is accepted for this session but not
/// persisted — used by "Test connection" so a probe never mutates trust state.
Future<HostKeyDecision> verifyHostKey(
  HostKeyStore store,
  String hostPort,
  String type,
  String fingerprint, {
  bool pinUnseen = true,
}) async {
  final existing = await store.pinned(hostPort, type);
  if (existing == null) {
    if (pinUnseen) await store.pin(hostPort, type, fingerprint);
    return HostKeyDecision.accept;
  }
  return existing == fingerprint
      ? HostKeyDecision.accept
      : HostKeyDecision.reject;
}
