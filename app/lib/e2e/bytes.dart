import 'dart:typed_data';

/// Shared low-level byte helpers for the E2E / trust-log modules.

String hexEncode(List<int> b) {
  final sb = StringBuffer();
  for (final x in b) {
    sb.write(x.toRadixString(16).padLeft(2, '0'));
  }
  return sb.toString();
}

Uint8List hexDecode(String h) => Uint8List.fromList(
    [for (var i = 0; i < h.length; i += 2) int.parse(h.substring(i, i + 2), radix: 16)]);

bool bytesEqual(List<int> a, List<int> b) {
  if (a.length != b.length) return false;
  for (var i = 0; i < a.length; i++) {
    if (a[i] != b[i]) return false;
  }
  return true;
}

/// Orders two byte lists lexicographically, mirroring Go's bytes.Compare.
int compareBytes(List<int> a, List<int> b) {
  for (var i = 0; i < a.length && i < b.length; i++) {
    if (a[i] != b[i]) return a[i] - b[i];
  }
  return a.length - b.length;
}

void putUint32(BytesBuilder b, int n) {
  final h = Uint8List(4);
  ByteData.sublistView(h).setUint32(0, n, Endian.big);
  b.add(h);
}

/// Appends n big-endian as two 32-bit halves rather than ByteData.setUint64,
/// which throws under dart2js (Flutter web has no native 64-bit int).
void putUint64(BytesBuilder b, int n) {
  final h = Uint8List(8);
  final bd = ByteData.sublistView(h);
  bd.setUint32(0, (n ~/ 0x100000000) & 0xffffffff, Endian.big);
  bd.setUint32(4, n & 0xffffffff, Endian.big);
  b.add(h);
}
