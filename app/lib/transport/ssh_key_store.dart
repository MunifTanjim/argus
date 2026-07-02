import '../pairing/gateway_store.dart';

/// An OpenSSH private key with an optional passphrase.
class SshKey {
  const SshKey(this.pem, [this.passphrase]);
  final String pem;
  final String? passphrase;
}

/// Persists the SSH private key in secure storage (Android Keystore-backed).
class SshKeyStore {
  SshKeyStore(this._kv);
  final SecureKv _kv;

  static const _pemKey = 'ssh_key_pem';
  static const _passKey = 'ssh_key_passphrase';

  Future<SshKey?> load() async {
    // Read both keys concurrently — each is a separate secure-storage round
    // trip, and load() runs on every (re)connect to an SSH gateway.
    final [pem, pass] = await Future.wait([
      _kv.read(_pemKey),
      _kv.read(_passKey),
    ]);
    if (pem == null) return null;
    return SshKey(pem, pass);
  }

  Future<void> save(SshKey key) async {
    await _kv.write(_pemKey, key.pem);
    final pass = key.passphrase;
    if (pass != null && pass.isNotEmpty) {
      await _kv.write(_passKey, pass);
    } else {
      await _kv.delete(_passKey);
    }
  }

  Future<void> clear() async {
    await _kv.delete(_pemKey);
    await _kv.delete(_passKey);
  }
}
