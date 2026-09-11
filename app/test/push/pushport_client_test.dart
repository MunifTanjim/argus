import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/testing.dart';
import 'package:http/http.dart' as http;
import 'package:argus/push/pushport_client.dart';

void main() {
  test('subscribe posts transport+token and parses the sealed endpoint',
      () async {
    late http.Request seen;
    final mock = MockClient((req) async {
      seen = req;
      return http.Response(
        jsonEncode({
          'request_id': 'r1',
          'data': {
            'endpoint': 'https://push.pushport.dev/push/abc',
            'expires_at': 123
          },
        }),
        200,
        headers: {'content-type': 'application/json'},
      );
    });
    final c = PushPortClient(
        baseUrl: 'https://push.pushport.dev', appId: 'argus', httpClient: mock);
    final sub =
        await c.subscribe(transport: 'fcm', token: 'fcm-token', ttl: '360h');

    expect(seen.url.toString(), 'https://push.pushport.dev/apps/argus/subscribe');
    final sent = jsonDecode(seen.body) as Map<String, dynamic>;
    expect(sent['transport'], 'fcm');
    expect(sent['token'], 'fcm-token');
    expect(sub.endpoint, 'https://push.pushport.dev/push/abc');
    expect(sub.expiresAt, 123);
  });
}
