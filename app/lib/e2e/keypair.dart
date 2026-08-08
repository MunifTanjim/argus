import 'dart:typed_data';

import 'package:cryptography_plus/cryptography_plus.dart';

class KeyPair {
  KeyPair(this.privateKey, this.publicKey) {
    if (privateKey.length != 32 || publicKey.length != 32) {
      throw ArgumentError('KeyPair keys must be 32 bytes');
    }
  }

  final Uint8List privateKey;
  final Uint8List publicKey;
}

final _x25519 = X25519();

Future<KeyPair> generateKeyPair() async {
  return _extract(await _x25519.newKeyPair());
}

/// Stores [seed] unclamped as [KeyPair.privateKey] and derives the public key
/// from the clamped seed, matching Go's `flynn/noise` `GenerateKeypair`.
Future<KeyPair> keyPairFromSeed(List<int> seed) async {
  final kp = await _x25519.newKeyPairFromSeed(seed);
  final data = await kp.extract();
  return KeyPair(
    Uint8List.fromList(seed),
    Uint8List.fromList(data.publicKey.bytes),
  );
}

Future<KeyPair> _extract(SimpleKeyPair kp) async {
  final data = await kp.extract();
  return KeyPair(
    Uint8List.fromList(data.bytes),
    Uint8List.fromList(data.publicKey.bytes),
  );
}
