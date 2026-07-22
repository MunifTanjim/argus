import 'dart:convert';
import 'dart:typed_data';

import '../e2e/e2e.dart' show KeyPair, generateKeyPair;
import '../pairing/gateway_store.dart' show SecureKv;

/// Persists the client's Curve25519 Noise static identity so a locked network
/// authorizes the device once and it survives restarts. Web-safe (SecureKv).
class ClientIdentityStore {
  ClientIdentityStore(this._kv);

  final SecureKv _kv;
  static const _privKey = 'e2e_identity_priv';
  static const _pubKey = 'e2e_identity_pub';

  /// Returns the persisted identity, generating + saving one on first use (or if
  /// the stored value is missing/corrupt).
  Future<KeyPair> loadOrCreate() async {
    final priv = await _kv.read(_privKey);
    final pub = await _kv.read(_pubKey);
    if (priv != null && pub != null) {
      try {
        final p = base64.decode(priv);
        final q = base64.decode(pub);
        if (p.length == 32 && q.length == 32) {
          return KeyPair(Uint8List.fromList(p), Uint8List.fromList(q));
        }
      } catch (_) {
        // corrupt encoding: fall through to regenerate
      }
    }
    final kp = await generateKeyPair();
    await _kv.write(_privKey, base64.encode(kp.privateKey));
    await _kv.write(_pubKey, base64.encode(kp.publicKey));
    return kp;
  }
}
