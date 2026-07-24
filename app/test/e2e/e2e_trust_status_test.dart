import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';
import '../e2e/loopback.dart';

Map<String, dynamic> _tl() => (jsonDecode(
    File('test/e2e/testdata/vectors.json').readAsStringSync()) as Map<String, dynamic>)['trustlog'] as Map<String, dynamic>;

void main() {
  test('open network: isLocked is null', () async {
    final node = LoopbackNode('A', await generateKeyPair(), (m, p) => Uint8List.fromList(utf8.encode('null')));
    final link = MultiNodeLoopbackLink({'A': node});
    final c = E2EClient(link.incoming, link.send, await generateKeyPair(), tofu: true);
    await c.connect();
    expect(c.isLocked, isNull);
    await c.close();
  });

  test('locked + authorized device: isLocked true, isAuthorized true', () async {
    final v = _tl();
    final aKp = await keyPairFromSeed(base64.decode(v['enforcement_node_a_seed'] as String));
    final node = LoopbackNode('A', aKp, (m, p) => Uint8List.fromList(utf8.encode('null')));
    final link = MultiNodeLoopbackLink({'A': node},
        trustChain: Uint8List.fromList(base64.decode(v['enforcement_chain'] as String)));
    // Authorize THIS client's static key by using node A's authorized identity as the client static.
    final c = E2EClient(link.incoming, link.send, aKp, tofu: true);
    await c.connect();
    expect(c.isLocked, isTrue);
    expect(c.isAuthorized, isTrue);
    expect(c.isDisabled, isFalse);
    await c.close();
  });

  test('locked + unauthorized device: isAuthorized false', () async {
    final v = _tl();
    final aKp = await keyPairFromSeed(base64.decode(v['enforcement_node_a_seed'] as String));
    final node = LoopbackNode('A', aKp, (m, p) => Uint8List.fromList(utf8.encode('null')));
    final link = MultiNodeLoopbackLink({'A': node},
        trustChain: Uint8List.fromList(base64.decode(v['enforcement_chain'] as String)));
    final c = E2EClient(link.incoming, link.send, await generateKeyPair(), tofu: true); // random (unauthorized) static
    await c.connect();
    expect(c.isLocked, isTrue);
    expect(c.isAuthorized, isFalse);
    await c.close();
  });

  test('disabled chain: isDisabled true, isLocked true', () async {
    final v = _tl();
    final node = LoopbackNode('A', await generateKeyPair(), (m, p) => Uint8List.fromList(utf8.encode('null')));
    final link = MultiNodeLoopbackLink({'A': node},
        trustChain: Uint8List.fromList(base64.decode(v['disabled_chain'] as String)));
    final c = E2EClient(link.incoming, link.send, await generateKeyPair(), tofu: true);
    await c.connect();
    expect(c.isDisabled, isTrue);
    expect(c.isLocked, isTrue);
    await c.close();
  });
}
