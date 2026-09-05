import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

import 'loopback.dart';

Map<String, dynamic> _tl() =>
    (jsonDecode(File('test/e2e/testdata/vectors.json').readAsStringSync())
            as Map<String, dynamic>)['trustlog']
        as Map<String, dynamic>;

Uint8List _b(Map<String, dynamic> v, String k) =>
    Uint8List.fromList(base64.decode(v[k] as String));

// Mirrors Go TestTipConsistencyOverChannel / TestTrustChangedNudgeKicksSync:
// the client reads each node's trust-log tip over its authenticated Noise
// channel (node.identify) and flags equivocation when a tip cannot be
// reconciled with the resolved chain after the miss threshold; a trust-changed
// gateway nudge triggers a prompt trust-log pull.
void main() {
  test('an off-chain tip read over the channel trips equivocation', () async {
    final v = _tl();
    final aKp = await keyPairFromSeed(_b(v, 'enforcement_node_a_seed'));
    final bKp = await keyPairFromSeed(_b(v, 'enforcement_node_b_seed'));
    final a = LoopbackNode('A', aKp, (m, p) => utf8.encode('null'));
    final b = LoopbackNode('B', bKp, (m, p) => utf8.encode('null'));
    final chain = _b(v, 'reeval_initial_chain'); // authorizes A + B
    final head = hashEntry(unmarshalChain(chain).last);
    a.tip = head; // in the resolved chain
    b.tip = Uint8List.fromList(List.filled(32, 0x99)); // never in the chain
    final link = MultiNodeLoopbackLink({'A': a, 'B': b}, trustChain: chain);
    final client = E2EClient(
      link.incoming,
      link.send,
      await generateKeyPair(),
      tofu: true,
    );
    await client.connect();
    expect(client.connectedNodeIds.toSet(), {'A', 'B'});
    for (var i = 0; i < 3; i++) {
      await client.resyncNow(); // > miss threshold
    }
    expect(
      client.equivocation,
      isTrue,
      reason:
          'an off-chain tip must trip equivocation after the miss threshold',
    );
    await client.close();
  });

  test('in-chain tips over the channel stay quiet', () async {
    final v = _tl();
    final aKp = await keyPairFromSeed(_b(v, 'enforcement_node_a_seed'));
    final bKp = await keyPairFromSeed(_b(v, 'enforcement_node_b_seed'));
    final a = LoopbackNode('A', aKp, (m, p) => utf8.encode('null'));
    final b = LoopbackNode('B', bKp, (m, p) => utf8.encode('null'));
    final chain = _b(v, 'reeval_initial_chain');
    final head = hashEntry(unmarshalChain(chain).last);
    a.tip = head;
    b.tip = head;
    final link = MultiNodeLoopbackLink({'A': a, 'B': b}, trustChain: chain);
    final client = E2EClient(
      link.incoming,
      link.send,
      await generateKeyPair(),
      tofu: true,
    );
    await client.connect();
    expect(client.connectedNodeIds.toSet(), {'A', 'B'});
    for (var i = 0; i < 4; i++) {
      await client.resyncNow();
    }
    expect(
      client.equivocation,
      isFalse,
      reason: 'all in-chain tips must not trip equivocation',
    );
    await client.close();
  });

  test('a trust-changed node.event triggers a prompt trust-log sync', () async {
    final v = _tl();
    final aKp = await keyPairFromSeed(_b(v, 'enforcement_node_a_seed'));
    final a = LoopbackNode('A', aKp, (m, p) => utf8.encode('null'));
    final chain = _b(v, 'reeval_initial_chain');
    a.tip = hashEntry(unmarshalChain(chain).last);
    final link = MultiNodeLoopbackLink({'A': a}, trustChain: chain);
    final client = E2EClient(
      link.incoming,
      link.send,
      await generateKeyPair(),
      tofu: true,
    );
    await client.connect();
    // Connect pulls the trust log once synchronously; record that baseline.
    final base = link.trustSyncCount;

    // The nudge takes the same gateway-notification path onGatewayNotification
    // handles; the Node field is irrelevant for this event type.
    link.pushNotification('node.event', {'type': 'trust-changed'});

    final deadline = DateTime.now().add(const Duration(seconds: 3));
    while (DateTime.now().isBefore(deadline)) {
      await Future<void>.delayed(const Duration(milliseconds: 20));
      if (link.trustSyncCount > base) break;
    }
    expect(
      link.trustSyncCount,
      greaterThan(base),
      reason: 'a trust-changed nudge must trigger a prompt trust-log pull',
    );
    await client.close();
  });
}
