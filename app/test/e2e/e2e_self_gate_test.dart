import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

import 'loopback.dart';

Map<String, dynamic> _tl() =>
    (jsonDecode(File('test/e2e/testdata/vectors.json').readAsStringSync())
        as Map<String, dynamic>)['trustlog'] as Map<String, dynamic>;

Uint8List _b(String key) => Uint8List.fromList(base64.decode(_tl()[key] as String));

Future<LoopbackNode> _nodeA() async => LoopbackNode(
    'A',
    await keyPairFromSeed(_b('enforcement_node_a_seed')),
    (m, p) => Uint8List.fromList(utf8.encode('null')));

void main() {
  test('an unauthorized device opens no channels', () async {
    final link = MultiNodeLoopbackLink({'A': await _nodeA()},
        trustChain: _b('enforcement_chain'));
    final client = E2EClient(link.incoming, link.send, await generateKeyPair(),
        genesisHash: _b('enforcement_genesis_head'));
    await client.connect();
    expect(client.connectedNodeIds, isEmpty);
    expect(link.relayOpenCalls, isEmpty,
        reason: 'the node would drop the handshake, so none must be attempted');
    await client.close();
  });

  test('an unauthorized device opens a node with lock enforcement disabled', () async {
    final link = MultiNodeLoopbackLink({'A': await _nodeA()},
        trustChain: _b('enforcement_chain'), lockDisabled: {'A'});
    final client = E2EClient(link.incoming, link.send, await generateKeyPair(),
        genesisHash: _b('enforcement_genesis_head'));
    await client.connect();
    expect(client.connectedNodeIds.toSet(), {'A'});
    await client.close();
  });

  test('channels open once the device is authorized, without a reconnect', () async {
    final link = MultiNodeLoopbackLink({'A': await _nodeA()},
        trustChain: _b('enforcement_chain_pre_client'));
    final client = E2EClient(link.incoming, link.send,
        await keyPairFromSeed(_b('enforcement_client_seed')),
        genesisHash: _b('enforcement_genesis_head'));
    await client.connect();
    expect(client.connectedNodeIds, isEmpty);

    link.trustChain = _b('enforcement_chain');
    link.pushNotification('node.event', {'type': 'trust-changed'});
    for (var i = 0; i < 200 && client.connectedNodeIds.isEmpty; i++) {
      await Future<void>.delayed(const Duration(milliseconds: 10));
    }
    expect(client.connectedNodeIds.toSet(), {'A'});
    await client.close();
  });

  test('a roster event during the handshake does not open a second channel', () async {
    final link = MultiNodeLoopbackLink({'A': await _nodeA()},
        trustChain: _b('enforcement_chain'));
    final client = E2EClient(link.incoming, link.send,
        await keyPairFromSeed(_b('enforcement_client_seed')),
        genesisHash: _b('enforcement_genesis_head'));
    final pub = base64.encode((await keyPairFromSeed(_b('enforcement_node_a_seed'))).publicKey);
    link.onRelayOpen = (_) {
      link.onRelayOpen = null;
      link.pushNotification('node.event', {
        'type': 'online',
        'node': {'id': 'A', 'identity_pubkey': pub, 'online': true},
      });
    };
    await client.connect();
    await Future<void>.delayed(const Duration(milliseconds: 50));
    expect(client.connectedNodeIds.toSet(), {'A'});
    expect(link.relayOpenCalls, ['A']);
    await client.close();
  });
}
