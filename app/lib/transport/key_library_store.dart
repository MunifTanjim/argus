import '../pairing/gateway_store.dart';
import 'library_key.dart';

/// Persists the reusable SSH key library (`sshkey_<id>`), so editing one key
/// never rewrites the others' (multi-KB) PEMs.
class KeyLibraryStore {
  KeyLibraryStore(SecureKv kv)
      : _records = IndexedStore<LibraryKey>(
          kv,
          indexKey: 'ssh_keys_index',
          prefix: 'sshkey',
          fromJson: LibraryKey.fromJson,
          toJson: (k) => k.toJson(),
          idOf: (k) => k.id,
        );
  final IndexedStore<LibraryKey> _records;

  Future<List<LibraryKey>> list() => _records.list();
  Future<LibraryKey?> get(String id) => _records.get(id);
  Future<void> add(LibraryKey k) => _records.add(k);
  Future<void> update(LibraryKey k) => _records.update(k);
  Future<void> delete(String id) => _records.delete(id);
}
