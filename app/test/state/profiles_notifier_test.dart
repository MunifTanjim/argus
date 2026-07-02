import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/pairing/profile.dart';
import 'package:argus/pairing/profile_store.dart';
import 'package:argus/state/profiles.dart';
import 'package:argus/transport/key_library_store.dart';
import 'package:argus/transport/library_key.dart';

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
  test('keysProvider reflects add and remove', () async {
    final kv = MemKv();
    final container = ProviderContainer(overrides: [
      keyLibraryStoreProvider.overrideWithValue(KeyLibraryStore(kv)),
    ]);
    addTearDown(container.dispose);

    expect(await container.read(keysProvider.future), isEmpty);
    await container.read(keysProvider.notifier)
        .add(const LibraryKey(id: 'k1', name: 'k', pem: 'P'));
    expect((await container.read(keysProvider.future)).single.id, 'k1');
    await container.read(keysProvider.notifier).remove('k1');
    expect(await container.read(keysProvider.future), isEmpty);
  });

  test('profilesProvider reflects add', () async {
    final kv = MemKv();
    final container = ProviderContainer(overrides: [
      profileStoreProvider.overrideWithValue(ProfileStore(kv)),
    ]);
    addTearDown(container.dispose);

    await container.read(profilesProvider.notifier).add(const Profile(
        id: 'p1', name: 'box', mode: ProfileMode.ssh, token: 't', host: 'h', keyId: 'k1'));
    expect((await container.read(profilesProvider.future)).single.id, 'p1');
  });
}
