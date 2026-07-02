import 'dart:convert';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';

import 'pairing_uri.dart';

abstract class SecureKv {
  Future<String?> read(String key);
  Future<void> write(String key, String value);
  Future<void> delete(String key);
}

class FlutterSecureKv implements SecureKv {
  const FlutterSecureKv([this._s = const FlutterSecureStorage()]);
  final FlutterSecureStorage _s;

  @override
  Future<String?> read(String key) => _s.read(key: key);
  @override
  Future<void> write(String key, String value) => _s.write(key: key, value: value);
  @override
  Future<void> delete(String key) => _s.delete(key: key);
}

/// Persists records as an id index plus one `<prefix>_<id>` record each, so
/// editing one record never rewrites the others'. Writes record-first and
/// de-indexes-first so a torn write leaves an orphan, never a dangling index.
class IndexedStore<T> {
  IndexedStore(
    this._kv, {
    required this.indexKey,
    required this.prefix,
    required this.fromJson,
    required this.toJson,
    required this.idOf,
  });

  final SecureKv _kv;
  final String indexKey;
  final String prefix;
  final T Function(String id, Map<String, dynamic> json) fromJson;
  final Map<String, dynamic> Function(T value) toJson;
  final String Function(T value) idOf;

  String _recKey(String id) => '${prefix}_$id';

  Future<List<String>> _index() async {
    final raw = await _kv.read(indexKey);
    if (raw == null || raw.isEmpty) return [];
    return (jsonDecode(raw) as List).cast<String>();
  }

  Future<void> _writeIndex(List<String> ids) =>
      _kv.write(indexKey, jsonEncode(ids));

  Future<List<T>> list() async {
    final records = await Future.wait((await _index()).map(get));
    return [for (final r in records) ?r]; // skip torn records
  }

  Future<T?> get(String id) async {
    final raw = await _kv.read(_recKey(id));
    if (raw == null) return null;
    return fromJson(id, jsonDecode(raw) as Map<String, dynamic>);
  }

  Future<void> add(T value) async {
    final id = idOf(value);
    await _kv.write(_recKey(id), jsonEncode(toJson(value))); // record first
    final ids = await _index();
    if (!ids.contains(id)) {
      ids.add(id);
      await _writeIndex(ids);
    }
  }

  Future<void> update(T value) => add(value); // same write path; add is idempotent

  Future<void> delete(String id) async {
    final ids = await _index();
    if (ids.remove(id)) await _writeIndex(ids); // de-index first
    await _kv.delete(_recKey(id));
  }
}

class GatewayStore {
  GatewayStore(this._kv);
  final SecureKv _kv;

  static const _urlKey = 'gateway_url';
  static const _tokenKey = 'gateway_token';

  Future<GatewayCredentials?> load() async {
    final url = await _kv.read(_urlKey);
    final token = await _kv.read(_tokenKey);
    if (url == null || token == null) return null;
    return GatewayCredentials(url, token);
  }

  Future<void> save(GatewayCredentials c) async {
    await _kv.write(_urlKey, c.url);
    await _kv.write(_tokenKey, c.token);
  }

  Future<void> clear() async {
    await _kv.delete(_urlKey);
    await _kv.delete(_tokenKey);
  }
}
