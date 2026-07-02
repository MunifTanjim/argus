import 'gateway_store.dart';

const _flagKey = 'profiles_migrated';
const _legacyKeys = [
  'gateway_url',
  'gateway_token',
  'ssh_key_pem',
  'ssh_key_passphrase',
];

/// Fresh-start migration: drop the pre-manager single-slot credentials once, so
/// the app opens to the profiles list instead of auto-connecting stale data.
Future<void> migrateLegacyOnce(SecureKv kv) async {
  if (await kv.read(_flagKey) != null) return;
  for (final k in _legacyKeys) {
    await kv.delete(k);
  }
  await kv.write(_flagKey, '1');
}
