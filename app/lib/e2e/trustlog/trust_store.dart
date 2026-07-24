import 'dart:typed_data';

import '../bytes.dart' show bytesEqual, compareBytes, hexEncode;
import 'codec.dart' show hashEntry, unmarshalChain, validCoSigns;
import 'entry.dart';
import 'trust_log.dart';

/// foldSignersAt replays the already-verified prefix entries[0:p] using fold
/// only (no signature/quorum re-verification — the prefix was verified when
/// adopted) and returns the signer set trusted at that fork point.
Future<Set<String>> _foldSignersAt(List<Entry> entries, int p) async {
  if (p == 0) return const {};
  final l = TrustLog();
  for (var i = 0; i < p; i++) {
    l.foldOnly(entries[i]);
  }
  return l.signerHexSet;
}

/// Exposed for the differential test: same result as _foldSignersAt without
/// going through a public Store ingest cycle.
Future<Set<String>> foldSignersAtForTest(List<Entry> entries, int p) =>
    _foldSignersAt(entries, p);

/// weightAtFork scores a first-diverging entry using ONLY signers trusted at the
/// fork point. A co-signed revoke counts its distinct valid co-signers in that set;
/// post-fork puppets are absent → 0. Any other entry weighs 1 iff its single signer
/// is trusted at the fork point.
Future<int> _weightAtFork(Entry e, Set<String> forkSigners) async {
  if (e.kind == Kind.revokeSigner) {
    final (n, _) = await validCoSigns(e, (pub) => forkSigners.contains(hexEncode(pub)));
    return n;
  }
  final signer = e.signer;
  if (signer != null && forkSigners.contains(hexEncode(signer))) return 1;
  return 0;
}

/// isRemoval reports whether e removes trust — the preferred sibling on a weight tie.
bool _isRemoval(Entry e) => e.kind == Kind.revokeSigner || e.kind == Kind.removeSigner;

/// forkChoice decides whether to adopt cand over the already-verified cur. Both
/// slices are fully-verified chains sharing the pinned genesis (cur[0]==cand[0]).
///
/// Rules (mirrors Go forkChoice exactly):
///   - Linear: cand prefix-preserves cur and is longer → adopt; identical → no-op;
///     cand is a strict prefix of cur (shorter, no divergence) → keep cur.
///   - True divergence: resolved at the FORK POINT. Each first-diverging entry is
///     weighed ONLY by signers trusted at the fork point. Higher weight wins; tie →
///     prefer a removal; tie → lexicographically-lowest hashEntry of the first-
///     diverging entry. Every divergence resolves deterministically.
Future<bool> _forkChoice(List<Entry> cur, List<Entry> cand) async {
  var p = 0;
  while (p < cur.length && p < cand.length && bytesEqual(hashEntry(cur[p]), hashEntry(cand[p]))) {
    p++;
  }
  if (p == cur.length) {
    // cand extends (or equals) cur — adopt iff strictly longer.
    return cand.length > p;
  }
  if (p == cand.length) {
    // cand is a strict prefix of cur — keep cur (no-op).
    return false;
  }
  // True divergence at index p. Fold the signer set from the shared prefix.
  final forkSigners = await _foldSignersAt(cur, p);
  final wcur = await _weightAtFork(cur[p], forkSigners);
  final wcand = await _weightAtFork(cand[p], forkSigners);
  if (wcand != wcur) return wcand > wcur;
  // Tie on weight → prefer a removal.
  final rcur = _isRemoval(cur[p]);
  final rcand = _isRemoval(cand[p]);
  if (rcur != rcand) return rcand;
  // Final tie-break: globally-lowest first-diverging-entry hash.
  return compareBytes(hashEntry(cand[p]), hashEntry(cur[p])) < 0;
}

/// Holds a verified chain pinned to an out-of-band genesis hash. ingest adopts a
/// candidate that is a same-genesis, fully-verified linear extension, or — on a
/// true fork — the winner of the fork-point resolution rule (the sibling first-
/// diverging entry with more weight from signers trusted at the fork point; a co-
/// signed key revocation beats a plain branch even when shorter).
///
/// **Authenticity vs recency**: the pinned genesis provides AUTHENTICITY (the
/// chain is genuinely signed by the declared signers), not RECENCY. With no prior
/// or persisted chain, a malicious gateway can serve a genuinely-signed but STALE
/// chain — one that predates a revoke or disable — and this store will accept it.
/// Revocation is therefore not rollback-safe until a future persistence layer
/// re-runs genesis-pinned ingest on the chain seeded from disk.
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
  List<Uint8List>? get devices => _log?.devices;
  Uint8List? get chainBytes => _chainBytes;
  bool deviceAuthorized(List<int> pub) => _log?.deviceAuthorized(pub) ?? false;
  bool signerTrusted(List<int> pub) => _log?.signerTrusted(pub) ?? false;

  /// Whether locked-mode enforcement applies. A pinned store is always locked
  /// (constructed knowing the network is locked → fail-closed). A TOFU store is
  /// locked only after it has adopted a chain (an empty first pull ⇒ open network).
  bool get locked => _tofu ? _log != null : true;

  /// Ingest decodes, verifies, and adopts a candidate chain. Adopts a linear
  /// extension, resolves a true fork via forkChoice (every divergence resolves
  /// deterministically), and is a no-op for an identical, strict-prefix, or
  /// losing candidate. Returns whether the verified tip advanced.
  Future<bool> ingest(Uint8List chainBytes) async {
    // Fast path: an identical re-ingest of the already-adopted chain (the common
    // case — the gateway echoes a node's own chain every sync tick) is a no-op.
    // The bytes match one already verified, so skip the full-chain re-verify
    // (async Ed25519 per entry + Argon2id for any disablement) and fork-choice walk.
    final cb = _chainBytes;
    if (_log != null && cb != null && bytesEqual(chainBytes, cb)) {
      return false;
    }
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
    // Cheap genesis-pin check first — reject a wrong-genesis chain before the
    // expensive full-chain signature verification in Load.
    if (!bytesEqual(hashEntry(entries.first), _genesisHash!)) {
      throw const FormatException('trustlog: candidate genesis does not match pinned hash');
    }
    final cand = await TrustLog.load(entries); // verifies sigs, links, signer trust
    final cur = _entries;
    if (cur != null) {
      // Disablement dominance (mirrors Go store.Ingest): a Load-verified disabled
      // chain is break-glass and beats any non-disabled competitor sharing the
      // genesis, regardless of fork-choice weight. A disable is authorized by a
      // genesis-committed secret preimage (not signer votes) and is terminal, so
      // this grants no new power — but it is required because the chain is persisted
      // (not purged) on disable, so a non-disabled fork must never roll it back.
      final curDisabled = _log?.disabled ?? false;
      final candDisabled = cand.disabled;
      if (curDisabled == candDisabled) {
        final adopt = await _forkChoice(cur, entries);
        if (!adopt) return false;
      } else if (curDisabled) {
        // Current disabled, candidate not: never roll back a disablement.
        return false;
      }
      // else: candidate disabled, current not → dominates → adopt (fall through).
    }
    _log = cand;
    _entries = entries;
    _chainBytes = Uint8List.fromList(chainBytes);
    return true;
  }
}
