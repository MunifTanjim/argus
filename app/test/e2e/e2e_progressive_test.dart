import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

import 'loopback.dart';

Uint8List _json(Object? v) => Uint8List.fromList(utf8.encode(jsonEncode(v)));

void main() {
  test(
    'forEachNodeSessions fires per-node as each responds, not gated on slowest',
    () async {
      final gate = Completer<void>();
      final a = LoopbackNode(
        'A',
        await generateKeyPair(),
        (m, p) => _json([
          {'id': 'sA', 'title': 'fast'},
        ]),
      );
      final b = LoopbackNode(
        'B',
        await generateKeyPair(),
        (m, p) => _json([
          {'id': 'sB', 'title': 'slow'},
        ]),
      );
      b.gate = gate;

      final lnk = MultiNodeLoopbackLink({'A': a, 'B': b});
      final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
      await client.connect();

      final collected = <String, List<Map<String, dynamic>>>{};
      final fanout = client.forEachNodeSessions(
        'sessions.list',
        (nodeId, sessions) => collected[nodeId] = sessions,
      );

      // Pump the event loop a few times so A (ungated) resolves.
      for (var i = 0; i < 5; i++) {
        await Future(() {});
      }

      // A responded; B is still held behind the gate.
      expect(collected.containsKey('A'), isTrue, reason: 'A should have fired');
      expect(
        collected.containsKey('B'),
        isFalse,
        reason: 'B should still be gated',
      );
      expect(collected['A']!.first['id'], 'A:sA');

      // Release B and wait for full completion.
      gate.complete();
      await fanout;

      expect(
        collected.containsKey('B'),
        isTrue,
        reason: 'B should have fired after gate',
      );
      expect(collected['B']!.first['id'], 'B:sB');

      await client.close();
    },
  );

  test('forEachNodeSessions skips erroring node, other still fires', () async {
    final a = LoopbackNode(
      'A',
      await generateKeyPair(),
      (m, p) => _json([
        {'id': 'sA'},
      ]),
    );
    final b = LoopbackNode(
      'B',
      await generateKeyPair(),
      (m, p) => throw StateError('node B down'),
    );

    final lnk = MultiNodeLoopbackLink({'A': a, 'B': b});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.connect();

    final collected = <String, List<Map<String, dynamic>>>{};
    await client.forEachNodeSessions(
      'sessions.list',
      (nodeId, sessions) => collected[nodeId] = sessions,
    );

    expect(collected.containsKey('A'), isTrue);
    expect(collected['A']!.first['id'], 'A:sA');
    expect(
      collected.containsKey('B'),
      isFalse,
      reason: 'erroring node callback must not fire',
    );

    await client.close();
  });
}
