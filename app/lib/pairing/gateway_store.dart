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
