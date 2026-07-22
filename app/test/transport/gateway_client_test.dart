import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';
import 'package:argus/transport/gateway_client.dart';
import 'package:argus/transport/jsonrpc.dart';
import 'package:argus/transport/rpc_client.dart';

import '../e2e/loopback.dart';

void main() {
  test('RpcClient is a GatewayClient', () {
    final c = RpcClient(incoming: const Stream.empty(), sendFrame: (_) {});
    expect(c, isA<GatewayClient>());
    c.close();
  });

  test('E2EClient is a GatewayClient and adapts notifications to RpcMessage', () async {
    final node = LoopbackNode('A', await generateKeyPair(),
        (m, p) => Uint8List.fromList(utf8.encode('null')));
    final link = MultiNodeLoopbackLink({'A': node});
    final client = E2EClient(link.incoming, link.send, await generateKeyPair());
    expect(client, isA<GatewayClient>());
    await client.connect();
    final got = client.notifications.firstWhere((m) => m.method == 'session.event');
    node.emitNotification('session.event', Uint8List.fromList(utf8.encode('{"type":"updated","session":{"id":"s1"}}')));
    final m = await got.timeout(const Duration(seconds: 2));
    expect(m, isA<RpcMessage>());
    expect(m.method, 'session.event');
    expect((m.params as Map)['session'], isA<Map>());
    await client.close();
  });
}
