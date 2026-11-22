import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:argus/e2e/e2e.dart';
import 'package:argus/transport/jsonrpc.dart';
import 'package:flutter_test/flutter_test.dart';

import 'loopback.dart';

Uint8List _json(Object? v) => Uint8List.fromList(utf8.encode(jsonEncode(v)));

Map<String, dynamic> _descriptor(String id, KeyPair kp, {bool online = true}) =>
    {
      'id': id,
      'label': '$id-box',
      'identity_pubkey': base64.encode(kp.publicKey),
      'online': online,
    };

Future<void> _until(bool Function() done) async {
  final deadline = DateTime.now().add(const Duration(seconds: 3));
  while (DateTime.now().isBefore(deadline)) {
    if (done()) return;
    await Future<void>.delayed(const Duration(milliseconds: 20));
  }
}

Map<String, dynamic> _params(Uint8List raw) =>
    jsonDecode(utf8.decode(raw)) as Map<String, dynamic>;

typedef _Fixture = ({
  MultiNodeLoopbackLink lnk,
  E2EClient client,
  LoopbackNode a,
  LoopbackNode b,
});

Future<_Fixture> _connect(NodeHandler a, NodeHandler b) async {
  final na = LoopbackNode('A', await generateKeyPair(), a);
  final nb = LoopbackNode('B', await generateKeyPair(), b);
  final lnk = MultiNodeLoopbackLink({'A': na, 'B': nb});
  final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
  await client.connect();
  await client.channelsSettled;
  return (lnk: lnk, client: client, a: na, b: nb);
}

NodeHandler _agents(List<Map<String, dynamic>> agents, [String primary = '']) =>
    (m, p) => _json({
      'agents': agents,
      if (primary.isNotEmpty) 'primary': primary,
    });

void main() {
  test('pagent.list merges nodes with composite ids in node order', () async {
    final f = await _connect(
      _agents([
        {'id': 'a1', 'name': 'x', 'status': 'idle', 'primary': true},
      ], 'a1'),
      _agents([
        {'id': 'a1', 'name': 'y', 'status': 'working', 'primary': true},
        {'id': 'a2', 'name': 'z', 'status': 'idle'},
      ], 'a1'),
    );
    final res = await f.client.call('pagent.list', {}) as Map;
    final agents = (res['agents'] as List).cast<Map>();
    expect(agents.map((a) => a['id']), ['A:a1', 'B:a1', 'B:a2']);
    expect(agents[0]['node_id'], 'A');
    expect(agents[0]['node_label'], 'A-box');
    expect(agents[0]['primary'], true);
    expect(agents[1]['primary'], false, reason: 'only the first primary wins');
    expect(res['primary'], 'A:a1');
    await f.client.close();
  });

  test('primary falls back to a node-reported primary', () async {
    final f = await _connect(
      _agents([]),
      _agents([
        {'id': 'a1', 'status': 'idle'},
      ], 'a1'),
    );
    final res = await f.client.call('pagent.list', {}) as Map;
    expect(res['primary'], 'B:a1');
    await f.client.close();
  });

  test('an offline node keeps its agents as unreachable until removed', () async {
    final f = await _connect(
      _agents([
        {'id': 'a1', 'status': 'idle'},
      ]),
      _agents([
        {'id': 'b1', 'status': 'working', 'primary': true},
      ]),
    );
    await f.client.call('pagent.list', {});
    final seen = <String>[];
    final sub = f.client.notifications.listen((m) => seen.add(m.method ?? ''));

    f.lnk.pushNotification('node.event', {
      'type': 'offline',
      'node': _descriptor('B', f.b.keyPair, online: false),
    });
    await _until(() => !f.client.connectedNodeIds.contains('B'));
    expect(seen, contains('pagent.changed'));

    var res = await f.client.call('pagent.list', {}) as Map;
    var agents = (res['agents'] as List).cast<Map>();
    expect(agents.map((a) => '${a['id']}:${a['status']}'), [
      'A:a1:idle',
      'B:b1:unreachable',
    ]);
    expect(res['primary'], 'B:b1');

    f.lnk.pushNotification('node.event', {
      'type': 'removed',
      'node': _descriptor('B', f.b.keyPair, online: false),
    });
    await _until(() => seen.where((m) => m == 'pagent.changed').length > 1);
    res = await f.client.call('pagent.list', {}) as Map;
    agents = (res['agents'] as List).cast<Map>();
    expect(agents.map((a) => a['id']), ['A:a1']);
    await sub.cancel();
    await f.client.close();
  });

  test('pagent.changed from a node is stamped with its node id', () async {
    final f = await _connect(_agents([]), _agents([]));
    final got = Completer<Object?>();
    final sub = f.client.notifications.listen((m) {
      if (m.method == 'pagent.changed' && !got.isCompleted) got.complete(m.params);
    });
    f.b.emitNotification('pagent.changed', utf8.encode('{}'));
    expect(await got.future.timeout(const Duration(seconds: 3)), {'node_id': 'B'});
    await sub.cancel();
    await f.client.close();
  });

  test('setPrimary routes to the agent node and unsets the others', () async {
    final f = await _connect((m, p) => _json(null), (m, p) => _json(null));
    await f.client.call('pagent.setPrimary', {'agent_id': 'B:a2'});
    final bReq = f.b.requests.where((r) => r.method == 'pagent.setPrimary');
    final aReq = f.a.requests.where((r) => r.method == 'pagent.setPrimary');
    expect(_params(bReq.single.params), {'agent_id': 'a2'});
    expect(_params(aReq.single.params), isEmpty);

    await f.client.call('pagent.setPrimary', {});
    expect(
      f.a.requests.where((r) => r.method == 'pagent.setPrimary').length,
      2,
    );
    expect(
      f.b.requests.where((r) => r.method == 'pagent.setPrimary').length,
      2,
    );
    await f.client.close();
  });

  test('agent-addressed calls split the id and composite the result', () async {
    final f = await _connect(
      (m, p) => _json(null),
      (m, p) {
        final params = _params(p);
        return switch (m) {
          'pagent.thread.send' => _json({'thread_id': 't1', 'session_id': 's1'}),
          'pagent.thread.list' => _json({
            'threads': [
              {'id': 't1', 'agent_id': 'a1', 'session_id': 's1'},
              {'id': 't2', 'agent_id': 'a1'},
            ],
          }),
          'pagent.show' => _json({'id': params['agent_id'], 'name': 'n'}),
          'pagent.memory.log' => _json({
            'commits': [
              {'hash': 'h1', 'thread_id': 't1'},
              {'hash': 'h2'},
            ],
          }),
          'pagent.memory.proposals' => _json({
            'proposals': [
              {'id': 'mp1', 'agent_id': 'a1', 'thread_id': 't1', 'ops': []},
            ],
          }),
          _ => _json(null),
        };
      },
    );
    final send = await f.client.call('pagent.thread.send', {
      'agent_id': 'B:a1',
      'thread_id': 'B:t1',
      'text': 'hi',
    }) as Map;
    expect(send, {'thread_id': 'B:t1', 'session_id': 'B:s1'});
    expect(_params(f.b.requests.last.params), {
      'agent_id': 'a1',
      'thread_id': 't1',
      'text': 'hi',
    });

    final threads = await f.client.call('pagent.thread.list', {
      'agent_id': 'B:a1',
    }) as Map;
    final list = (threads['threads'] as List).cast<Map>();
    expect(list[0], {'id': 'B:t1', 'agent_id': 'B:a1', 'session_id': 'B:s1'});
    expect(list[1].containsKey('session_id'), false);

    final show = await f.client.call('pagent.show', {'agent_id': 'B:a1'}) as Map;
    expect(show['id'], 'B:a1');
    expect(show['node_id'], 'B');

    final log = await f.client.call('pagent.memory.log', {'agent_id': 'B:a1'}) as Map;
    final commits = (log['commits'] as List).cast<Map>();
    expect(commits[0]['thread_id'], 'B:t1');
    expect(commits[1].containsKey('thread_id'), false);

    final props = await f.client.call('pagent.memory.proposals', {
      'agent_id': 'B:a1',
    }) as Map;
    expect((props['proposals'] as List).single, {
      'id': 'B:mp1',
      'agent_id': 'B:a1',
      'thread_id': 'B:t1',
      'ops': [],
    });
    await f.client.close();
  });

  test('a thread from another node or a bare id is rejected', () async {
    final f = await _connect((m, p) => _json(null), (m, p) => _json(null));
    await expectLater(
      f.client.call('pagent.thread.send', {
        'agent_id': 'B:a1',
        'thread_id': 'A:t1',
        'text': 'x',
      }),
      throwsA(isA<RpcError>()),
    );
    await expectLater(
      f.client.call('pagent.show', {'agent_id': 'bare'}),
      throwsA(isA<RpcError>()),
    );
    await f.client.close();
  });

  test('thread-addressed and proposal calls route by their id', () async {
    final f = await _connect(
      (m, p) => _json(null),
      (m, p) => m == 'pagent.thread.timeline'
          ? _json({
              'thread_id': 't1',
              'entries': [],
              'sessions': [
                {'session_id': 's1', 'start_index': 0, 'live': true},
              ],
            })
          : _json(null),
    );
    final tl = await f.client.call('pagent.thread.timeline', {
      'thread_id': 'B:t1',
    }) as Map;
    expect(tl['thread_id'], 'B:t1');
    expect(((tl['sessions'] as List).single as Map)['session_id'], 'B:s1');
    expect(
      _params(
        f.b.requests.firstWhere((r) => r.method == 'pagent.thread.timeline').params,
      ),
      {'thread_id': 't1'},
    );

    await f.client.call('pagent.thread.markRead', {'thread_id': 'B:t1'});
    await f.client.call('pagent.memory.decide', {
      'proposal_id': 'B:mp1',
      'accept': true,
    });
    expect(_params(f.b.requests.last.params), {
      'proposal_id': 'mp1',
      'accept': true,
    });
    expect(f.a.requests.where((r) => r.method.startsWith('pagent.')), isEmpty);
    await f.client.close();
  });

  test('create and add go to the named node and stamp the agent', () async {
    final f = await _connect(
      (m, p) => _json(null),
      (m, p) => m == 'pagent.create'
          ? _json({'id': 'a9', 'name': 'n'})
          : _json({
              'agent': {'id': 'a8', 'name': 'm'},
              'found_instructions': true,
            }),
    );
    final created = await f.client.call('pagent.create', {
      'name': 'n',
      'node_id': 'B',
    }) as Map;
    expect(created['id'], 'B:a9');
    expect(created['node_id'], 'B');
    final added = await f.client.call('pagent.add', {
      'dir': '/x',
      'node_id': 'B',
    }) as Map;
    expect((added['agent'] as Map)['id'], 'B:a8');
    expect(added['found_instructions'], true);
    await f.client.close();
  });

  test('routine calls route by agent and composite their results', () async {
    final f = await _connect(
      (m, p) => _json(null),
      (m, p) => switch (m) {
        'pagent.routine.list' => _json({
          'routines': [
            {'agent_id': 'a1', 'slug': 'billing', 'state': 'active'},
          ],
        }),
        'pagent.routine.pause' || 'pagent.routine.resume' => _json({
          'agent_id': 'a1',
          'slug': 'billing',
          'state': 'paused',
        }),
        'pagent.routine.run' => _json({'run_id': 'r1'}),
        'pagent.routine.runs' => _json({
          'runs': [
            {'id': 'r1', 'thread_id': 't1'},
            {'id': 'r2'},
          ],
        }),
        _ => _json(null),
      },
    );
    final list = await f.client.call('pagent.routine.list', {
      'agent_id': 'B:a1',
    }) as Map;
    expect(((list['routines'] as List).single as Map)['agent_id'], 'B:a1');

    final paused = await f.client.call('pagent.routine.pause', {
      'agent_id': 'B:a1',
      'slug': 'billing',
    }) as Map;
    expect(paused['agent_id'], 'B:a1');
    expect(
      _params(f.b.requests.last.params),
      {'agent_id': 'a1', 'slug': 'billing'},
    );
    final resumed = await f.client.call('pagent.routine.resume', {
      'agent_id': 'B:a1',
      'slug': 'billing',
    }) as Map;
    expect(resumed['agent_id'], 'B:a1');

    final run = await f.client.call('pagent.routine.run', {
      'agent_id': 'B:a1',
      'slug': 'billing',
    }) as Map;
    expect(run['run_id'], 'r1');

    final runs = await f.client.call('pagent.routine.runs', {
      'agent_id': 'B:a1',
      'slug': 'billing',
    }) as Map;
    final rs = (runs['runs'] as List).cast<Map>();
    expect(rs[0]['thread_id'], 'B:t1');
    expect(rs[1].containsKey('thread_id'), false);
    expect(f.a.requests.where((r) => r.method.startsWith('pagent.')), isEmpty);
    await f.client.close();
  });
}
