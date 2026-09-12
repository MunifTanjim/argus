import 'dart:async';
import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/testing.dart';
import 'package:http/http.dart' as http;
import 'package:argus/push/pushport_client.dart';

import 'push_test_support.dart';

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

  test('subscribe asks for the app-wide ttl by default', () async {
    late http.Request seen;
    final c = _client(MockClient((req) async {
      seen = req;
      return http.Response(
        pushPortSubscribeBody('https://relay/x', const Duration(hours: 1)), 200);
    }));

    await c.subscribe(transport: 'apns', token: 't');

    expect((jsonDecode(seen.body) as Map)['ttl'], pushPortRequestedTtl);
  });

  test('subscribe leaves out the sandbox flag by default', () async {
    late http.Request seen;
    final c = _client(MockClient((req) async {
      seen = req;
      return http.Response(
        pushPortSubscribeBody('https://relay/x', const Duration(hours: 1)), 200);
    }));

    await c.subscribe(transport: 'apns', token: 't');

    expect((jsonDecode(seen.body) as Map).containsKey('sandbox'), isFalse);
  });

  test('subscribe asks for the APNs sandbox when told to', () async {
    late http.Request seen;
    final c = _client(MockClient((req) async {
      seen = req;
      return http.Response(
        pushPortSubscribeBody('https://relay/x', const Duration(hours: 1)), 200);
    }));

    await c.subscribe(transport: 'apns', token: 't', sandbox: true);

    expect((jsonDecode(seen.body) as Map)['sandbox'], isTrue);
  });

  test('subscribe gives up when the relay never answers', () async {
    final c = _client(
      MockClient((_) => Completer<http.Response>().future),
      timeout: const Duration(milliseconds: 20),
    );

    await expectLater(
      c.subscribe(transport: 'apns', token: 't'),
      throwsA(isA<TimeoutException>()),
    );
  });

  test('subscribe throws on a non-2xx answer', () async {
    final c = _client(MockClient((_) async => http.Response('nope', 503)));

    await expectLater(
      c.subscribe(transport: 'apns', token: 't'),
      throwsA(isA<Exception>()),
    );
  });

  group('expires_at', () {
    Future<int> parsed(Object? value) async {
      final c = _client(MockClient((_) async => http.Response(
            jsonEncode({
              'data': {'endpoint': 'https://relay/x', 'expires_at': value},
            }),
            200,
          )));
      return (await c.subscribe(transport: 'apns', token: 't')).expiresAt;
    }

    test('reads an integer', () async => expect(await parsed(123), 123));
    test('reads a JSON number', () async => expect(await parsed(123.0), 123));
    test('reads a numeric string', () async => expect(await parsed('123'), 123));
    test('is 0 when absent', () async => expect(await parsed(null), 0));
    test('is 0 when unreadable', () async => expect(await parsed('soon'), 0));
  });
}

PushPortClient _client(http.Client httpClient, {Duration? timeout}) =>
    PushPortClient(
      baseUrl: 'https://push.pushport.dev',
      appId: 'argus',
      httpClient: httpClient,
      timeout: timeout ?? pushPortTimeout,
    );
