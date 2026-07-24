import 'dart:async';
import 'dart:convert';
import 'dart:io' as io; // TEST-ONLY: reads source files for the web-safety guard.

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

import 'loopback.dart';

Future<NodeDescriptor> _node(LoopbackNode n) async =>
    NodeDescriptor(id: n.id, identityPubKey: base64.encode(n.keyPair.publicKey));

void main() {
  test('openChannel completes an IK handshake and callNode returns the raw result', () async {
    final nodeKp = await generateKeyPair();
    final node = LoopbackNode('node-1', nodeKp, (method, params) {
      expect(method, 'server.info');
      return utf8.encode('{"echo":${utf8.decode(params)}}');
    });
    final link = LoopbackLink(node);
    final client = E2EClient(link.incoming, link.send, await generateKeyPair());

    final nc = await client.openChannel(await _node(node));
    final result = await client.callNode(nc, 'server.info', utf8.encode('{"q":1}'));
    expect(utf8.decode(result), '{"echo":{"q":1}}');
    await client.close();
  });

  test('a null result comes back as raw "null" bytes', () async {
    // The error path (an {"error":...} inner -> RpcError) is covered by the
    // channel_test unit test; the loopback node only produces {"result":...}.
    final nodeKp = await generateKeyPair();
    final node = LoopbackNode('n', nodeKp, (method, params) => utf8.encode('null'));
    final lnk = LoopbackLink(node);
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    final nc = await client.openChannel(await _node(node));
    final r = await client.callNode(nc, 'x', utf8.encode('1'));
    expect(utf8.decode(r), 'null'); // {"result":null} -> raw "null"
    await client.close();
  });

  test('node notifications reach the events stream tagged with nodeId', () async {
    final nodeKp = await generateKeyPair();
    final node = LoopbackNode('node-7', nodeKp, (m, p) => utf8.encode('null'));
    final lnk = LoopbackLink(node);
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.openChannel(await _node(node));

    final got = client.events.first;
    node.emitNotification('session.event', utf8.encode('{"seq":1}'));
    final ev = await got.timeout(const Duration(seconds: 2));
    expect(ev.method, 'session.event');
    expect(ev.nodeId, 'node-7');
    expect(utf8.decode(ev.params), '{"seq":1}');
    await client.close();
  });

  test('callNode times out when no reply arrives', () async {
    final nodeKp = await generateKeyPair();
    final node = LoopbackNode('n', nodeKp, (m, p) => utf8.encode('null'));
    final lnk = LoopbackLink(node);
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair(),
        callTimeout: const Duration(milliseconds: 200));
    final nc = await client.openChannel(await _node(node)); // handshake still works
    node.dropRequests = true; // node now silently drops sealed requests
    await expectLater(
        client.callNode(nc, 'x', utf8.encode('1')), throwsA(isA<TimeoutException>()));
    await client.close();
  });

  test('openChannel times out when gateway never answers relay.open', () async {
    final nodeKp = await generateKeyPair();
    final node = LoopbackNode('node-silent', nodeKp, (m, p) => utf8.encode('null'));
    final link = LoopbackLink(node);
    link.answerGatewayRpc = false;
    final client = E2EClient(link.incoming, link.send, await generateKeyPair(),
        handshakeTimeout: const Duration(milliseconds: 200));
    await expectLater(
        client.openChannel(await _node(node)), throwsA(isA<TimeoutException>()));
    await client.close();
  });

  test('callNode after close() fails fast with StateError', () async {
    final nodeKp = await generateKeyPair();
    final node = LoopbackNode('node-closed', nodeKp, (m, p) => utf8.encode('null'));
    final lnk = LoopbackLink(node);
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    final nc = await client.openChannel(await _node(node));
    await client.close();
    await expectLater(
        client.callNode(nc, 'x', utf8.encode('1')), throwsA(isA<StateError>()));
  });

  test('lib/e2e contains no dart:io import (web-safety)', () {
    final dir = io.Directory('lib/e2e');
    for (final f in dir.listSync(recursive: true).whereType<io.File>().where((f) => f.path.endsWith('.dart'))) {
      expect(f.readAsStringSync().contains("dart:io"), isFalse, reason: '${f.path} imports dart:io');
    }
  });

  test('lib/transport/jsonrpc.dart contains no dart:io import (web-safety)', () {
    final f = io.File('lib/transport/jsonrpc.dart');
    expect(f.readAsStringSync().contains("dart:io"), isFalse,
        reason: 'lib/transport/jsonrpc.dart imports dart:io');
  });
}
