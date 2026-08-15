import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

import 'loopback.dart';

Uint8List _json(Object? v) => Uint8List.fromList(utf8.encode(jsonEncode(v)));

void main() {
  test('connect discovers nodes and call(sessions.list) merges + stamps origin', () async {
    final a = LoopbackNode('A', await generateKeyPair(),
        (m, p) => _json([{'id': 's1', 'title': 'a'}]));
    final b = LoopbackNode('B', await generateKeyPair(),
        (m, p) => _json([{'id': 's2', 'title': 'b'}]));
    final lnk = MultiNodeLoopbackLink({'A': a, 'B': b});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.connect();

    final list = (await client.call('sessions.list')) as List;
    final byId = {for (final s in list) (s as Map)['id']: s};
    expect(byId.keys.toSet(), {'A:s1', 'B:s2'});
    expect((byId['A:s1'] as Map)['node_id'], 'A');
    expect((byId['A:s1'] as Map)['node_label'], 'A-box');
    expect((byId['A:s1'] as Map)['offline'], false);
    await client.close();
  });

  test('a failing node is dropped from the fanout, others returned', () async {
    final a = LoopbackNode('A', await generateKeyPair(),
        (m, p) => _json([{'id': 's1'}]));
    final b = LoopbackNode('B', await generateKeyPair(),
        (m, p) => throw StateError('node B down'));
    final lnk = MultiNodeLoopbackLink({'A': a, 'B': b});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.connect();
    final list = (await client.call('sessions.list')) as List;
    expect(list.map((s) => (s as Map)['id']), ['A:s1']);
    await client.close();
  });

  test('historyProjects fanout sorts by last_activity descending', () async {
    final a = LoopbackNode('A', await generateKeyPair(),
        (m, p) => _json([{'name': 'pa', 'last_activity': '2026-07-01'}]));
    final b = LoopbackNode('B', await generateKeyPair(),
        (m, p) => _json([{'name': 'pb', 'last_activity': '2026-07-20'}]));
    final lnk = MultiNodeLoopbackLink({'A': a, 'B': b});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.connect();
    final list = (await client.call('sessions.historyProjects')) as List;
    expect(list.map((p) => (p as Map)['name']), ['pb', 'pa']); // newest first
    expect((list.first as Map)['node_id'], 'B');
    await client.close();
  });

  test('passthrough goes to the gateway, not a node', () async {
    final a = LoopbackNode('A', await generateKeyPair(), (m, p) => _json(null));
    final link = MultiNodeLoopbackLink({'A': a});
    final client = E2EClient(link.incoming, link.send, await generateKeyPair());
    await client.connect();
    // ping is gateway-native; returns null and does not reach a node handler.
    expect(await client.call('ping'), isNull);
    await client.close();
  });

  test('sessionAddressed splits the composite id and routes with the local id', () async {
    String? seenSessionId;
    final a = LoopbackNode('A', await generateKeyPair(), (m, p) {
      seenSessionId = (jsonDecode(utf8.decode(p)) as Map)['session_id'] as String?;
      return _json(null);
    });
    final lnk = MultiNodeLoopbackLink({'A': a});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.connect();
    await client.call('sessions.input', {'session_id': 'A:s1', 'data': 'x'});
    expect(seenSessionId, 's1'); // node saw the LOCAL id, not the composite
    await client.close();
  });

  test('sessions.spawn (nodeAddressed, compositeResult) composites the result id', () async {
    final a = LoopbackNode('A', await generateKeyPair(), (m, p) => _json({'session_id': 's9'}));
    final lnk = MultiNodeLoopbackLink({'A': a});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.connect();
    final r = await client.call('sessions.spawn', {'node_id': 'A'}) as Map;
    expect(r['session_id'], 'A:s9');
    await client.close();
  });

  test('sessions.historySessions stamps items with node origin', () async {
    final a = LoopbackNode('A', await generateKeyPair(),
        (m, p) => _json({'items': [{'session_id': 'x'}], 'has_more': false}));
    final lnk = MultiNodeLoopbackLink({'A': a});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.connect();
    final r = await client.call('sessions.historySessions', {'node_id': 'A'}) as Map;
    expect(((r['items'] as List).first as Map)['node_id'], 'A');
    await client.close();
  });

  test('nodeAddressed with no node_id uses the sole node, errors with >1', () async {
    final a = LoopbackNode('A', await generateKeyPair(), (m, p) => _json({'agents': []}));
    final oneLnk = MultiNodeLoopbackLink({'A': a});
    final one = E2EClient(oneLnk.incoming, oneLnk.send, await generateKeyPair());
    await one.connect();
    expect(await one.call('agents.list'), isA<Map>()); // sole node
    await one.close();

    final b = LoopbackNode('B', await generateKeyPair(), (m, p) => _json({'agents': []}));
    final twoLnk = MultiNodeLoopbackLink({'A': a, 'B': b});
    final two = E2EClient(twoLnk.incoming, twoLnk.send, await generateKeyPair());
    await two.connect();
    await expectLater(two.call('agents.list'), throwsA(anything)); // ambiguous
    await two.close();
  });

  test('transcript.subscribe records the handle; unsubscribe routes to that node', () async {
    var unsubNode = '';
    final a = LoopbackNode('A', await generateKeyPair(), (m, p) {
      if (m == 'transcript.unsubscribe') unsubNode = 'A';
      return _json(null);
    });
    final b = LoopbackNode('B', await generateKeyPair(), (m, p) => _json(null));
    final lnk = MultiNodeLoopbackLink({'A': a, 'B': b});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.connect();
    await client.call('transcript.subscribe', {'session_id': 'A:s1', 'sub_id': 'sub-1'});
    await client.call('transcript.unsubscribe', {'sub_id': 'sub-1'});
    expect(unsubNode, 'A');
    await client.close();
  });

  test('session.event notifications get composite-stamped session origin', () async {
    final a = LoopbackNode('A', await generateKeyPair(), (m, p) => _json(null));
    final lnk = MultiNodeLoopbackLink({'A': a});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.connect();
    // openChannel already happened in connect(); emit after a subscription exists.
    final got = client.aggregatedEvents.firstWhere((e) => e.method == 'session.event');
    a.emitNotification('session.event', _json({'type': 'updated', 'session': {'id': 's1'}}));
    final ev = await got.timeout(const Duration(seconds: 2));
    final sess = (ev.params as Map)['session'] as Map;
    expect(sess['id'], 'A:s1');
    expect(sess['node_id'], 'A');
    await client.close();
  });

  test('non-session.event notifications pass through decoded, unstamped', () async {
    final a = LoopbackNode('A', await generateKeyPair(), (m, p) => _json(null));
    final lnk = MultiNodeLoopbackLink({'A': a});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.connect();
    final got = client.aggregatedEvents.firstWhere((e) => e.method == 'transcript.delta');
    a.emitNotification('transcript.delta', _json({'sub_id': 'x', 'chunks': []}));
    final ev = await got.timeout(const Duration(seconds: 2));
    expect((ev.params as Map)['sub_id'], 'x');
    await client.close();
  });
}
