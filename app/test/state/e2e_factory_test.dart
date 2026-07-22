import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/data/client_identity_store.dart';
import 'package:argus/data/trust_chain_store.dart';
import 'package:argus/e2e/e2e.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/transport/connection.dart';
import '../e2e/loopback.dart';

class _MemKv implements SecureKv {
  final _m = <String, String>{};
  @override Future<String?> read(String k) async => _m[k];
  @override Future<void> write(String k, String v) async => _m[k] = v;
  @override Future<void> delete(String k) async => _m.remove(k);
}

Map<String, dynamic> _tl() => (jsonDecode(
    File('test/e2e/testdata/vectors.json').readAsStringSync()) as Map<String, dynamic>)['trustlog'] as Map<String, dynamic>;

void main() {
  test('open network: builds an E2E client and connects (no persisted chain)', () async {
    final node = LoopbackNode('A', await generateKeyPair(), (m, p) => Uint8List.fromList(utf8.encode('null')));
    final link = MultiNodeLoopbackLink({'A': node});
    final kv = _MemKv();
    final c = await buildE2EClient(link.incoming, link.send, ClientIdentityStore(kv), TrustChainStore(kv));
    expect((c as E2EClient).connectedNodeIds, contains('A'));
    await c.close();
  });

  test('a tampered stored trust chain refuses to connect (no re-TOFU)', () async {
    final v = _tl();
    final kv = _MemKv();
    // Seed a corrupt chain into the store.
    await TrustChainStore(kv).save(Uint8List.fromList(base64.decode(v['chain'] as String))..[20] ^= 0xff);
    final node = LoopbackNode('A', await generateKeyPair(), (m, p) => Uint8List.fromList(utf8.encode('null')));
    final link = MultiNodeLoopbackLink({'A': node},
        trustChain: Uint8List.fromList(base64.decode(v['chain'] as String)));
    await expectLater(
        buildE2EClient(link.incoming, link.send, ClientIdentityStore(kv), TrustChainStore(kv)),
        throwsA(isA<FatalConnectError>()));
  });

  test('tofu first-connect: adopted chain is persisted in the store', () async {
    final v = _tl();
    final aKp = await keyPairFromSeed(base64.decode(v['enforcement_node_a_seed'] as String));
    final node = LoopbackNode('A', aKp, (m, p) => Uint8List.fromList(utf8.encode('null')));
    final enfChain = Uint8List.fromList(base64.decode(v['enforcement_chain'] as String));
    final link = MultiNodeLoopbackLink({'A': node}, trustChain: enfChain);
    final kv = _MemKv();
    final c = await buildE2EClient(link.incoming, link.send, ClientIdentityStore(kv), TrustChainStore(kv));
    final stored = await TrustChainStore(kv).load();
    expect(stored, equals(enfChain));
    await c.close();
  });

  test('re-anchor: an advanced gateway chain replaces the shorter stored chain', () async {
    final v = _tl();
    final node = LoopbackNode('A', await generateKeyPair(), (m, p) => Uint8List.fromList(utf8.encode('null')));
    final shortChain = Uint8List.fromList(base64.decode(v['chain'] as String));
    final longChain = Uint8List.fromList(base64.decode(v['disabled_chain'] as String));
    final link = MultiNodeLoopbackLink({'A': node}, trustChain: longChain);
    final kv = _MemKv();
    await TrustChainStore(kv).save(shortChain);
    final c = await buildE2EClient(link.incoming, link.send, ClientIdentityStore(kv), TrustChainStore(kv));
    final stored = await TrustChainStore(kv).load();
    expect(stored, equals(longChain));
    await c.close();
  });
}
