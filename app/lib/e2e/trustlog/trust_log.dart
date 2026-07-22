import 'dart:typed_data';

import 'package:cryptography_plus/cryptography_plus.dart';

import 'codec.dart';
import 'disablement.dart';
import 'entry.dart';

final Ed25519 _ed25519 = Ed25519();

String _hex(List<int> b) {
  final sb = StringBuffer();
  for (final x in b) {
    sb.write(x.toRadixString(16).padLeft(2, '0'));
  }
  return sb.toString();
}

bool _eq(Uint8List a, Uint8List b) {
  if (a.length != b.length) return false;
  for (var i = 0; i < a.length; i++) {
    if (a[i] != b[i]) return false;
  }
  return true;
}

bool _contains(List<Uint8List> set, Uint8List b) => set.any((s) => _eq(s, b));

Future<bool> _verifySig(Entry e) async {
  final signer = e.signer, sig = e.sig;
  if (signer == null || signer.length != 32 || sig == null) return false;
  return _ed25519.verify(sigBytes(e),
      signature: Signature(sig, publicKey: SimplePublicKey(signer, type: KeyPairType.ed25519)));
}

/// A verified, folded trust-log chain. Load rejects tampering, reordering,
/// rollback onto a bad link, and edits by an untrusted signer. The caller must
/// independently trust the genesis (pin its head) — load proves the rest follows.
class TrustLog {
  final Set<String> _signers = {};
  final Set<String> _devices = {};
  List<Uint8List> _disablements = const [];
  bool _disabled = false;
  Uint8List _head = Uint8List(0);
  int _count = 0;

  bool deviceAuthorized(List<int> pub) => _devices.contains(_hex(pub));
  bool get disabled => _disabled;
  Uint8List get head => _head;
  List<Uint8List> get signers =>
      _signers.map((h) => Uint8List.fromList([for (var i = 0; i < h.length; i += 2) int.parse(h.substring(i, i + 2), radix: 16)])).toList();

  static Future<TrustLog> load(List<Entry> entries) async {
    final l = TrustLog();
    for (var i = 0; i < entries.length; i++) {
      await l._apply(entries[i], i);
    }
    if (l._count == 0) throw const FormatException('trustlog: empty chain');
    return l;
  }

  Future<void> _apply(Entry e, int i) async {
    if (!await _verifySig(e)) throw FormatException('trustlog: entry $i: bad signature');
    if (e.kind == Kind.genesis) {
      if (_count != 0) throw const FormatException('trustlog: genesis must be first');
      if (e.signers.isEmpty) throw const FormatException('trustlog: genesis needs a signer');
      if (e.prev != null) throw const FormatException('trustlog: genesis has no prev');
      final signer = e.signer!;
      if (!_contains(e.signers, signer)) {
        throw const FormatException('trustlog: genesis signer not in its set');
      }
      for (final s in e.signers) {
        _signers.add(_hex(s));
      }
      _disablements = e.disablements;
    } else {
      if (_count == 0) throw const FormatException('trustlog: first entry must be genesis');
      final prev = e.prev;
      if (prev == null || !_eq(prev, _head)) {
        throw const FormatException('trustlog: entry does not extend head');
      }
      if (_disabled) throw const FormatException('trustlog: disabled; no further entries');
      if (e.kind == Kind.disable) {
        final commit = await disablementCommitment(e.key ?? Uint8List(0));
        if (!_contains(_disablements, commit)) {
          throw const FormatException('trustlog: disable secret does not match a commitment');
        }
        _disabled = true;
      } else {
        if (e.signer == null) throw FormatException('trustlog: entry $i: missing signer');
        if (!_signers.contains(_hex(e.signer!))) {
          throw const FormatException('trustlog: entry not signed by a trusted signer');
        }
        if (e.key == null) throw FormatException('trustlog: entry $i: missing key');
        switch (e.kind) {
          case Kind.addSigner:
            _signers.add(_hex(e.key!));
          case Kind.removeSigner:
            if (!_signers.contains(_hex(e.key!))) {
              throw const FormatException('trustlog: cannot remove an unknown signer');
            }
            if (_signers.length == 1) {
              throw const FormatException('trustlog: cannot remove the last signer');
            }
            _signers.remove(_hex(e.key!));
          case Kind.authorizeDevice:
            _devices.add(_hex(e.key!));
          case Kind.revokeDevice:
            _devices.remove(_hex(e.key!));
          default:
            throw const FormatException('trustlog: unknown entry kind');
        }
      }
    }
    _count++;
    _head = hashEntry(e);
  }
}
