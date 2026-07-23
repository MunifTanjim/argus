import 'dart:typed_data';

import 'package:cryptography_plus/cryptography_plus.dart' show Ed25519, SimplePublicKey, KeyPairType, Signature;

import '../symmetric_state.dart' show blake2s;
import 'entry.dart';

const int _maxField = 1 << 20;
const int _maxSigners = 1 << 12;
const int _maxDisablements = 1 << 12;
const int _maxCoSigns = 1 << 12;
const int _maxReplaces = 1 << 12;
const int _maxEntries = 1 << 16;

// Writes a 4-byte big-endian count (mirrors Go's binary.BigEndian.PutUint32).
void _count32(BytesBuilder b, int n) {
  final h = Uint8List(4);
  ByteData.sublistView(h).setUint32(0, n, Endian.big);
  b.add(h);
}

/// The chain hash of a full (signed) entry. Covers sigBytes ‖ putField(sig).
/// For KindRevokeSigner, replaces is already in sigBytes; CoSigns are appended
/// after Sig so the chain commits to the full co-signed payload (mirrors Go hashEntry).
Uint8List hashEntry(Entry e) {
  final b = BytesBuilder();
  b.add(sigBytes(e));
  putField(b, e.sig);
  if (e.kind == Kind.revokeSigner) {
    _count32(b, e.coSigns.length);
    for (final cs in e.coSigns) {
      putField(b, cs.signer);
      putField(b, cs.sig);
    }
  }
  return blake2s(b.toBytes());
}

/// Returns the number of distinct valid co-signs from trusted (non-revoked) signers,
/// and whether that count exceeds the number of revoked signers. By default the
/// revoked signers (e.signers) are excluded; when [allowRevoked] is true they may
/// co-sign (KindRevokeSigner with replacements — voluntary rotation).
/// Mirrors Go validCoSigns exactly.
Future<(int, bool)> validCoSigns(Entry e, bool Function(Uint8List) trusted, {bool allowRevoked = false}) async {
  final ed25519 = Ed25519();
  final sb = sigBytes(e);
  // revoked set: signers listed in e.signers (the revoked pubkeys)
  final revoked = <String>{};
  if (!allowRevoked) {
    for (final r in e.signers) {
      revoked.add(String.fromCharCodes(r));
    }
  }
  final seen = <String>{};
  var n = 0;
  for (final cs in e.coSigns) {
    final ks = String.fromCharCodes(cs.signer);
    if (seen.contains(ks) || revoked.contains(ks) || !trusted(cs.signer)) continue;
    if (cs.signer.length != 32) continue;
    final ok = await ed25519.verify(
      sb,
      signature: Signature(cs.sig, publicKey: SimplePublicKey(cs.signer, type: KeyPairType.ed25519)),
    );
    if (!ok) continue;
    seen.add(ks);
    n++;
  }
  return (n, n > e.signers.length);
}

class _Reader {
  _Reader(this._b) : _view = ByteData.sublistView(_b);
  final Uint8List _b;
  final ByteData _view;
  int _off = 0;
  int get remaining => _b.length - _off;

  int readU8() {
    if (remaining < 1) throw const FormatException('trustlog: truncated');
    return _b[_off++];
  }

  int readU32() {
    if (remaining < 4) throw const FormatException('trustlog: truncated');
    final v = _view.getUint32(_off, Endian.big);
    _off += 4;
    return v;
  }

  int readCount(int cap) {
    final n = readU32();
    if (n > cap) throw const FormatException('trustlog: count exceeds cap');
    return n;
  }

  Uint8List? readField() {
    final n = readU32();
    if (n > _maxField) throw const FormatException('trustlog: field exceeds cap');
    if (n == 0) return null;
    if (remaining < n) throw const FormatException('trustlog: truncated field');
    final f = Uint8List.fromList(Uint8List.sublistView(_b, _off, _off + n));
    _off += n;
    return f;
  }
}

Entry _readEntry(_Reader r) {
  final kind = Kind.fromValue(r.readU8());
  if (kind == null) throw const FormatException('trustlog: unknown kind');
  final prev = r.readField();
  final sc = r.readCount(_maxSigners);
  final signers = [for (var i = 0; i < sc; i++) r.readField() ?? Uint8List(0)];
  final dc = r.readCount(_maxDisablements);
  final disablements = [for (var i = 0; i < dc; i++) r.readField() ?? Uint8List(0)];
  final key = r.readField();
  final signer = r.readField();
  // KindRevokeSigner: Replaces count+fields come after Signer (part of sigBytes) and before Sig.
  List<Uint8List> replaces = const [];
  if (kind == Kind.revokeSigner) {
    final rc = r.readCount(_maxReplaces);
    replaces = [for (var i = 0; i < rc; i++) r.readField() ?? Uint8List(0)];
  }
  final sig = r.readField();
  List<CoSign> coSigns = const [];
  if (kind == Kind.revokeSigner) {
    final csc = r.readCount(_maxCoSigns);
    coSigns = [
      for (var i = 0; i < csc; i++)
        CoSign(signer: r.readField() ?? Uint8List(0), sig: r.readField() ?? Uint8List(0))
    ];
  }
  return Entry(
      kind: kind, prev: prev, signers: signers, disablements: disablements,
      key: key, signer: signer, sig: sig, coSigns: coSigns, replaces: replaces);
}

Entry unmarshalEntry(Uint8List b) {
  final r = _Reader(b);
  final e = _readEntry(r);
  if (r.remaining != 0) throw const FormatException('trustlog: trailing bytes after entry');
  return e;
}

List<Entry> unmarshalChain(Uint8List b) {
  final r = _Reader(b);
  final cnt = r.readCount(_maxEntries);
  final entries = <Entry>[];
  for (var i = 0; i < cnt; i++) {
    final raw = r.readField();
    if (raw == null) throw const FormatException('trustlog: empty entry');
    entries.add(unmarshalEntry(raw));
  }
  if (r.remaining != 0) throw const FormatException('trustlog: trailing bytes after chain');
  return entries;
}
