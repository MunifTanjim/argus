import 'package:flutter_test/flutter_test.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/push/pause_preference_store.dart';

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
    expect(await PausePreferenceStore(MemKv()).load(), isNull);
  });

  test('saved preference round-trips', () async {
    final store = PausePreferenceStore(MemKv());
    await store.save('2026-06-01T13:00:00Z');
    expect(await store.load(), '2026-06-01T13:00:00Z');
  });

  test('the indefinite sentinel round-trips', () async {
    final store = PausePreferenceStore(MemKv());
    await store.save(pauseIndefinite);
    expect(await store.load(), pauseIndefinite);
  });

  test('survives a fresh instance over the same store (the restart path)',
      () async {
    final kv = MemKv();
    await PausePreferenceStore(kv).save(pauseIndefinite);
    expect(await PausePreferenceStore(kv).load(), pauseIndefinite);
  });

  test('clear removes the persisted preference', () async {
    final store = PausePreferenceStore(MemKv());
    await store.save(pauseIndefinite);
    await store.clear();
    expect(await store.load(), isNull);
  });
}
