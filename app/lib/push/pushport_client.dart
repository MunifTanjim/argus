import 'dart:convert';
import 'package:http/http.dart' as http;

class PushPortSubscription {
  final String endpoint;
  final int expiresAt;

  const PushPortSubscription({required this.endpoint, required this.expiresAt});
}

class PushPortClient {
  final String _baseUrl;
  final String _appId;
  final http.Client _httpClient;

  PushPortClient({
    required this._baseUrl,
    required this._appId,
    http.Client? httpClient,
  }) : _httpClient = httpClient ?? http.Client();

  Future<PushPortSubscription> subscribe({
    required String transport,
    required Object token,
    String ttl = '24h',
  }) async {
    final url = Uri.parse('$_baseUrl/apps/$_appId/subscribe');
    final res = await _httpClient.post(
      url,
      headers: {'content-type': 'application/json'},
      body: jsonEncode({'transport': transport, 'token': token, 'ttl': ttl}),
    );
    if (res.statusCode < 200 || res.statusCode >= 300) {
      throw Exception('PushPort subscribe failed: ${res.statusCode} ${res.body}');
    }
    final body = jsonDecode(res.body) as Map<String, dynamic>;
    final data = body['data'] as Map<String, dynamic>;
    return PushPortSubscription(
      endpoint: data['endpoint'] as String,
      expiresAt: data['expires_at'] as int,
    );
  }
}
