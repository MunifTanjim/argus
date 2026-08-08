import 'dart:typed_data';

import 'package:cryptography_plus/cryptography_plus.dart'
    show Ed25519, KeyPairType, Signature, SimplePublicKey;

final Ed25519 _ed25519 = Ed25519();

/// Verifies an Ed25519 [sig] over [msg] by public key [pub] as a TOTAL function,
/// matching Go's crypto/ed25519.Verify: returns false for any malformed input
/// and never throws.
///
/// cryptography_plus's [Ed25519.verify] throws an [ArgumentError] when the
/// public key is not 32 bytes or the signature is not 64 bytes; Go returns false
/// in those cases. Without this parity the Dart client would be STRICTER than the
/// node on adversarial input — e.g. an untrusted gateway could append a junk
/// co-sign (a known trusted signer's pubkey with a wrong-length signature) to an
/// otherwise-valid revoke entry, making the client throw and reject a chain the
/// node accepts, thereby suppressing a revocation.
Future<bool> ed25519Verify(Uint8List pub, List<int> msg, Uint8List sig) async {
  if (pub.length != 32 || sig.length != 64) return false;
  try {
    return await _ed25519.verify(
      msg,
      signature: Signature(sig, publicKey: SimplePublicKey(pub, type: KeyPairType.ed25519)),
    );
  } on ArgumentError {
    return false;
  }
}
