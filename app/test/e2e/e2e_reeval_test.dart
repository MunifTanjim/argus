import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

import 'loopback.dart';

Map<String, dynamic> _tl() => (jsonDecode(
        File('test/e2e/testdata/vectors.json').readAsStringSync())
    as Map<String, dynamic>)['trustlog'] as Map<String, dynamic>;

Uint8List _b(Map<String, dynamic> v, String k) =>
    Uint8List.fromList(base64.decode(v[k] as String));

void main() {
  test('re-sync drops a node revoked mid-session, keeps the rest', () async {
    final v = _tl();
    final aKp = await keyPairFromSeed(_b(v, 'enforcement_node_a_seed'));
    final bKp = await keyPairFromSeed(_b(v, 'enforcement_node_b_seed'));
    final a = LoopbackNode('A', aKp, (m, p) => Uint8List.fromList(utf8.encode('null')));
    final b = LoopbackNode('B', bKp, (m, p) => Uint8List.fromList(utf8.encode('null')));
    final link = MultiNodeLoopbackLink({'A': a, 'B': b},
        trustChain: _b(v, 'reeval_initial_chain'));

    Uint8List? advanced;
    final client = E2EClient(
      link.incoming,
      link.send,
      await generateKeyPair(),
      tofu: true,
      onTrustChainAdvance: (c) async => advanced = c,
    );
    await client.connect();
    expect(client.connectedNodeIds.toSet(), {'A', 'B'});

    link.trustChain = _b(v, 'reeval_revoke_b_chain');
    await client.resyncNow();

    expect(client.connectedNodeIds.toSet(), {'A'});
    expect(advanced, equals(_b(v, 'reeval_revoke_b_chain')));
    await client.close();
  });

  test('re-sync with no advance keeps all channels and does not persist', () async {
    final v = _tl();
    final aKp = await keyPairFromSeed(_b(v, 'enforcement_node_a_seed'));
    final bKp = await keyPairFromSeed(_b(v, 'enforcement_node_b_seed'));
    final a = LoopbackNode('A', aKp, (m, p) => Uint8List.fromList(utf8.encode('null')));
    final b = LoopbackNode('B', bKp, (m, p) => Uint8List.fromList(utf8.encode('null')));
    final link = MultiNodeLoopbackLink({'A': a, 'B': b},
        trustChain: _b(v, 'reeval_initial_chain'));

    var advances = 0;
    final client = E2EClient(link.incoming, link.send, await generateKeyPair(),
        tofu: true, onTrustChainAdvance: (c) async => advances++);
    await client.connect();
    expect(client.connectedNodeIds.toSet(), {'A', 'B'});

    await client.resyncNow();
    expect(client.connectedNodeIds.toSet(), {'A', 'B'});
    expect(advances, 0);
    await client.close();
  });
}
