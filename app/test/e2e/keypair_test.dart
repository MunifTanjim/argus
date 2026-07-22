import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/keypair.dart';

void main() {
  test('keyPairFromSeed derives the public key matching the Go vector', () async {
    final v = jsonDecode(File('test/e2e/testdata/vectors.json').readAsStringSync())
        as Map<String, dynamic>;
    final s = v['init_static'] as Map<String, dynamic>;
    final priv = base64.decode(s['priv'] as String);
    final expectedPub = base64.decode(s['pub'] as String);

    final kp = await keyPairFromSeed(priv);
    expect(kp.privateKey, equals(Uint8List.fromList(priv)));
    expect(kp.publicKey, equals(Uint8List.fromList(expectedPub)));
  });

  test('generateKeyPair returns 32-byte keys', () async {
    final kp = await generateKeyPair();
    expect(kp.privateKey.length, 32);
    expect(kp.publicKey.length, 32);
  });
}
