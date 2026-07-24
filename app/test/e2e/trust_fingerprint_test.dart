import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

void main() {
  test('signerSetFingerprintWords matches the Go signer_fingerprint vector', () {
    final v = (jsonDecode(File('test/e2e/testdata/vectors.json').readAsStringSync())
        as Map<String, dynamic>)['signer_fingerprint'] as Map<String, dynamic>;
    final signers = [for (final s in v['signers'] as List) Uint8List.fromList(base64.decode(s as String))];
    final expected = [for (final w in v['words'] as List) w as String];
    expect(signerSetFingerprintWords(signers), equals(expected));
  });

  test('signerSetFingerprintWords is order-independent (sorts internally)', () {
    final a = Uint8List.fromList([1, 2, 3]);
    final b = Uint8List.fromList([4, 5, 6]);
    expect(signerSetFingerprintWords([a, b]), equals(signerSetFingerprintWords([b, a])));
  });
}
