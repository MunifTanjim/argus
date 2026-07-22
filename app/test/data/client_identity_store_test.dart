import 'package:flutter_test/flutter_test.dart';
import 'package:argus/data/client_identity_store.dart';
import 'package:argus/pairing/gateway_store.dart';

class _MemKv implements SecureKv {
  final _m = <String, String>{};
  @override
  Future<String?> read(String key) async => _m[key];
  @override
  Future<void> write(String key, String value) async => _m[key] = value;
  @override
  Future<void> delete(String key) async => _m.remove(key);
}

void main() {
  test('loadOrCreate generates + persists on first use, returns the same after', () async {
    final kv = _MemKv();
    final store = ClientIdentityStore(kv);
    final a = await store.loadOrCreate();
    expect(a.privateKey.length, 32);
    expect(a.publicKey.length, 32);
    final b = await store.loadOrCreate();
    expect(b.privateKey, equals(a.privateKey));
    expect(b.publicKey, equals(a.publicKey));
  });

  test('a corrupt stored value regenerates', () async {
    final kv = _MemKv();
    await kv.write('e2e_identity_priv', 'not-base64!!');
    await kv.write('e2e_identity_pub', 'also-bad');
    final kp = await ClientIdentityStore(kv).loadOrCreate();
    expect(kp.privateKey.length, 32);
  });
}
