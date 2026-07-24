import 'dart:typed_data';

/// Shared low-level byte helpers for the E2E / trust-log modules: a single home
/// for the hex, equality, lexicographic-compare, and big-endian-count primitives
/// that were previously hand-rolled in each file.

/// hexEncode returns the lowercase hex encoding of b (two digits per byte).
String hexEncode(List<int> b) {
  final sb = StringBuffer();
  for (final x in b) {
    sb.write(x.toRadixString(16).padLeft(2, '0'));
  }
  return sb.toString();
}

/// hexDecode parses a hex string into bytes (two hex digits per byte).
Uint8List hexDecode(String h) => Uint8List.fromList(
    [for (var i = 0; i < h.length; i += 2) int.parse(h.substring(i, i + 2), radix: 16)]);

/// bytesEqual reports whether a and b have identical length and contents.
bool bytesEqual(List<int> a, List<int> b) {
  if (a.length != b.length) return false;
  for (var i = 0; i < a.length; i++) {
    if (a[i] != b[i]) return false;
  }
  return true;
}

/// compareBytes orders two byte lists lexicographically (shorter-is-less on a
/// prefix tie), mirroring Go's bytes.Compare.
int compareBytes(List<int> a, List<int> b) {
  for (var i = 0; i < a.length && i < b.length; i++) {
    if (a[i] != b[i]) return a[i] - b[i];
  }
  return a.length - b.length;
}

/// putUint32 appends n as a 4-byte big-endian integer (mirrors Go
/// binary.BigEndian.PutUint32).
void putUint32(BytesBuilder b, int n) {
  final h = Uint8List(4);
  ByteData.sublistView(h).setUint32(0, n, Endian.big);
  b.add(h);
}

/// putUint64 appends n as an 8-byte big-endian integer (mirrors Go
/// binary.BigEndian.PutUint64). Web-safe: it writes two 32-bit halves rather than
/// using ByteData.setUint64, which throws under dart2js (Flutter web has no native
/// 64-bit int) — the same technique cipher_state.dart uses for the Noise nonce.
void putUint64(BytesBuilder b, int n) {
  final h = Uint8List(8);
  final bd = ByteData.sublistView(h);
  bd.setUint32(0, (n ~/ 0x100000000) & 0xffffffff, Endian.big); // high 32 bits
  bd.setUint32(4, n & 0xffffffff, Endian.big); // low 32 bits
  b.add(h);
}
