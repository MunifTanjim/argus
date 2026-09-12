import 'dart:async';
import 'dart:convert';

import 'package:argus/push/apns_provider.dart';
import 'package:argus/push/push_provider.dart';
import 'package:argus/push/pushport_client.dart';
import 'package:argus/push/pushport_fcm_provider.dart';
import 'package:argus/push/webpush_crypto.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import 'push_test_support.dart';

void main() {
  test('start subscribes with the apns transport and reports the target',
      () async {
    late String seenTransport;
    late Object seenToken;
    final httpClient = MockClient((req) async {
      final body = req.body;
      seenTransport = body.contains('"transport":"apns"') ? 'apns' : 'other';
      seenToken = body.contains('"token":"abcd"') ? 'abcd' : 'missing';
      return http.Response(
        '{"data":{"endpoint":"https://relay/x","expires_at":123}}',
        200,
      );
    });
    final client = PushPortClient(
      baseUrl: 'https://pushport.test',
      appId: 'app1',
      httpClient: httpClient,
    );
    final keys = WebPushKeys(p256dh: 'PUB', auth: 'AUTH', privateKey: const [1, 2, 3]);
    var wroteKeys = false;
    final provider = ApnsProvider(
      client: client,
      getApnsToken: () async => 'abcd',
      writeKeys: ({required privB64url, required authB64url}) async {
        wroteKeys = privB64url.isNotEmpty && authB64url == 'AUTH';
        return true;
      },
      keyStore: FixedWebPushKeyStore(keys),
    );

    PushTarget? reported;
    await provider.start(
      onTarget: (t) => reported = t,
      onMessage: (_) {},
      onOpen: (_) {},
    );

    expect(provider.name, 'pushport/apns');
    expect(seenTransport, 'apns');
    expect(seenToken, 'abcd');
    expect(wroteKeys, isTrue);
    expect(reported, const PushTarget('https://relay/x', p256dh: 'PUB', auth: 'AUTH'));
  });

  test('refresh re-subscribes and reports the fresh target', () async {
    var calls = 0;
    final provider = _provider((_) async {
      calls++;
      return pushPortSubscribeBody('https://relay/$calls', const Duration(hours: 24));
    });

    final reported = <PushTarget>[];
    await provider.start(
      onTarget: reported.add,
      onMessage: (_) {},
      onOpen: (_) {},
    );
    await provider.refresh();

    expect(calls, 2, reason: 'refresh must mint a new subscription');
    expect(reported.map((t) => t.endpoint),
        ['https://relay/1', 'https://relay/2']);
  });

  test('overlapping refreshes share one subscription', () async {
    var calls = 0;
    Completer<void>? gate;
    final provider = _provider((_) async {
      calls++;
      await gate?.future;
      return pushPortSubscribeBody('https://relay/$calls', const Duration(hours: 24));
    });

    final reported = <PushTarget>[];
    await provider.start(
      onTarget: reported.add,
      onMessage: (_) {},
      onOpen: (_) {},
    );

    gate = Completer<void>();
    final first = provider.refresh();
    final second = provider.refresh();
    gate.complete();
    await Future.wait([first, second]);

    expect(calls, 2, reason: 'one mint on start, one shared by both refreshes');
    expect(reported.map((t) => t.endpoint),
        ['https://relay/1', 'https://relay/2']);
  });

  test('a mint the stop overtook cannot report into the restarted provider',
      () async {
    var calls = 0;
    final gate = Completer<void>();
    final provider = _provider((_) async {
      final n = ++calls;
      if (n == 1) await gate.future;
      return pushPortSubscribeBody('https://relay/$n', const Duration(hours: 24));
    });

    final reported = <PushTarget>[];
    final overtaken = provider.start(
      onTarget: reported.add,
      onMessage: (_) {},
      onOpen: (_) {},
    );
    await provider.stop();
    await provider.start(
      onTarget: reported.add,
      onMessage: (_) {},
      onOpen: (_) {},
    );
    gate.complete();
    await overtaken;

    expect(calls, 2);
    expect(reported.map((t) => t.endpoint), ['https://relay/2'],
        reason: 'the endpoint minted before the stop is dead to the app');

    await provider.refresh();
    expect(calls, 3, reason: 'the overtaken mint must not hold the guard slot');
  });

  test('a development build subscribes in the APNs sandbox, renewals included',
      () async {
    final bodies = <Map<String, dynamic>>[];
    final provider = _provider(
      (req) async {
        bodies.add(jsonDecode(req.body) as Map<String, dynamic>);
        return pushPortSubscribeBody('https://relay/x', const Duration(hours: 24));
      },
      sandbox: true,
    );

    await provider.start(onTarget: (_) {}, onMessage: (_) {}, onOpen: (_) {});
    await provider.refresh();

    expect(bodies.map((b) => b['sandbox']), [true, true],
        reason: 'a renewal must stay in the environment that minted the token');
  });

  test('a release build subscribes in APNs production', () async {
    final bodies = <Map<String, dynamic>>[];
    final provider = _provider((req) async {
      bodies.add(jsonDecode(req.body) as Map<String, dynamic>);
      return pushPortSubscribeBody('https://relay/x', const Duration(hours: 24));
    });

    await provider.start(onTarget: (_) {}, onMessage: (_) {}, onOpen: (_) {});

    expect(bodies.single.containsKey('sandbox'), isFalse);
  });

  test('the APNs environment follows the build it was compiled for', () {
    final provider = ApnsProvider(
      client: PushPortClient(baseUrl: 'https://pushport.test', appId: 'app1'),
      getApnsToken: () async => 'abcd',
      writeKeys: ({required privB64url, required authB64url}) async => true,
    );

    expect(provider.sandbox, apnsUsesSandbox,
        reason: 'a development build must not subscribe in APNs production');
  });

  test('a fresh long subscription is not due for renewal', () async {
    final provider = _provider((_) async =>
        pushPortSubscribeBody('https://relay/x', const Duration(hours: 24)));
    await provider.start(onTarget: (_) {}, onMessage: (_) {}, onOpen: (_) {});
    expect(provider.lease.needsRenewal(), isFalse);
  });

  test('a lapsed subscription is due for renewal', () async {
    final provider = _provider((_) async =>
        pushPortSubscribeBody('https://relay/x', const Duration(hours: -1)));
    await provider.start(onTarget: (_) {}, onMessage: (_) {}, onOpen: (_) {});
    expect(provider.lease.needsRenewal(), isTrue);
  });

  test('there is no lease before start, and none again after stop', () async {
    final provider = _provider((_) async =>
        pushPortSubscribeBody('https://relay/x', const Duration(hours: 24)));
    expect(provider.lease, PushLease.none);

    await provider.start(onTarget: (_) {}, onMessage: (_) {}, onOpen: (_) {});
    expect(provider.lease.isKnown, isTrue);

    await provider.stop();
    expect(provider.lease, PushLease.none);
  });

  test('refresh after stop does nothing', () async {
    var calls = 0;
    final provider = _provider((_) async {
      calls++;
      return pushPortSubscribeBody('https://relay/x', const Duration(hours: 24));
    });
    await provider.start(onTarget: (_) {}, onMessage: (_) {}, onOpen: (_) {});
    await provider.stop();
    await provider.refresh();

    expect(calls, 1);
  });
}

ApnsProvider _provider(Future<String> Function(http.Request) respond,
    {bool sandbox = false}) {
  final client = PushPortClient(
    baseUrl: 'https://pushport.test',
    appId: 'app1',
    httpClient: MockClient((req) async => http.Response(await respond(req), 200)),
  );
  return ApnsProvider(
    client: client,
    getApnsToken: () async => 'abcd',
    sandbox: sandbox,
    writeKeys: ({required privB64url, required authB64url}) async => true,
    keyStore: const FixedWebPushKeyStore(
      WebPushKeys(p256dh: 'PUB', auth: 'AUTH', privateKey: [1, 2, 3]),
    ),
  );
}
