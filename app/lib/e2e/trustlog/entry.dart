import 'dart:typed_data';

import '../bytes.dart' show putUint32;

/// Trust-log entry kinds (values match Go trustlog.Kind).
enum Kind {
  genesis(1), addSigner(2), removeSigner(3), authorizeDevice(4), revokeDevice(5), disable(6),
  revokeSigner(7);
  const Kind(this.value);
  final int value;
  static Kind? fromValue(int v) {
    for (final k in Kind.values) {
      if (k.value == v) return k;
    }
    return null;
  }
}

/// One signer's co-signature over a co-signed entry's sigBytes (KindRevokeSigner only).
class CoSign {
  const CoSign({required this.signer, required this.sig});
  final Uint8List signer;
  final Uint8List sig;
}

/// One link in the trust log. Genesis carries the initial signer set + disablement
/// commitments; every other entry has prev = the previous entry's hash.
/// For KindRevokeSigner, signers = revoked pubkeys, replaces = replacement pubkeys,
/// and coSigns = co-signatures from trusted signers.
class Entry {
  Entry({
    required this.kind,
    this.prev,
    this.signers = const [],
    this.disablements = const [],
    this.key,
    this.signer,
    this.sig,
    this.coSigns = const [],
    this.replaces = const [],
  });

  final Kind kind;
  final Uint8List? prev;
  final List<Uint8List> signers;
  final List<Uint8List> disablements;
  final Uint8List? key;
  final Uint8List? signer;
  final Uint8List? sig;
  /// KindRevokeSigner only: co-signatures from trusted signers.
  final List<CoSign> coSigns;
  /// KindRevokeSigner only: replacement signer pubkeys added atomically.
  final List<Uint8List> replaces;
}

/// Appends a 4-byte big-endian length prefix then the bytes (len 0 for null/empty).
void putField(BytesBuilder b, Uint8List? f) {
  final len = f?.length ?? 0;
  putUint32(b, len);
  if (len > 0) b.add(f!);
}

/// The deterministic encoding an entry's signature covers: every field except sig
/// and coSigns. For KindRevokeSigner the replaces count+fields are appended so
/// co-signs attest the full replacement set.
Uint8List sigBytes(Entry e) {
  final b = BytesBuilder();
  b.addByte(e.kind.value);
  putField(b, e.prev);
  putUint32(b, e.signers.length);
  for (final s in e.signers) {
    putField(b, s);
  }
  putUint32(b, e.disablements.length);
  for (final d in e.disablements) {
    putField(b, d);
  }
  putField(b, e.key);
  putField(b, e.signer);
  if (e.kind == Kind.revokeSigner) {
    putUint32(b, e.replaces.length);
    for (final r in e.replaces) {
      putField(b, r);
    }
  }
  return b.toBytes();
}
