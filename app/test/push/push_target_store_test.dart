import 'package:flutter_test/flutter_test.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/push/push_provider.dart';
import 'package:argus/push/push_target_store.dart';

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
  test('load on an empty store is null', () async {
    expect(await PushTargetStore(MemKv()).load(), isNull);
  });

  test('saved target round-trips, keys included', () async {
    final store = PushTargetStore(MemKv());
    const t = PushTarget('https://ep.example/x', p256dh: 'pk', auth: 'au');
    await store.save(t);
    expect(await store.load(), t);
  });

  test('saved endpoint-only target round-trips with null keys', () async {
    final store = PushTargetStore(MemKv());
    const t = PushTarget('https://ep.example/plain');
    await store.save(t);
    final got = await store.load();
    expect(got, t);
    expect(got!.p256dh, isNull);
    expect(got.auth, isNull);
  });

  test('survives a fresh instance over the same store (the restart path)',
      () async {
    final kv = MemKv();
    await PushTargetStore(kv).save(const PushTarget('https://ep.example/keep'));
    // New process after relaunch = new store, same persisted kv.
    expect(await PushTargetStore(kv).load(),
        const PushTarget('https://ep.example/keep'));
  });

  test('clear removes the persisted target', () async {
    final store = PushTargetStore(MemKv());
    await store.save(const PushTarget('https://ep.example/x'));
    await store.clear();
    expect(await store.load(), isNull);
  });

  test('garbage payload loads as null instead of throwing', () async {
    final kv = MemKv();
    await kv.write('push_target', 'not json');
    expect(await PushTargetStore(kv).load(), isNull);
  });

  test('payload with empty endpoint loads as null', () async {
    final kv = MemKv();
    await kv.write('push_target', '{"endpoint":""}');
    expect(await PushTargetStore(kv).load(), isNull);
  });
}
