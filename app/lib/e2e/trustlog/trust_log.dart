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
      for (final s in e.signers) {
        _signers.add(hexEncode(s));
      }
      _disablements = e.disablements;
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
        // Add replacement signers before computing the remaining count.
        for (final r in e.replaces) {
          _signers.add(hexEncode(r));
        }
        // Count remaining signers after revocation.
        var remaining = _signers.length;
        final seenRevoked = <String>{};
        for (final r in e.signers) {
          final rs = hexEncode(r);
          if (_signers.contains(rs) && !seenRevoked.contains(rs)) {
            seenRevoked.add(rs);
            remaining--;
          }
        }
        if (remaining < 1) throw const FormatException('trustlog: revoke-signer would leave zero signers');
        // Remove revoked signers and invalidate their devices.
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
        final commit = await disablementCommitment(e.key ?? Uint8List(0));
        if (!_contains(_disablements, commit)) {
          throw const FormatException('trustlog: disable secret does not match a commitment');
        }
        _disabled = true;
      } else {
        if (e.signer == null) throw FormatException('trustlog: entry $i: missing signer');
        if (!_signers.contains(hexEncode(e.signer!))) {
          throw const FormatException('trustlog: entry not signed by a trusted signer');
        }
        if (e.key == null) throw FormatException('trustlog: entry $i: missing key');
        switch (e.kind) {
          case Kind.addSigner:
            _signers.add(hexEncode(e.key!));
          case Kind.removeSigner:
            final removed = hexEncode(e.key!);
            if (!_signers.contains(removed)) {
              throw const FormatException('trustlog: cannot remove an unknown signer');
            }
            if (_signers.length == 1) {
              throw const FormatException('trustlog: cannot remove the last signer');
            }
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
            if (_devices.contains(hexEncode(e.key!))) {
              throw const FormatException('trustlog: device already authorized');
            }
            _devices.add(hexEncode(e.key!));
            _deviceSigner[hexEncode(e.key!)] = hexEncode(e.signer!);
          case Kind.revokeDevice:
            _devices.remove(hexEncode(e.key!));
            _deviceSigner.remove(hexEncode(e.key!));
          default:
            throw const FormatException('trustlog: unknown entry kind');
        }
      }
    }
    _count++;
    _tip = hashEntry(e);
  }
}
