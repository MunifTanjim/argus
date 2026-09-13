import 'dart:convert';
import 'package:http/http.dart' as http;

class PushPortSubscription {
  final String endpoint;
  final int expiresAt;

  const PushPortSubscription({required this.endpoint, required this.expiresAt});
}

/// Advisory: the relay grants what it wants, and `expires_at` in its answer is
/// the only value the app acts on.
const pushPortRequestedTtl = '24h';

/// The controller awaits a subscribe before it registers the device, so an
/// unbounded call would hold back a working target for the life of the
/// connection.
const pushPortTimeout = Duration(seconds: 20);

class PushPortClient {
  final String _baseUrl;
  final String _appId;
  final http.Client _httpClient;
  final Duration _timeout;

  PushPortClient({
    required this._baseUrl,
    required this._appId,
    http.Client? httpClient,
    this._timeout = pushPortTimeout,
  }) : _httpClient = httpClient ?? http.Client();

  /// [sandbox] is APNs only: it routes the endpoint through the APNs sandbox.
  Future<PushPortSubscription> subscribe({
    required String transport,
    required Object token,
    String ttl = pushPortRequestedTtl,
    bool sandbox = false,
  }) async {
    final url = Uri.parse('$_baseUrl/apps/$_appId/subscribe');
    final res = await _httpClient
        .post(
          url,
          headers: {'content-type': 'application/json'},
          body: jsonEncode({
            'transport': transport,
            'token': token,
            'ttl': ttl,
            if (sandbox) 'sandbox': true,
          }),
        )
        .timeout(_timeout);
    if (res.statusCode < 200 || res.statusCode >= 300) {
      throw Exception('PushPort subscribe failed: ${res.statusCode} ${res.body}');
    }
    final body = jsonDecode(res.body) as Map<String, dynamic>;
    final data = body['data'] as Map<String, dynamic>;
    return PushPortSubscription(
      endpoint: data['endpoint'] as String,
      expiresAt: _unixSeconds(data['expires_at']),
    );
  }
}

/// Zero for anything unreadable, which reads as "no expiry known" downstream.
int _unixSeconds(Object? v) => switch (v) {
      final int i => i,
      final double d => d.toInt(),
      final String s => int.tryParse(s) ?? 0,
      _ => 0,
    };
