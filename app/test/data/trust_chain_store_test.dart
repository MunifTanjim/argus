import 'dart:typed_data';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/data/trust_chain_store.dart';
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
  test('save/load round-trips the chain bytes', () async {
    final store = TrustChainStore(_MemKv());
    final chain = Uint8List.fromList([1, 2, 3, 250, 0, 99]);
    await store.save(chain);
    expect(await store.load(), equals(chain));
  });

  test('load returns null when absent or corrupt', () async {
    final kv = _MemKv();
    expect(await TrustChainStore(kv).load(), isNull);
    await kv.write('e2e_trust_chain', 'not base64 %%%');
    expect(await TrustChainStore(kv).load(), isNull);
  });

  test('clear removes the stored chain', () async {
    final store = TrustChainStore(_MemKv());
    await store.save(Uint8List.fromList([1]));
    await store.clear();
    expect(await store.load(), isNull);
  });
}
