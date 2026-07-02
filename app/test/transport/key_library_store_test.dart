import 'package:flutter_test/flutter_test.dart';
import 'package:argus/pairing/gateway_store.dart';
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
  test('add persists a record then indexes it', () async {
    final kv = MemKv();
    final s = KeyLibraryStore(kv);
    await s.add(const LibraryKey(id: 'a', name: 'A', pem: 'PA'));
    expect(kv.m.containsKey('sshkey_a'), isTrue);
    expect(kv.m['ssh_keys_index'], contains('a'));
    expect((await s.get('a'))!.pem, 'PA');
  });

  test('list returns items in index order', () async {
    final s = KeyLibraryStore(MemKv());
    await s.add(const LibraryKey(id: 'a', name: 'A', pem: 'PA'));
    await s.add(const LibraryKey(id: 'b', name: 'B', pem: 'PB'));
    expect((await s.list()).map((k) => k.id).toList(), ['a', 'b']);
  });

  test('list skips an index id whose record is missing', () async {
    final kv = MemKv();
    final s = KeyLibraryStore(kv);
    await s.add(const LibraryKey(id: 'a', name: 'A', pem: 'PA'));
    await s.add(const LibraryKey(id: 'b', name: 'B', pem: 'PB'));
    await kv.delete('sshkey_a'); // simulate a torn write
    expect((await s.list()).map((k) => k.id).toList(), ['b']);
  });

  test('delete removes the id from the index before deleting the record', () async {
    final kv = MemKv();
    final s = KeyLibraryStore(kv);
    await s.add(const LibraryKey(id: 'a', name: 'A', pem: 'PA'));
    await s.delete('a');
    expect(kv.m['ssh_keys_index'], isNot(contains('a')));
    expect(kv.m.containsKey('sshkey_a'), isFalse);
  });

  test('update rewrites the record and keeps a single index entry', () async {
    final kv = MemKv();
    final s = KeyLibraryStore(kv);
    await s.add(const LibraryKey(id: 'a', name: 'A', pem: 'PA'));
    await s.update(const LibraryKey(id: 'a', name: 'A2', pem: 'PA2'));
    expect((await s.get('a'))!.name, 'A2');
    expect((await s.list()).length, 1);
  });
}
