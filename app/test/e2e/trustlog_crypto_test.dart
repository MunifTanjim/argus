import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';
import 'package:cryptography_plus/cryptography_plus.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

Map<String, dynamic> _tl() =>
    (jsonDecode(File('test/e2e/testdata/vectors.json').readAsStringSync())
        as Map<String, dynamic>)['trustlog'] as Map<String, dynamic>;

void main() {
  test('disablementCommitment(secret) matches the Go Argon2id commitment', () async {
    final v = _tl();
    final commit = await disablementCommitment(base64.decode(v['secret'] as String));
    expect(commit, equals(Uint8List.fromList(base64.decode(v['commitment'] as String))));
  });

  test('the genesis entry signature verifies with Ed25519 over sigBytes', () async {
    final v = _tl();
    final genesis =
        unmarshalChain(Uint8List.fromList(base64.decode(v['chain'] as String))).first;
    final ok = await Ed25519().verify(
      sigBytes(genesis),
      signature: Signature(
        genesis.sig!,
        publicKey: SimplePublicKey(genesis.signer!, type: KeyPairType.ed25519),
      ),
    );
    expect(ok, isTrue);
  });
}
