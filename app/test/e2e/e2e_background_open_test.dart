import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

import 'loopback.dart';

Uint8List _json(Object? v) => Uint8List.fromList(utf8.encode(jsonEncode(v)));

Future<void> _pump() async {
  for (var i = 0; i < 20; i++) {
    await Future(() {});
  }
}

Future<(E2EClient, Completer<void>)> _connectWithSlowB() async {
  final gate = Completer<void>();
  NodeHandler handler(String id) => (m, p) => m == 'sessions.list'
      ? _json([
          {'id': 's$id'},
        ])
      : _json({'node': id});
  final a = LoopbackNode('A', await generateKeyPair(), handler('A'));
  final b = LoopbackNode('B', await generateKeyPair(), handler('B'))
    ..handshakeGate = gate;
  final lnk = MultiNodeLoopbackLink({'A': a, 'B': b});
  final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
  await client.connect();
  await _pump();
  return (client, gate);
}

void main() {
  test('connect returns while a node is still opening', () async {
    final (client, gate) = await _connectWithSlowB();
    expect(client.connectedNodeIds, {'A'});
    expect(client.openingNodeIds, {'B'});
    gate.complete();
    await client.close();
  });

  test('a call to an open node does not wait for an opening one', () async {
    final (client, gate) = await _connectWithSlowB();
    final r = await client.call('sessions.capture', {'session_id': 'A:s'});
    expect(r, {'node': 'A'});
    gate.complete();
    await client.close();
  });

  test('a call to an opening node waits for its handshake', () async {
    final (client, gate) = await _connectWithSlowB();
    Object? result;
    final call = client
        .call('sessions.capture', {'session_id': 'B:s'})
        .then((r) => result = r);
    await _pump();
    expect(result, isNull);
    gate.complete();
    await call;
    expect(result, {'node': 'B'});
    expect(client.openingNodeIds, isEmpty);
    await client.close();
  });

  test('forEachNodeSessions includes opening nodes as they open', () async {
    final (client, gate) = await _connectWithSlowB();
    final seen = <String>[];
    final fanout = client.forEachNodeSessions(
      'sessions.list',
      (nodeId, _) => seen.add(nodeId),
    );
    await _pump();
    expect(seen, ['A']);
    gate.complete();
    await fanout;
    expect(seen, ['A', 'B']);
    await client.close();
  });

  test('an aggregating fan-out waits for opening nodes', () async {
    final (client, gate) = await _connectWithSlowB();
    final list = client.call('sessions.list');
    await _pump();
    gate.complete();
    final ids = [for (final s in (await list) as List) (s as Map)['id']];
    expect(ids, unorderedEquals(['A:sA', 'B:sB']));
    await client.close();
  });

  test('a node whose open fails is skipped, not waited on', () async {
    NodeHandler handler(String id) => (m, p) => m == 'sessions.list'
        ? _json([
            {'id': 's$id'},
          ])
        : _json({'node': id});
    final a = LoopbackNode('A', await generateKeyPair(), handler('A'));
    final b = LoopbackNode('B', await generateKeyPair(), handler('B'));
    final lnk = MultiNodeLoopbackLink({'A': a, 'B': b}, failRelayOpen: {'B'});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.connect();

    final seen = <String>[];
    await client.forEachNodeSessions(
      'sessions.list',
      (nodeId, _) => seen.add(nodeId),
    );
    expect(seen, ['A']);
    await expectLater(
      client.call('sessions.capture', {'session_id': 'B:s'}),
      throwsA(anything),
    );
    expect(client.connectedNodeIds, {'A'});
    expect(client.openingNodeIds, isEmpty);
    await client.close();
  });
}
