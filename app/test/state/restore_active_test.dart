import 'package:flutter_test/flutter_test.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/pairing/profile.dart';
import 'package:argus/pairing/profile_store.dart';
import 'package:argus/state/profiles.dart';
import 'package:argus/transport/key_library_store.dart';
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
  test('no active id returns null', () async {
    final creds = await restoreActiveCredentials(
        ProfileStore(MemKv()), KeyLibraryStore(MemKv()), SshKeyStore(MemKv()));
    expect(creds, isNull);
  });

  test('valid direct profile resolves to its credentials', () async {
    final profiles = ProfileStore(MemKv());
    await profiles.add(const Profile(
        id: 'd', name: 'd', mode: ProfileMode.direct, token: 't',
        url: 'wss://example.test'));
    await profiles.saveActiveId('d');

    final creds = await restoreActiveCredentials(
        profiles, KeyLibraryStore(MemKv()), SshKeyStore(MemKv()));
    expect(creds!.url, 'wss://example.test');
    expect(creds.token, 't');
    expect(await profiles.loadActiveId(), 'd'); // unchanged on success
  });

  test('active id whose profile is gone returns null and clears the id', () async {
    final profiles = ProfileStore(MemKv());
    await profiles.saveActiveId('missing');

    final creds = await restoreActiveCredentials(
        profiles, KeyLibraryStore(MemKv()), SshKeyStore(MemKv()));
    expect(creds, isNull);
    expect(await profiles.loadActiveId(), isNull);
  });

  test('dangling ssh profile (missing key) returns null and clears the id', () async {
    final profiles = ProfileStore(MemKv());
    await profiles.add(const Profile(
        id: 's', name: 's', mode: ProfileMode.ssh, token: 't',
        host: 'h', keyId: 'gone'));
    await profiles.saveActiveId('s');

    final creds = await restoreActiveCredentials(
        profiles, KeyLibraryStore(MemKv()), SshKeyStore(MemKv()));
    expect(creds, isNull);
    expect(await profiles.loadActiveId(), isNull);
  });
}
