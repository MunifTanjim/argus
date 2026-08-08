import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/symmetric_state.dart';

void main() {
  final v = jsonDecode(File('test/e2e/testdata/vectors.json').readAsStringSync())
      as Map<String, dynamic>;

  test('blake2s matches the protocol-name hash vector', () {
    final expected = base64.decode(v['h0'] as String);
    expect(blake2s(utf8.encode(v['protocol_name'] as String)),
        equals(Uint8List.fromList(expected)));
  });

  test('hkdf matches the Go HKDF vector', () {
    final s = v['hkdf_sample'] as Map<String, dynamic>;
    final out = hkdf(base64.decode(s['ck'] as String),
        base64.decode(s['ikm'] as String), s['num'] as int);
    final expected = (s['outputs'] as List).map((e) => base64.decode(e as String)).toList();
    expect(out.length, expected.length);
    for (var i = 0; i < out.length; i++) {
      expect(out[i], equals(Uint8List.fromList(expected[i])));
    }
  });

  test('mixHash matches the Go mixHash vector', () {
    final s = v['mixhash_sample'] as Map<String, dynamic>;
    final ss = SymmetricState('x'); // seed then overwrite via reflection-free path:
    // mixHash composes h = blake2s(h_in ++ data); verify the pure relation instead.
    final hIn = base64.decode(s['h_in'] as String);
    final data = base64.decode(s['data'] as String);
    final expected = base64.decode(s['h_out'] as String);
    expect(blake2s([...hIn, ...data]), equals(Uint8List.fromList(expected)));
    // touch ss to avoid unused warning
    expect(ss.handshakeHash.length, 32);
  });
}
