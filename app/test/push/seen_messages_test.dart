import 'package:flutter_test/flutter_test.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/push/seen_messages.dart';

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
  test('first sighting is new, replay is suppressed', () async {
    final seen = SeenMessages(MemKv());
    expect(await seen.markSeen('abc'), isTrue);
    expect(await seen.markSeen('abc'), isFalse);
    expect(await seen.markSeen('abc'), isFalse);
  });

  test('distinct ids each show once', () async {
    final seen = SeenMessages(MemKv());
    expect(await seen.markSeen('a'), isTrue);
    expect(await seen.markSeen('b'), isTrue);
    expect(await seen.markSeen('a'), isFalse);
  });

  test('null/empty ids are never deduped', () async {
    final seen = SeenMessages(MemKv());
    expect(await seen.markSeen(null), isTrue);
    expect(await seen.markSeen(null), isTrue);
    expect(await seen.markSeen(''), isTrue);
    expect(await seen.markSeen(''), isTrue);
  });

  test('survives a fresh instance over the same store (the replay path)', () async {
    final kv = MemKv();
    expect(await SeenMessages(kv).markSeen('x'), isTrue);
    // New engine/isolate after relaunch = new SeenMessages, same persisted kv.
    expect(await SeenMessages(kv).markSeen('x'), isFalse);
  });

  test('remembers more ids than the plugin can replay', () async {
    final seen = SeenMessages(MemKv());
    for (var i = 0; i < 40; i++) {
      expect(await seen.markSeen('id$i'), isTrue);
    }
    // The plugin buffers the last 20; the earliest of those must still dedup.
    expect(await seen.markSeen('id20'), isFalse);
    expect(await seen.markSeen('id39'), isFalse);
  });
}
