// app/test/pairing/profile_store_test.dart
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/pairing/profile.dart';
import 'package:argus/pairing/profile_store.dart';

class MemKv implements SecureKv {
  final m = <String, String>{};
  @override
  Future<String?> read(String key) async => m[key];
  @override
  Future<void> write(String key, String value) async => m[key] = value;
  @override
  Future<void> delete(String key) async => m.remove(key);
}

Profile _p(String id) =>
    Profile(id: id, name: id, mode: ProfileMode.ssh, token: 't', host: 'h', keyId: 'k1');

void main() {
  test('add then list returns the profile in index order', () async {
    final s = ProfileStore(MemKv());
    await s.add(_p('a'));
    await s.add(_p('b'));
    expect((await s.list()).map((p) => p.id).toList(), ['a', 'b']);
  });

  test('add writes record before indexing', () async {
    final kv = MemKv();
    await ProfileStore(kv).add(_p('a'));
    expect(kv.m.containsKey('profile_a'), isTrue);
    expect(kv.m['profiles_index'], contains('a'));
  });

  test('delete de-indexes then removes the record', () async {
    final kv = MemKv();
    final s = ProfileStore(kv);
    await s.add(_p('a'));
    await s.delete('a');
    expect(kv.m['profiles_index'], isNot(contains('a')));
    expect(kv.m.containsKey('profile_a'), isFalse);
  });

  test('list skips a torn record', () async {
    final kv = MemKv();
    final s = ProfileStore(kv);
    await s.add(_p('a'));
    await s.add(_p('b'));
    await kv.delete('profile_a');
    expect((await s.list()).map((p) => p.id).toList(), ['b']);
  });

  test('update keeps a single index entry', () async {
    final kv = MemKv();
    final s = ProfileStore(kv);
    await s.add(_p('a'));
    await s.update(Profile(
        id: 'a', name: 'renamed', mode: ProfileMode.ssh, token: 't', host: 'h', keyId: 'k1'));
    expect((await s.get('a'))!.name, 'renamed');
    expect((await s.list()).length, 1);
  });

  test('active id round-trips and clears', () async {
    final s = ProfileStore(MemKv());
    expect(await s.loadActiveId(), isNull);
    await s.saveActiveId('a');
    expect(await s.loadActiveId(), 'a');
    await s.clearActiveId();
    expect(await s.loadActiveId(), isNull);
  });

  test('deleting the active profile clears the active id', () async {
    final s = ProfileStore(MemKv());
    await s.add(_p('a'));
    await s.saveActiveId('a');
    await s.delete('a');
    expect(await s.loadActiveId(), isNull);
  });

  test('deleting a non-active profile keeps the active id', () async {
    final s = ProfileStore(MemKv());
    await s.add(_p('a'));
    await s.add(_p('b'));
    await s.saveActiveId('a');
    await s.delete('b');
    expect(await s.loadActiveId(), 'a');
  });
}
