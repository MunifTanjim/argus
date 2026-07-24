import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/cipher_state.dart';

void main() {
  test('encryptWithAd matches the Go cipher vector at the given counter', () {
    final v = jsonDecode(File('test/e2e/testdata/vectors.json').readAsStringSync())
        as Map<String, dynamic>;
    final s = v['cipher_sample'] as Map<String, dynamic>;
    final key = base64.decode(s['key'] as String);
    final counter = s['counter'] as int;
    final ad = base64.decode(s['ad'] as String);
    final pt = base64.decode(s['plaintext'] as String);
    final expectedCt = base64.decode(s['ciphertext'] as String);

    final cs = CipherState(key);
    // Advance the nonce to the sample's counter.
    for (var i = 0; i < counter; i++) {
      cs.encryptWithAd(const [], const []);
    }
    final ct = cs.encryptWithAd(ad, pt);
    expect(ct, equals(Uint8List.fromList(expectedCt)));
  });

  test('encrypt/decrypt throw when the nonce is exhausted (no silent reuse)', () {
    final key = Uint8List.fromList(List<int>.filled(32, 7));
    // Seed at the maximum representable-without-loss nonce; the next op would
    // overflow (native) or lose precision (web), so it must throw instead of
    // silently reusing a nonce.
    final enc = CipherState(key, initialNonce: cipherStateMaxNonce);
    expect(() => enc.encryptWithAd(const [], const [1, 2, 3]), throwsStateError);
    final dec = CipherState(key, initialNonce: cipherStateMaxNonce);
    expect(() => dec.decryptWithAd(const [], List<int>.filled(20, 0)), throwsStateError);
  });

  test('round-trips with a paired state and rejects a tampered tag', () {
    final key = Uint8List.fromList(List<int>.generate(32, (i) => i));
    final enc = CipherState(key);
    final dec = CipherState(key);
    final ad = [0, 0, 0, 1, 0];
    final ct = enc.encryptWithAd(ad, utf8.encode('secret'));
    expect(utf8.decode(dec.decryptWithAd(ad, ct)), 'secret');

    final enc2 = CipherState(key);
    final dec2 = CipherState(key);
    final ct2 = enc2.encryptWithAd(ad, utf8.encode('secret'));
    ct2[0] ^= 0xff;
    expect(() => dec2.decryptWithAd(ad, ct2), throwsA(anything));
  });

  test('rejects a non-32-byte key and a too-short ciphertext', () {
    expect(() => CipherState(List<int>.filled(31, 0)), throwsArgumentError);
    expect(() => CipherState(List<int>.filled(32, 0)).decryptWithAd(const [], const [1, 2, 3]),
        throwsArgumentError);
  });
}
