import 'dart:typed_data';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/bytes.dart';

// putUint64 must produce an 8-byte big-endian encoding (matching Go
// binary.BigEndian.PutUint64) using a two-32-bit-half split so it is web-safe —
// ByteData.setUint64 throws under dart2js (no native 64-bit int).
void main() {
  test('putUint64 encodes big-endian across both 32-bit halves', () {
    final b = BytesBuilder();
    putUint64(b, 0x0102030405060708); // both halves non-zero
    expect(b.toBytes(), equals([1, 2, 3, 4, 5, 6, 7, 8]));
  });

  test('putUint64 encodes a value above 2^32 (high half set)', () {
    final b = BytesBuilder();
    putUint64(b, 0x100000001); // 2^32 + 1
    expect(b.toBytes(), equals([0, 0, 0, 1, 0, 0, 0, 1]));
  });

  test('putUint64 encodes a small value with a zero high half', () {
    final b = BytesBuilder();
    putUint64(b, 7);
    expect(b.toBytes(), equals([0, 0, 0, 0, 0, 0, 0, 7]));
  });
}
