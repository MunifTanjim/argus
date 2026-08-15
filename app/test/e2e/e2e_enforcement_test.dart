import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

import 'loopback.dart';

Map<String, dynamic> _tl() =>
    (jsonDecode(File('test/e2e/testdata/vectors.json').readAsStringSync())
        as Map<String, dynamic>)['trustlog'] as Map<String, dynamic>;

void main() {
  test(
      'pinned client opens the authorized node and skips the unauthorized one',
      () async {
    final v = _tl();

    final aKp = await keyPairFromSeed(base64.decode(v['enforcement_node_a_seed'] as String));
    final bKp = await keyPairFromSeed(base64.decode(v['enforcement_node_b_seed'] as String));

    final a = LoopbackNode('A', aKp, (m, p) => utf8.encode('null'));
    final b = LoopbackNode('B', bKp, (m, p) => utf8.encode('null'));

    final enfChain = Uint8List.fromList(base64.decode(v['enforcement_chain'] as String));
    final enfGenesis = Uint8List.fromList(base64.decode(v['enforcement_genesis_head'] as String));

    final pinnedLink = MultiNodeLoopbackLink({'A': a, 'B': b}, trustChain: enfChain);
    final pinned = E2EClient(pinnedLink.incoming, pinnedLink.send, await generateKeyPair(), genesisHash: enfGenesis);
    await pinned.connect();
    expect(pinned.connectedNodeIds.toSet(), equals({'A'}),
        reason: 'pinned: only authorized node A should be connected');
    await pinned.close();

    final disabledChain = Uint8List.fromList(base64.decode(v['disabled_chain'] as String));
    final disabledGenesis = Uint8List.fromList(base64.decode(v['genesis_head'] as String));
    final disabledLink = MultiNodeLoopbackLink({'A': a, 'B': b}, trustChain: disabledChain);
    final disabledClient = E2EClient(disabledLink.incoming, disabledLink.send, await generateKeyPair(), genesisHash: disabledGenesis);
    await disabledClient.connect();
    expect(disabledClient.connectedNodeIds.toSet(), equals({'A', 'B'}),
        reason: 'disabled trust log: both nodes should connect');
    await disabledClient.close();

    final openLink = MultiNodeLoopbackLink({'A': a, 'B': b});
    final openClient = E2EClient(openLink.incoming, openLink.send, await generateKeyPair());
    await openClient.connect();
    expect(openClient.connectedNodeIds.toSet(), equals({'A', 'B'}),
        reason: 'open mode (no genesisHash): both nodes should connect');
    await openClient.close();
  });

  test('rollback anchor: seeding a stale chain then pulling a longer one advances', () async {
    final v = _tl();
    final a = LoopbackNode('A', await generateKeyPair(), (m, p) => Uint8List.fromList(utf8.encode('null')));
    final link = MultiNodeLoopbackLink({'A': a},
        trustChain: Uint8List.fromList(base64.decode(v['disabled_chain'] as String)));
    final client = E2EClient(link.incoming, link.send, await generateKeyPair(),
        genesisHash: Uint8List.fromList(base64.decode(v['genesis_head'] as String)),
        initialTrustChain: Uint8List.fromList(base64.decode(v['chain'] as String)));
    await client.connect();
    expect(client.trustChainBytes, equals(Uint8List.fromList(base64.decode(v['disabled_chain'] as String))));
    await client.close();
  });

  test('rollback anchor: a gateway serving a shorter (stale) chain is rejected', () async {
    final v = _tl();
    final a = LoopbackNode('A', await generateKeyPair(), (m, p) => Uint8List.fromList(utf8.encode('null')));
    final link = MultiNodeLoopbackLink({'A': a},
        trustChain: Uint8List.fromList(base64.decode(v['chain'] as String))); // gateway serves SHORT
    final client = E2EClient(link.incoming, link.send, await generateKeyPair(),
        genesisHash: Uint8List.fromList(base64.decode(v['genesis_head'] as String)),
        initialTrustChain: Uint8List.fromList(base64.decode(v['disabled_chain'] as String))); // anchored LONG
    await client.connect();
    expect(client.trustChainBytes, equals(Uint8List.fromList(base64.decode(v['disabled_chain'] as String))));
    await client.close();
  });

  test('rollback anchor: a tampered seed is dropped, then the gateway chain is adopted', () async {
    final v = _tl();
    final tampered = Uint8List.fromList(base64.decode(v['chain'] as String))..[20] ^= 0xff;
    final a = LoopbackNode('A', await generateKeyPair(), (m, p) => Uint8List.fromList(utf8.encode('null')));
    final link = MultiNodeLoopbackLink({'A': a},
        trustChain: Uint8List.fromList(base64.decode(v['chain'] as String)));
    final client = E2EClient(link.incoming, link.send, await generateKeyPair(),
        genesisHash: Uint8List.fromList(base64.decode(v['genesis_head'] as String)),
        initialTrustChain: tampered);
    await client.connect();
    expect(client.trustChainBytes, equals(Uint8List.fromList(base64.decode(v['chain'] as String))));
    await client.close();
  });

  test('pinned client swallows trustlog.sync errors and stays fail-closed', () async {
    final v = _tl();
    final enfGenesis = Uint8List.fromList(base64.decode(v['enforcement_genesis_head'] as String));

    final aKp = await keyPairFromSeed(base64.decode(v['enforcement_node_a_seed'] as String));
    final bKp = await keyPairFromSeed(base64.decode(v['enforcement_node_b_seed'] as String));
    final a = LoopbackNode('A', aKp, (m, p) => utf8.encode('null'));
    final b = LoopbackNode('B', bKp, (m, p) => utf8.encode('null'));

    final link = MultiNodeLoopbackLink({'A': a, 'B': b});
    final client = E2EClient(link.incoming, link.send, await generateKeyPair(), genesisHash: enfGenesis);
    await client.connect();
    expect(client.connectedNodeIds.toSet(), isEmpty,
        reason: 'empty trust log: no authorized nodes; fail-closed means 0 connections');
    await client.close();
  });

  test('tofu: first-connect adopts the gateway chain and enforces (opens A, skips B)', () async {
    final v = _tl();
    final aKp = await keyPairFromSeed(base64.decode(v['enforcement_node_a_seed'] as String));
    final bKp = await keyPairFromSeed(base64.decode(v['enforcement_node_b_seed'] as String));
    final a = LoopbackNode('A', aKp, (m, p) => Uint8List.fromList(utf8.encode('null')));
    final b = LoopbackNode('B', bKp, (m, p) => Uint8List.fromList(utf8.encode('null')));
    final link = MultiNodeLoopbackLink({'A': a, 'B': b},
        trustChain: Uint8List.fromList(base64.decode(v['enforcement_chain'] as String)));
    final client = E2EClient(link.incoming, link.send, await generateKeyPair(), tofu: true);
    await client.connect();
    expect(client.connectedNodeIds.toSet(), {'A'});
    expect(client.trustTip, isNotNull);
    await client.close();
  });

  test('tofu: an empty pull is an open network — all nodes open', () async {
    final a = LoopbackNode('A', await generateKeyPair(), (m, p) => Uint8List.fromList(utf8.encode('null')));
    final b = LoopbackNode('B', await generateKeyPair(), (m, p) => Uint8List.fromList(utf8.encode('null')));
    final link = MultiNodeLoopbackLink({'A': a, 'B': b});
    final client = E2EClient(link.incoming, link.send, await generateKeyPair(), tofu: true);
    await client.connect();
    expect(client.connectedNodeIds.toSet(), {'A', 'B'});
    await client.close();
  });

  test('tofu: a persisted (seeded) chain re-anchors — a shorter gateway chain is rejected', () async {
    final v = _tl();
    final a = LoopbackNode('A', await generateKeyPair(), (m, p) => Uint8List.fromList(utf8.encode('null')));
    final link = MultiNodeLoopbackLink({'A': a},
        trustChain: Uint8List.fromList(base64.decode(v['chain'] as String))); // gateway serves SHORT
    final client = E2EClient(link.incoming, link.send, await generateKeyPair(),
        tofu: true,
        initialTrustChain: Uint8List.fromList(base64.decode(v['disabled_chain'] as String))); // seed LONG
    await client.connect();
    expect(client.trustChainBytes, equals(Uint8List.fromList(base64.decode(v['disabled_chain'] as String))));
    await client.close();
  });
}
