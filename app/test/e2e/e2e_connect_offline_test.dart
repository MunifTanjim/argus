import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

import 'loopback.dart';

Uint8List _json(Object? v) => Uint8List.fromList(utf8.encode(jsonEncode(v)));

void main() {
  test('connect skips an offline node without attempting relay.open', () async {
    final a = LoopbackNode(
      'A',
      await generateKeyPair(),
      (m, p) => _json([
        {'id': 's1'},
      ]),
    );
    final b = LoopbackNode(
      'B',
      await generateKeyPair(),
      (m, p) => _json([
        {'id': 's2'},
      ]),
    );
    final lnk = MultiNodeLoopbackLink({'A': a, 'B': b}, offline: {'B'});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());

    await client.connect(); // must not throw because one node is offline

    final list = (await client.call('sessions.list')) as List;
    expect(list.map((s) => (s as Map)['id']), ['A:s1']); // only A connected
    expect(lnk.relayOpenCalls, ['A']); // B skipped before relay.open
    await client.close();
  });

  test(
    'connect tolerates a node whose relay.open fails and connects the rest',
    () async {
      final a = LoopbackNode(
        'A',
        await generateKeyPair(),
        (m, p) => _json([
          {'id': 's1'},
        ]),
      );
      final b = LoopbackNode(
        'B',
        await generateKeyPair(),
        (m, p) => _json([
          {'id': 's2'},
        ]),
      );
      final lnk = MultiNodeLoopbackLink({'A': a, 'B': b}, failRelayOpen: {'B'});
      final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());

      await client
          .connect(); // must not throw when one node's relay.open errors

      final list = (await client.call('sessions.list')) as List;
      expect(list.map((s) => (s as Map)['id']), ['A:s1']);
      await client.close();
    },
  );
}
