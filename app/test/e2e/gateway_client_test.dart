import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

import 'loopback.dart';

void main() {
  test('notifications fans out one decoded event to multiple listeners', () async {
    final node = LoopbackNode('A', await generateKeyPair(),
        (m, p) => Uint8List.fromList(utf8.encode('null')));
    final link = MultiNodeLoopbackLink({'A': node});
    final client = E2EClient(link.incoming, link.send, await generateKeyPair());
    await client.connect();

    final a = client.notifications.firstWhere((m) => m.method == 'session.event');
    final b = client.notifications.firstWhere((m) => m.method == 'session.event');
    node.emitNotification('session.event',
        Uint8List.fromList(utf8.encode('{"type":"updated","session":{"id":"s1"}}')));
    final ra = await a.timeout(const Duration(seconds: 2));
    final rb = await b.timeout(const Duration(seconds: 2));
    expect(ra.method, 'session.event');
    expect(rb.method, 'session.event');
    expect((ra.params as Map)['session'], equals((rb.params as Map)['session']));
    await client.close();
  });
}
