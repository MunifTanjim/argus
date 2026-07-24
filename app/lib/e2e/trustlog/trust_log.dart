import 'dart:typed_data';

import 'package:cryptography_plus/cryptography_plus.dart';

import '../bytes.dart' show bytesEqual, compareBytes, hexDecode, hexEncode;
import 'codec.dart' show hashEntry, validCoSigns;
import 'disablement.dart';
import 'entry.dart';

final Ed25519 _ed25519 = Ed25519();

bool _contains(List<Uint8List> set, Uint8List b) => set.any((s) => bytesEqual(s, b));

Future<bool> _verifySig(Entry e) async {
  final signer = e.signer, sig = e.sig;
  if (signer == null || signer.length != 32 || sig == null) return false;
  return _ed25519.verify(sigBytes(e),
      signature: Signature(sig, publicKey: SimplePublicKey(signer, type: KeyPairType.ed25519)));
}

/// A verified, folded trust-log chain. Load rejects tampering, reordering,
/// rollback onto a bad link, and edits by an untrusted signer. The caller must
/// independently trust the genesis (pin its tip) — load proves the rest follows.
class TrustLog {
  final Set<String> _signers = {};
  final Set<String> _devices = {};
  final Map<String, String> _deviceSigner = {};
  List<Uint8List> _disablements = const [];
  bool _disabled = false;
  Uint8List _tip = Uint8List(0);
  int _count = 0;

  bool deviceAuthorized(List<int> pub) => _devices.contains(hexEncode(pub));
  bool signerTrusted(List<int> pub) => _signers.contains(hexEncode(pub));
  bool get disabled => _disabled;
  Uint8List get tip => _tip;

  // _sortedBytes decodes a hex set to bytes in lexicographic order.
  static List<Uint8List> _sortedBytes(Set<String> hexSet) =>
      hexSet.map(hexDecode).toList()..sort(compareBytes);

  List<Uint8List> get signers => _sortedBytes(_signers);
  List<Uint8List> get devices => _sortedBytes(_devices);

  /// Returns the signer set as hex strings (for efficient lookup in fork-choice).
  Set<String> get signerHexSet => Set.unmodifiable(_signers);

  static Future<TrustLog> load(List<Entry> entries) async {
    final l = TrustLog();
    for (var i = 0; i < entries.length; i++) {
      await l._apply(entries[i], i);
    }
    if (l._count == 0) throw const FormatException('trustlog: empty chain');
    return l;
  }

  Future<void> _apply(Entry e, int i) async {
    await _verify(e, i);
    _fold(e);
  }

  /// _verify runs every read-only check for e against the current state.
  /// It MUST NOT mutate any field (_signers, _devices, _deviceSigner,
  /// _disablements, _disabled, _count, _tip).
  Future<void> _verify(Entry e, int i) async {
    // KindRevokeSigner is authorized by co-signs, not a single Signer+Sig — skip verifySig.
    if (e.kind != Kind.revokeSigner && !await _verifySig(e)) {
      throw FormatException('trustlog: entry $i: bad signature');
    }
    if (e.kind == Kind.genesis) {
      if (_count != 0) throw const FormatException('trustlog: genesis must be first');
      if (e.signers.isEmpty) throw const FormatException('trustlog: genesis needs a signer');
      if (e.prev != null) throw const FormatException('trustlog: genesis has no prev');
      final signer = e.signer!;
      if (!_contains(e.signers, signer)) {
        throw const FormatException('trustlog: genesis signer not in its set');
      }
    } else {
      if (_count == 0) throw const FormatException('trustlog: first entry must be genesis');
      final prev = e.prev;
      if (prev == null || !bytesEqual(prev, _tip)) {
        throw const FormatException('trustlog: entry does not extend tip');
      }
      if (_disabled) throw const FormatException('trustlog: disabled; no further entries');
      if (e.kind == Kind.revokeSigner) {
        // Authorized by co-signs from signers trusted at the current head.
        // With replacements, the revoked signers may also co-sign (voluntary rotation).
        final (_, ok) = await validCoSigns(e, (pub) => _signers.contains(hexEncode(pub)),
            allowRevoked: e.replaces.isNotEmpty);
        if (!ok) throw const FormatException('trustlog: revoke-signer lacks enough valid co-signs');
        // Read-only remaining-signer count: mirrors Go's remainingAfterRevokeWith.
        // Compute merged set = _signers ∪ distinct(replaces), then subtract
        // distinct revoked signers present in the merged set.
        final replaceHexes = <String>{};
        for (final r in e.replaces) {
          replaceHexes.add(hexEncode(r));
        }
        var withReplaces = _signers.length;
        for (final rh in replaceHexes) {
          if (!_signers.contains(rh)) withReplaces++;
        }
        final seenRevoked = <String>{};
        var remaining = withReplaces;
        for (final r in e.signers) {
          final rs = hexEncode(r);
          if ((_signers.contains(rs) || replaceHexes.contains(rs)) &&
              !seenRevoked.contains(rs)) {
            seenRevoked.add(rs);
            remaining--;
          }
        }
        if (remaining < 1) {
          throw const FormatException('trustlog: revoke-signer would leave zero signers');
        }
      } else if (e.kind == Kind.disable) {
        final commit = await disablementCommitment(e.key ?? Uint8List(0));
        if (!_contains(_disablements, commit)) {
          throw const FormatException('trustlog: disable secret does not match a commitment');
        }
      } else {
        if (e.signer == null) throw FormatException('trustlog: entry $i: missing signer');
        if (!_signers.contains(hexEncode(e.signer!))) {
          throw const FormatException('trustlog: entry not signed by a trusted signer');
        }
        if (e.key == null) throw FormatException('trustlog: entry $i: missing key');
        switch (e.kind) {
          case Kind.addSigner:
            break; // no additional checks
          case Kind.removeSigner:
            final removed = hexEncode(e.key!);
            if (!_signers.contains(removed)) {
              throw const FormatException('trustlog: cannot remove an unknown signer');
            }
            if (_signers.length == 1) {
              throw const FormatException('trustlog: cannot remove the last signer');
            }
          case Kind.authorizeDevice:
            if (_devices.contains(hexEncode(e.key!))) {
              throw const FormatException('trustlog: device already authorized');
            }
          case Kind.revokeDevice:
            break; // no additional checks
          default:
            throw const FormatException('trustlog: unknown entry kind');
        }
      }
    }
  }

  /// _fold applies e's state transitions. It MUST NOT check anything or throw —
  /// callers guarantee e was already verified (_apply calls _verify first;
  /// foldOnly folds an already-verified entry from an adopted chain).
  /// Always appends: _count++ and _tip = hashEntry(e).
  void _fold(Entry e) {
    if (e.kind == Kind.genesis) {
      for (final s in e.signers) {
        _signers.add(hexEncode(s));
      }
      _disablements = e.disablements;
    } else if (e.kind == Kind.revokeSigner) {
      // Add replacement signers before removing revoked ones (mirrors Go fold order).
      for (final r in e.replaces) {
        _signers.add(hexEncode(r));
      }
      // Remove revoked signers and retroactively invalidate their authorized devices.
      for (final r in e.signers) {
        final rs = hexEncode(r);
        _signers.remove(rs);
        final toDrop = _deviceSigner.entries
            .where((en) => en.value == rs)
            .map((en) => en.key)
            .toList();
        for (final dev in toDrop) {
          _devices.remove(dev);
          _deviceSigner.remove(dev);
        }
      }
    } else if (e.kind == Kind.disable) {
      _disabled = true;
    } else {
      switch (e.kind) {
        case Kind.addSigner:
          _signers.add(hexEncode(e.key!));
        case Kind.removeSigner:
          final removed = hexEncode(e.key!);
          _signers.remove(removed);
          _deviceSigner.entries
              .where((en) => en.value == removed)
              .map((en) => en.key)
              .toList()
              .forEach((dev) {
            _devices.remove(dev);
            _deviceSigner.remove(dev);
          });
        case Kind.authorizeDevice:
          _devices.add(hexEncode(e.key!));
          _deviceSigner[hexEncode(e.key!)] = hexEncode(e.signer!);
        case Kind.revokeDevice:
          _devices.remove(hexEncode(e.key!));
          _deviceSigner.remove(hexEncode(e.key!));
        default:
          break; // unreachable: _verify already rejected unknown kinds
      }
    }
    _count++;
    _tip = hashEntry(e);
  }

  /// Fold-only entry point: applies e's state transitions without re-verifying.
  /// Used by trust_store.dart's foldSignersAt (fork-point signer recovery) and
  /// exposed for the differential test. Callers must only pass entries from an
  /// already-verified chain.
  void foldOnly(Entry e) => _fold(e);
}
