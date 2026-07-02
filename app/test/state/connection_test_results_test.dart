import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/pairing/profile_store.dart';
import 'package:argus/state/profiles.dart';

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
  test('set records per-id results and clear removes one', () {
    final c = ProviderContainer();
    addTearDown(c.dispose);
    final n = c.read(connectionTestResultsProvider.notifier);

    n.set('a', true);
    n.set('b', false);
    expect(c.read(connectionTestResultsProvider), {'a': true, 'b': false});

    n.clear('a');
    expect(c.read(connectionTestResultsProvider), {'b': false});
  });

  test('markActiveConnected marks the stored active profile green', () async {
    final c = ProviderContainer();
    addTearDown(c.dispose);
    final profiles = ProfileStore(MemKv());
    await profiles.saveActiveId('x');

    await markActiveConnected(
        profiles, c.read(connectionTestResultsProvider.notifier));
    expect(c.read(connectionTestResultsProvider), {'x': true});
  });

  test('markActiveConnected is a no-op with no active profile', () async {
    final c = ProviderContainer();
    addTearDown(c.dispose);
    await markActiveConnected(
        ProfileStore(MemKv()), c.read(connectionTestResultsProvider.notifier));
    expect(c.read(connectionTestResultsProvider), isEmpty);
  });
}
