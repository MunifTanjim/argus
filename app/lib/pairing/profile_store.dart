import 'gateway_store.dart';
import 'profile.dart';

/// Persists connection profiles (`profile_<id>`) plus the active-profile id.
class ProfileStore {
  ProfileStore(this._kv)
      : _records = IndexedStore<Profile>(
          _kv,
          indexKey: 'profiles_index',
          prefix: 'profile',
          fromJson: Profile.fromJson,
          toJson: (p) => p.toJson(),
          idOf: (p) => p.id,
        );
  final SecureKv _kv;
  final IndexedStore<Profile> _records;

  static const _activeKey = 'active_profile_id';

  Future<String?> loadActiveId() => _kv.read(_activeKey);
  Future<void> saveActiveId(String id) => _kv.write(_activeKey, id);
  Future<void> clearActiveId() => _kv.delete(_activeKey);

  Future<List<Profile>> list() => _records.list();
  Future<Profile?> get(String id) => _records.get(id);
  Future<void> add(Profile p) => _records.add(p);
  Future<void> update(Profile p) => _records.update(p);

  Future<void> delete(String id) async {
    await _records.delete(id);
    if (await loadActiveId() == id) await clearActiveId();
  }
}
