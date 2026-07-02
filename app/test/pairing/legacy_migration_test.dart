import 'package:flutter_test/flutter_test.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/pairing/legacy_migration.dart';

class MemKv implements SecureKv {
  final m = <String, String>{};
  @override
  Future<String?> read(String key) async => m[key];
  @override
  Future<void> write(String key, String value) async => m[key] = value;
  @override
  Future<void> delete(String key) async => m.remove(key);
}

void main() {
  test('clears legacy single-slot keys once, then no-ops', () async {
    final kv = MemKv();
    kv.m['gateway_url'] = 'ssh://h';
    kv.m['gateway_token'] = 'tok';
    kv.m['ssh_key_pem'] = 'PEM';
    kv.m['ssh_key_passphrase'] = 'pw';

    await migrateLegacyOnce(kv);
    expect(kv.m.containsKey('gateway_url'), isFalse);
    expect(kv.m.containsKey('ssh_key_pem'), isFalse);
    expect(kv.m['profiles_migrated'], '1');

    // Re-add something and ensure a second run leaves it alone.
    kv.m['ssh_key_pem'] = 'ACTIVE';
    await migrateLegacyOnce(kv);
    expect(kv.m['ssh_key_pem'], 'ACTIVE');
  });
}
