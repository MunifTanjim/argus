import 'package:flutter_test/flutter_test.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/pairing/profile.dart';
import 'package:argus/state/profiles.dart';
import 'package:argus/transport/key_library_store.dart';
import 'package:argus/transport/library_key.dart';
import 'package:argus/transport/ssh_key_store.dart';

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
  test('ssh profile writes the library key to the active slot and builds the url', () async {
    final keys = KeyLibraryStore(MemKv());
    await keys.add(const LibraryKey(id: 'k1', name: 'k', pem: 'PEM', passphrase: 'pw'));
    final active = SshKeyStore(MemKv());
    const p = Profile(
      id: 'p1', name: 'box', mode: ProfileMode.ssh, token: 'tok',
      host: 'h', user: 'me', sshPort: 2222, gatewayPort: 8443, keyId: 'k1',
    );

    final creds = await activateProfile(p, keys, active);

    expect(creds.url, 'ssh://me@h:2222?port=8443');
    expect(creds.token, 'tok');
    final saved = await active.load();
    expect(saved!.pem, 'PEM');
    expect(saved.passphrase, 'pw');
  });

  test('direct profile returns its url unchanged and touches no key', () async {
    final creds = await activateProfile(
      const Profile(id: 'p2', name: 'd', mode: ProfileMode.direct, token: 't', url: 'ws://h:8443'),
      KeyLibraryStore(MemKv()),
      SshKeyStore(MemKv()),
    );
    expect(creds.url, 'ws://h:8443');
    expect(creds.token, 't');
  });

  test('dangling ssh profile throws StateError', () async {
    const p = Profile(
        id: 'p1', name: 'box', mode: ProfileMode.ssh, token: 'tok', host: 'h', keyId: 'gone');
    await expectLater(
      activateProfile(p, KeyLibraryStore(MemKv()), SshKeyStore(MemKv())),
      throwsA(isA<StateError>()),
    );
  });
}
