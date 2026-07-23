import 'dart:typed_data';

import 'codec.dart';
import 'entry.dart';
import 'trust_log.dart';

bool _eq(Uint8List a, Uint8List b) {
  if (a.length != b.length) return false;
  for (var i = 0; i < a.length; i++) {
    if (a[i] != b[i]) return false;
  }
  return true;
}

/// Holds a verified chain pinned to an out-of-band genesis hash. ingest adopts a
/// candidate only if it is a same-genesis, prefix-preserving, strictly-longer,
/// fully-verified extension — the rollback/fork/tamper defense over the gateway.
///
/// **Authenticity vs recency**: the pinned genesis provides AUTHENTICITY (the
/// chain is genuinely signed by the declared signers), not RECENCY. With no prior
/// or persisted chain, a malicious gateway can serve a genuinely-signed but STALE
/// chain — one that predates a revoke or disable — and this store will accept it.
/// Revocation is therefore not rollback-safe until a future persistence layer
/// (F6) re-runs genesis-pinned ingest on the chain seeded from disk, making
/// regression behind the last-seen tip detectable.
class TrustStore {
  TrustStore(Uint8List genesisHash)
      : _genesisHash = Uint8List.fromList(genesisHash),
        _tofu = false;

  /// A genesis-unpinned store (Trust-On-First-Use): the first ingest adopts a
  /// fully-verified chain and pins its genesis hash; later ingests are pinned.
  TrustStore.tofu()
      : _genesisHash = null,
        _tofu = true;

  Uint8List? _genesisHash; // set on the first TOFU adopt
  final bool _tofu;
  TrustLog? _log;
  List<Entry>? _entries;
  Uint8List? _chainBytes;

  bool get disabled => _log?.disabled ?? false;
  Uint8List? get tip => _log?.tip;
  List<Uint8List>? get signers => _log?.signers;
  Uint8List? get chainBytes => _chainBytes;
  bool deviceAuthorized(List<int> pub) => _log?.deviceAuthorized(pub) ?? false;

  /// Whether locked-mode enforcement applies. A pinned store is always locked
  /// (constructed knowing the network is locked → fail-closed). A TOFU store is
  /// locked only after it has adopted a chain (an empty first pull ⇒ open network).
  bool get locked => _tofu ? _log != null : true;

  /// Returns whether the verified tip advanced. Identical chain → false (no-op);
  /// a rollback/fork/tamper/wrong-genesis chain throws and leaves state untouched.
  Future<bool> ingest(Uint8List chainBytes) async {
    final entries = unmarshalChain(chainBytes);
    if (entries.isEmpty) throw const FormatException('trustlog: empty chain');
    if (_genesisHash == null) {
      // TOFU first adopt: verify the chain internally (Load), then pin its genesis hash.
      final cand = await TrustLog.load(entries);
      final gh = Uint8List.fromList(hashEntry(entries.first));
      _log = cand;
      _entries = entries;
      _genesisHash = gh;
      _chainBytes = Uint8List.fromList(chainBytes);
      return true;
    }
    if (!_eq(hashEntry(entries.first), _genesisHash!)) {
      throw const FormatException('trustlog: candidate genesis does not match pinned hash');
    }
    final cand = await TrustLog.load(entries); // verifies sigs, links, signer trust
    final cur = _entries;
    if (cur != null) {
      if (entries.length < cur.length) {
        throw const FormatException('trustlog: candidate shorter than current (rollback)');
      }
      for (var i = 0; i < cur.length; i++) {
        if (!_eq(hashEntry(cur[i]), hashEntry(entries[i]))) {
          throw const FormatException('trustlog: candidate diverges (fork)');
        }
      }
      if (entries.length == cur.length) return false; // identical: no-op
    }
    _log = cand;
    _entries = entries;
    _chainBytes = Uint8List.fromList(chainBytes);
    return true;
  }
}
