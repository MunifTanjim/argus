import 'package:flutter_test/flutter_test.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/pairing/profile.dart';
import 'package:argus/state/profiles.dart';
import 'package:argus/transport/ssh_hostkey_store.dart';

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
  test('ssh profile with no key returns a message without connecting', () async {
    const p = Profile(
        id: 'p1', name: 'n', mode: ProfileMode.ssh, token: 't',
        host: 'h', keyId: 'k1');
    final msg = await verifyConnection(p, null, HostKeyStore(MemKv()));
    expect(msg, 'Pick an SSH key');
  });
}
