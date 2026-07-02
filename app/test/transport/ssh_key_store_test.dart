import 'package:flutter_test/flutter_test.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/transport/ssh_key_store.dart';

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
  test('save then load returns pem and passphrase', () async {
    final store = SshKeyStore(MemKv());
    expect(await store.load(), isNull);
    await store.save(const SshKey('PEMDATA', 'secret'));
    final k = await store.load();
    expect(k!.pem, 'PEMDATA');
    expect(k.passphrase, 'secret');
  });

  test('empty passphrase is stored as null', () async {
    final store = SshKeyStore(MemKv());
    await store.save(const SshKey('PEMDATA', ''));
    final k = await store.load();
    expect(k!.pem, 'PEMDATA');
    expect(k.passphrase, isNull);
  });

  test('clear removes the key', () async {
    final store = SshKeyStore(MemKv());
    await store.save(const SshKey('PEMDATA'));
    await store.clear();
    expect(await store.load(), isNull);
  });
}
