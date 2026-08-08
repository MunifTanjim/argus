import 'dart:typed_data';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/bytes.dart';

void main() {
  test('putUint64 encodes big-endian across both 32-bit halves', () {
    final b = BytesBuilder();
    putUint64(b, 0x0102030405060708);
    expect(b.toBytes(), equals([1, 2, 3, 4, 5, 6, 7, 8]));
  });

  test('putUint64 encodes a value above 2^32 (high half set)', () {
    final b = BytesBuilder();
    putUint64(b, 0x100000001);
    expect(b.toBytes(), equals([0, 0, 0, 1, 0, 0, 0, 1]));
  });

  test('putUint64 encodes a small value with a zero high half', () {
    final b = BytesBuilder();
    putUint64(b, 7);
    expect(b.toBytes(), equals([0, 0, 0, 0, 0, 0, 0, 7]));
  });
}
