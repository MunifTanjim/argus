import 'package:flutter_test/flutter_test.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/pairing/pairing_uri.dart';

class MemKv implements SecureKv {
  final _m = <String, String>{};
  @override
  Future<String?> read(String key) async => _m[key];
  @override
  Future<void> write(String key, String value) async => _m[key] = value;
  @override
  Future<void> delete(String key) async => _m.remove(key);
}

void main() {
  test('save then load returns the same credentials', () async {
    final store = GatewayStore(MemKv());
    expect(await store.load(), isNull);
    await store.save(const GatewayCredentials('wss://h/client', 'tok'));
    final c = await store.load();
    expect(c!.url, 'wss://h/client');
    expect(c.token, 'tok');
  });

  test('clear removes credentials', () async {
    final store = GatewayStore(MemKv());
    await store.save(const GatewayCredentials('wss://h/client', 'tok'));
    await store.clear();
    expect(await store.load(), isNull);
  });
}
