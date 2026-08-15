import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';
import 'package:argus/transport/jsonrpc.dart' show RpcError;

import 'loopback.dart';

Uint8List _json(Object? v) => Uint8List.fromList(utf8.encode(jsonEncode(v)));

void main() {
  test('push.register fans out to every node channel', () async {
    final hits = {'A': 0, 'B': 0};
    NodeHandler handler(String id) => (m, p) {
          hits[id] = (hits[id] ?? 0) + 1;
          return _json(null);
        };
    final a = LoopbackNode('A', await generateKeyPair(), handler('A'));
    final b = LoopbackNode('B', await generateKeyPair(), handler('B'));
    final lnk = MultiNodeLoopbackLink({'A': a, 'B': b});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.connect();

    await client.call('push.register', {'device_id': 'd1', 'endpoint': 'https://ep.example/x'});

    expect(hits['A'], 1, reason: 'push.register must reach node A');
    expect(hits['B'], 1, reason: 'push.register must reach node B');
    await client.close();
  });

  test('push.unregister fans out to every node channel', () async {
    final hits = {'A': 0, 'B': 0};
    NodeHandler handler(String id) => (m, p) {
          hits[id] = (hits[id] ?? 0) + 1;
          return _json(null);
        };
    final a = LoopbackNode('A', await generateKeyPair(), handler('A'));
    final b = LoopbackNode('B', await generateKeyPair(), handler('B'));
    final lnk = MultiNodeLoopbackLink({'A': a, 'B': b});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.connect();

    await client.call('push.unregister', {'device_id': 'd1'});

    expect(hits['A'], 1, reason: 'push.unregister must reach node A');
    expect(hits['B'], 1, reason: 'push.unregister must reach node B');
    await client.close();
  });

  test('push.register succeeds when at least one node succeeds', () async {
    final a = LoopbackNode('A', await generateKeyPair(), (m, p) => _json(null));
    final b = LoopbackNode('B', await generateKeyPair(),
        (m, p) => throw StateError('node B down'));
    final lnk = MultiNodeLoopbackLink({'A': a, 'B': b});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.connect();

    // partial success: does not throw
    await expectLater(
      client.call('push.register', {'device_id': 'd1', 'endpoint': 'https://ep.example/x'}),
      completes,
    );
    await client.close();
  });

  test('push.test partial success does not return gone', () async {
    // A succeeds, B reports 410 gone → overall success (no throw)
    final a = LoopbackNode('A', await generateKeyPair(), (m, p) => _json(null));
    final b = LoopbackNode('B', await generateKeyPair(),
        (m, p) => throw const RpcError(pushGoneCode, 'gone'));
    final lnk = MultiNodeLoopbackLink({'A': a, 'B': b});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.connect();

    await expectLater(client.call('push.test', {'device_id': 'd1'}), completes);
    await client.close();
  });

  test('push.test returns pushGoneCode when every node reports gone', () async {
    final a = LoopbackNode('A', await generateKeyPair(),
        (m, p) => throw const RpcError(pushGoneCode, 'gone'));
    final b = LoopbackNode('B', await generateKeyPair(),
        (m, p) => throw const RpcError(pushGoneCode, 'gone'));
    final lnk = MultiNodeLoopbackLink({'A': a, 'B': b});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.connect();

    Object? err;
    try {
      await client.call('push.test', {'device_id': 'd1'});
    } catch (e) {
      err = e;
    }
    expect(err, isA<RpcError>(), reason: 'all-gone push.test must throw RpcError');
    expect((err as RpcError).code, pushGoneCode,
        reason: 'error code must be pushGoneCode (410)');
    await client.close();
  });

  test('push.vapidKey is not in pushFanoutMethods (gateway passthrough)', () {
    // vapidKey must stay gateway-native: the subscription is created once
    // against the gateway's applicationServerKey, then the target is registered
    // with every node.
    expect(pushFanoutMethods.contains('push.vapidKey'), isFalse);
    expect(pushFanoutMethods.contains('push.register'), isTrue);
    expect(pushFanoutMethods.contains('push.unregister'), isTrue);
    expect(pushFanoutMethods.contains('push.test'), isTrue);
  });
}
