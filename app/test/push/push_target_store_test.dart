import 'package:flutter_test/flutter_test.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/push/push_provider.dart';
import 'package:argus/push/push_target_store.dart';

import 'push_test_support.dart';

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
    expect((await store.load())!.target, t);
  });

  test('saved endpoint-only target round-trips with null keys', () async {
    final store = PushTargetStore(MemKv());
    const t = PushTarget('https://ep.example/plain');
    await store.save(t);
    final got = await store.load();
    expect(got!.target, t);
    expect(got.target.p256dh, isNull);
    expect(got.target.auth, isNull);
  });

  test('survives a fresh instance over the same store (the restart path)',
      () async {
    final kv = MemKv();
    await PushTargetStore(kv).save(const PushTarget('https://ep.example/keep'));
    // New process after relaunch = new store, same persisted kv.
    expect((await PushTargetStore(kv).load())!.target,
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

  test('the expiry round-trips with the target', () async {
    final kv = MemKv();
    final lease = PushLease.granted(unixIn(const Duration(hours: 24)));
    await PushTargetStore(kv)
        .save(const PushTarget('https://ep.example/x'), lease: lease);

    final got = await PushTargetStore(kv).load();
    expect(got!.lease.expiresAt, lease.expiresAt);
    expect(got.lease.hasExpired(), isFalse);
  });

  test('a lapsed lease round-trips as expired', () async {
    final kv = MemKv();
    final lease = PushLease.granted(unixIn(const Duration(hours: -1)));
    await PushTargetStore(kv)
        .save(const PushTarget('https://ep.example/dead'), lease: lease);

    expect((await PushTargetStore(kv).load())!.lease.hasExpired(), isTrue);
  });

  test('a target saved without a lease loads as PushLease.none', () async {
    final kv = MemKv();
    await PushTargetStore(kv).save(const PushTarget('https://ep.example/x'));

    final got = await PushTargetStore(kv).load();
    expect(got!.lease, PushLease.none);
    expect(got.lease.hasExpired(), isFalse);
  });

  test('a payload written before leases existed still loads', () async {
    final kv = MemKv();
    await kv.write('push_target',
        '{"endpoint":"https://ep.example/old","p256dh":"pk","auth":"au"}');

    final got = await PushTargetStore(kv).load();
    expect(got!.target,
        const PushTarget('https://ep.example/old', p256dh: 'pk', auth: 'au'));
    expect(got.lease, PushLease.none);
  });

  test('the expiry is persisted beside the target, not inside the register '
      'payload', () async {
    final kv = MemKv();
    final lease = PushLease.granted(unixIn(const Duration(hours: 24)));
    await PushTargetStore(kv)
        .save(const PushTarget('https://ep.example/x'), lease: lease);

    expect(await kv.read('push_target'), contains('"expires_at"'));
    expect((await PushTargetStore(kv).load())!.target.toParams().keys,
        isNot(contains('expires_at')));
  });
}
