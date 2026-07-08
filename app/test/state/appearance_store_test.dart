import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/state/appearance.dart';
import 'package:flutter_test/flutter_test.dart';

class _FakeKv implements SecureKv {
  final Map<String, String> _m = {};

  @override
  Future<String?> read(String key) async => _m[key];
  @override
  Future<void> write(String key, String value) async => _m[key] = value;
  @override
  Future<void> delete(String key) async => _m.remove(key);
}

void main() {
  test('defaults to false when nothing is stored', () async {
    final prefs = await AppearanceStore(_FakeKv()).load();
    expect(prefs.collapseToolCalls, isFalse);
  });

  test('persists and reloads collapseToolCalls', () async {
    final kv = _FakeKv();
    await AppearanceStore(kv).setCollapseToolCalls(true);
    final prefs = await AppearanceStore(kv).load();
    expect(prefs.collapseToolCalls, isTrue);
  });

  test('malformed stored value parses to false', () async {
    final kv = _FakeKv();
    await kv.write('appearance.collapseToolCalls', 'yes');
    final prefs = await AppearanceStore(kv).load();
    expect(prefs.collapseToolCalls, isFalse);
  });
}
