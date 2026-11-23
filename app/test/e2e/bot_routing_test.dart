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

NodeHandler _bots(List<Map<String, dynamic>> bots, [String primary = '']) =>
    (m, p) => _json({
      'bots': bots,
      if (primary.isNotEmpty) 'primary': primary,
    });

void main() {
  test('bot.list merges nodes with composite ids in node order', () async {
    final f = await _connect(
      _bots([
        {'id': 'a1', 'name': 'x', 'status': 'idle', 'primary': true},
      ], 'a1'),
      _bots([
        {'id': 'a1', 'name': 'y', 'status': 'working', 'primary': true},
        {'id': 'a2', 'name': 'z', 'status': 'idle'},
      ], 'a1'),
    );
    final res = await f.client.call('bot.list', {}) as Map;
    final bots = (res['bots'] as List).cast<Map>();
    expect(bots.map((a) => a['id']), ['A:a1', 'B:a1', 'B:a2']);
    expect(bots[0]['node_id'], 'A');
    expect(bots[0]['node_label'], 'A-box');
    expect(bots[0]['primary'], true);
    expect(bots[1]['primary'], true, reason: 'each node keeps its primary');
    expect(bots[2]['primary'], isNot(true));
    expect(res.containsKey('primary'), false);
    expect(f.a.requests.where((r) => r.method == 'bot.setPrimary'), isEmpty);
    expect(f.b.requests.where((r) => r.method == 'bot.setPrimary'), isEmpty);
    await f.client.close();
  });

  test('an offline node keeps its bots as unreachable until removed', () async {
    final f = await _connect(
      _bots([
        {'id': 'a1', 'status': 'idle'},
      ]),
      _bots([
        {'id': 'b1', 'status': 'working', 'primary': true},
      ]),
    );
    await f.client.call('bot.list', {});
    final seen = <String>[];
    final sub = f.client.notifications.listen((m) => seen.add(m.method ?? ''));

    f.lnk.pushNotification('node.event', {
      'type': 'offline',
      'node': _descriptor('B', f.b.keyPair, online: false),
    });
    await _until(() => !f.client.connectedNodeIds.contains('B'));
    expect(seen, contains('bot.changed'));

    var res = await f.client.call('bot.list', {}) as Map;
    var bots = (res['bots'] as List).cast<Map>();
    expect(bots.map((a) => '${a['id']}:${a['status']}'), [
      'A:a1:idle',
      'B:b1:unreachable',
    ]);
    expect(bots[1]['primary'], true);

    f.lnk.pushNotification('node.event', {
      'type': 'removed',
      'node': _descriptor('B', f.b.keyPair, online: false),
    });
    await _until(() => seen.where((m) => m == 'bot.changed').length > 1);
    res = await f.client.call('bot.list', {}) as Map;
    bots = (res['bots'] as List).cast<Map>();
    expect(bots.map((a) => a['id']), ['A:a1']);
    await sub.cancel();
    await f.client.close();
  });

  test('a shared cache keeps an offline node\'s bots across reconnects', () async {
    final cache = <String, List<Map<String, dynamic>>>{};
    final ka = await generateKeyPair();
    final kb = await generateKeyPair();
    final handlerA = _bots([
      {'id': 'a1', 'status': 'idle'},
    ]);
    final handlerB = _bots([
      {'id': 'b1', 'status': 'working'},
    ]);
    Future<List<String>> listOn(MultiNodeLoopbackLink lnk) async {
      final client = E2EClient(
        lnk.incoming,
        lnk.send,
        await generateKeyPair(),
        botCache: cache,
      );
      await client.connect();
      await client.channelsSettled;
      final res = await client.call('bot.list', {}) as Map;
      await client.close();
      return [
        for (final a in (res['bots'] as List).cast<Map>())
          '${a['id']}:${a['status']}',
      ];
    }

    final both = {
      'A': LoopbackNode('A', ka, handlerA),
      'B': LoopbackNode('B', kb, handlerB),
    };
    expect(await listOn(MultiNodeLoopbackLink(both)), ['A:a1:idle', 'B:b1:working']);
    // Reconnected while B is offline: a new client, the same cache.
    final offline = {
      'A': LoopbackNode('A', ka, handlerA),
      'B': LoopbackNode('B', kb, handlerB),
    };
    expect(
      await listOn(MultiNodeLoopbackLink(offline, offline: {'B'})),
      ['A:a1:idle', 'B:b1:unreachable'],
    );
    // Reconnected after B left the roster: its bots are gone.
    expect(
      await listOn(MultiNodeLoopbackLink({'A': LoopbackNode('A', ka, handlerA)})),
      ['A:a1:idle'],
    );
  });

  test('bot.changed from a node is stamped with its node id', () async {
    final f = await _connect(_bots([]), _bots([]));
    final got = Completer<Object?>();
    final sub = f.client.notifications.listen((m) {
      if (m.method == 'bot.changed' && !got.isCompleted) got.complete(m.params);
    });
    f.b.emitNotification('bot.changed', utf8.encode('{}'));
    expect(await got.future.timeout(const Duration(seconds: 3)), {'node_id': 'B'});
    await sub.cancel();
    await f.client.close();
  });

  test('setPrimary routes to the bot node only', () async {
    final f = await _connect((m, p) => _json(null), (m, p) => _json(null));
    await f.client.call('bot.setPrimary', {'bot_id': 'B:a2'});
    final bReq = f.b.requests.where((r) => r.method == 'bot.setPrimary');
    expect(_params(bReq.single.params), {'bot_id': 'a2'});
    expect(f.a.requests.where((r) => r.method == 'bot.setPrimary'), isEmpty);
    await f.client.close();
  });

  test('unsetting the primary names its node', () async {
    final f = await _connect((m, p) => _json(null), (m, p) => _json(null));
    await expectLater(
      f.client.call('bot.setPrimary', {}),
      throwsA(isA<RpcError>()),
    );
    await f.client.call('bot.setPrimary', {'node_id': 'A'});
    final aReq = f.a.requests.where((r) => r.method == 'bot.setPrimary');
    expect(_params(aReq.single.params), {'node_id': 'A'});
    expect(f.b.requests.where((r) => r.method == 'bot.setPrimary'), isEmpty);
    await f.client.close();
  });

  test('bot-addressed calls split the id and composite the result', () async {
    final f = await _connect(
      (m, p) => _json(null),
      (m, p) {
        final params = _params(p);
        return switch (m) {
          'bot.thread.send' => _json({'thread_id': 't1', 'session_id': 's1'}),
          'bot.thread.list' => _json({
            'threads': [
              {'id': 't1', 'bot_id': 'a1', 'session_id': 's1'},
              {'id': 't2', 'bot_id': 'a1'},
            ],
          }),
          'bot.show' => _json({'id': params['bot_id'], 'name': 'n'}),
          'bot.memory.log' => _json({
            'commits': [
              {'hash': 'h1', 'thread_id': 't1'},
              {'hash': 'h2'},
            ],
          }),
          'bot.memory.proposals' => _json({
            'proposals': [
              {'id': 'mp1', 'bot_id': 'a1', 'thread_id': 't1', 'ops': []},
            ],
          }),
          _ => _json(null),
        };
      },
    );
    final send = await f.client.call('bot.thread.send', {
      'bot_id': 'B:a1',
      'thread_id': 'B:t1',
      'text': 'hi',
    }) as Map;
    expect(send, {'thread_id': 'B:t1', 'session_id': 'B:s1'});
    expect(_params(f.b.requests.last.params), {
      'bot_id': 'a1',
      'thread_id': 't1',
      'text': 'hi',
    });

    final threads = await f.client.call('bot.thread.list', {
      'bot_id': 'B:a1',
    }) as Map;
    final list = (threads['threads'] as List).cast<Map>();
    expect(list[0], {'id': 'B:t1', 'bot_id': 'B:a1', 'session_id': 'B:s1'});
    expect(list[1].containsKey('session_id'), false);

    final show = await f.client.call('bot.show', {'bot_id': 'B:a1'}) as Map;
    expect(show['id'], 'B:a1');
    expect(show['node_id'], 'B');

    final log = await f.client.call('bot.memory.log', {'bot_id': 'B:a1'}) as Map;
    final commits = (log['commits'] as List).cast<Map>();
    expect(commits[0]['thread_id'], 'B:t1');
    expect(commits[1].containsKey('thread_id'), false);

    final props = await f.client.call('bot.memory.proposals', {
      'bot_id': 'B:a1',
    }) as Map;
    expect((props['proposals'] as List).single, {
      'id': 'B:mp1',
      'bot_id': 'B:a1',
      'thread_id': 'B:t1',
      'ops': [],
    });
    await f.client.close();
  });

  test('a memory undo from a thread splits the thread id', () async {
    Map<String, dynamic>? got;
    final f = await _connect((m, p) => _json(null), (m, p) {
      if (m == 'bot.memory.undo') got = _params(p);
      return _json({'commit': 'rev'});
    });
    await f.client.call('bot.memory.undo', {
      'bot_id': 'B:a1',
      'commit': 'abc',
      'thread_id': 'B:t1',
    });
    expect(got, {'bot_id': 'a1', 'commit': 'abc', 'thread_id': 't1'});
    await expectLater(
      f.client.call('bot.memory.undo', {
        'bot_id': 'B:a1',
        'commit': 'abc',
        'thread_id': 'A:t1',
      }),
      throwsA(isA<RpcError>()),
    );
    await f.client.close();
  });

  test('a thread from another node or a bare id is rejected', () async {
    final f = await _connect((m, p) => _json(null), (m, p) => _json(null));
    await expectLater(
      f.client.call('bot.thread.send', {
        'bot_id': 'B:a1',
        'thread_id': 'A:t1',
        'text': 'x',
      }),
      throwsA(isA<RpcError>()),
    );
    await expectLater(
      f.client.call('bot.show', {'bot_id': 'bare'}),
      throwsA(isA<RpcError>()),
    );
    await f.client.close();
  });

  test('thread-addressed and proposal calls route by their id', () async {
    final f = await _connect(
      (m, p) => _json(null),
      (m, p) => m == 'bot.thread.timeline'
          ? _json({
              'thread_id': 't1',
              'entries': [],
              'sessions': [
                {'session_id': 's1', 'start_index': 0, 'live': true},
              ],
            })
          : _json(null),
    );
    final tl = await f.client.call('bot.thread.timeline', {
      'thread_id': 'B:t1',
    }) as Map;
    expect(tl['thread_id'], 'B:t1');
    expect(((tl['sessions'] as List).single as Map)['session_id'], 'B:s1');
    expect(
      _params(
        f.b.requests.firstWhere((r) => r.method == 'bot.thread.timeline').params,
      ),
      {'thread_id': 't1'},
    );

    await f.client.call('bot.thread.markRead', {'thread_id': 'B:t1'});
    await f.client.call('bot.memory.decide', {
      'proposal_id': 'B:mp1',
      'accept': true,
    });
    expect(_params(f.b.requests.last.params), {
      'proposal_id': 'mp1',
      'accept': true,
    });
    expect(f.a.requests.where((r) => r.method.startsWith('bot.')), isEmpty);
    await f.client.close();
  });

  test('create and add go to the named node and stamp the bot', () async {
    final f = await _connect(
      (m, p) => _json(null),
      (m, p) => m == 'bot.create'
          ? _json({'id': 'a9', 'name': 'n'})
          : _json({
              'bot': {'id': 'a8', 'name': 'm'},
              'found_instructions': true,
            }),
    );
    final created = await f.client.call('bot.create', {
      'name': 'n',
      'node_id': 'B',
    }) as Map;
    expect(created['id'], 'B:a9');
    expect(created['node_id'], 'B');
    final added = await f.client.call('bot.add', {
      'dir': '/x',
      'node_id': 'B',
    }) as Map;
    expect((added['bot'] as Map)['id'], 'B:a8');
    expect(added['found_instructions'], true);
    await f.client.close();
  });

  test('activity, secrets and drafts route to their node', () async {
    final f = await _connect(
      (m, p) => _json(null),
      (m, p) => switch (m) {
        'bot.activity' => _json({
          'activity': [
            {'id': 1, 'thread_id': 't1', 'session_id': 's1'},
            {'id': 2},
          ],
        }),
        'bot.secret.list' => _json({'names': ['K']}),
        _ => _json(null),
      },
    );
    final act = await f.client.call('bot.activity', {
      'bot_id': 'B:a1',
    }) as Map;
    final rows = (act['activity'] as List).cast<Map>();
    expect(rows[0]['thread_id'], 'B:t1');
    expect(rows[0]['session_id'], 'B:s1');
    expect(rows[1].containsKey('thread_id'), false);
    expect(_params(f.b.requests.last.params)['bot_id'], 'a1');

    await f.client.call('bot.secret.list', {'bot_id': 'B:a1'});
    expect(f.b.requests.last.method, 'bot.secret.list');
    await f.client.call('bot.secret.set', {
      'bot_id': 'B:a1',
      'name': 'K',
      'value': 'v',
    });
    expect(_params(f.b.requests.last.params)['bot_id'], 'a1');

    await f.client.call('bot.draft.send', {
      'thread_id': 'B:t1',
      'event_id': 4,
    });
    expect(_params(f.b.requests.last.params)['thread_id'], 't1');
    expect(f.a.requests.where((r) => r.method.startsWith('bot.')), isEmpty);
    await f.client.close();
  });

  test('usage, set and offers route to their node', () async {
    final f = await _connect(
      (m, p) => _json(null),
      (m, p) => switch (m) {
        'bot.usage' => _json({
          'bot_id': 'a1',
          'rows': [],
          'total': {'sessions': 2},
        }),
        'bot.set' => _json({'commit': 'c1'}),
        'bot.offer.start' => _json({'thread_id': 't1', 'session_id': 's1'}),
        _ => _json(null),
      },
    );
    final usage = await f.client.call('bot.usage', {
      'bot_id': 'B:a1',
      'since': '2026-11-15T00:00:00Z',
    }) as Map;
    expect(usage['bot_id'], 'B:a1');
    expect(_params(f.b.requests.last.params)['bot_id'], 'a1');

    final set = await f.client.call('bot.set', {
      'bot_id': 'B:a1',
      'key': 'notify',
      'value': 'off',
    }) as Map;
    expect(set['commit'], 'c1');
    expect(_params(f.b.requests.last.params), {
      'bot_id': 'a1',
      'key': 'notify',
      'value': 'off',
    });

    final start = await f.client.call('bot.offer.start', {
      'thread_id': 'B:t1',
      'offer_id': 'of_1',
    }) as Map;
    expect(start, {'thread_id': 'B:t1', 'session_id': 'B:s1'});
    expect(_params(f.b.requests.last.params), {
      'thread_id': 't1',
      'offer_id': 'of_1',
    });
    await f.client.call('bot.offer.dismiss', {
      'thread_id': 'B:t1',
      'offer_id': 'of_1',
    });
    expect(f.b.requests.last.method, 'bot.offer.dismiss');
    expect(f.a.requests.where((r) => r.method.startsWith('bot.')), isEmpty);
    await f.client.close();
  });

  test('routine calls route by bot and composite their results', () async {
    final f = await _connect(
      (m, p) => _json(null),
      (m, p) => switch (m) {
        'bot.routine.list' => _json({
          'routines': [
            {'bot_id': 'a1', 'slug': 'billing', 'state': 'active'},
          ],
        }),
        'bot.routine.pause' || 'bot.routine.resume' => _json({
          'bot_id': 'a1',
          'slug': 'billing',
          'state': 'paused',
        }),
        'bot.routine.run' => _json({'run_id': 'r1'}),
        'bot.routine.runs' => _json({
          'runs': [
            {'id': 'r1', 'thread_id': 't1'},
            {'id': 'r2'},
          ],
        }),
        _ => _json(null),
      },
    );
    final list = await f.client.call('bot.routine.list', {
      'bot_id': 'B:a1',
    }) as Map;
    expect(((list['routines'] as List).single as Map)['bot_id'], 'B:a1');

    final paused = await f.client.call('bot.routine.pause', {
      'bot_id': 'B:a1',
      'slug': 'billing',
    }) as Map;
    expect(paused['bot_id'], 'B:a1');
    expect(
      _params(f.b.requests.last.params),
      {'bot_id': 'a1', 'slug': 'billing'},
    );
    final resumed = await f.client.call('bot.routine.resume', {
      'bot_id': 'B:a1',
      'slug': 'billing',
    }) as Map;
    expect(resumed['bot_id'], 'B:a1');

    final run = await f.client.call('bot.routine.run', {
      'bot_id': 'B:a1',
      'slug': 'billing',
    }) as Map;
    expect(run['run_id'], 'r1');

    final runs = await f.client.call('bot.routine.runs', {
      'bot_id': 'B:a1',
      'slug': 'billing',
    }) as Map;
    final rs = (runs['runs'] as List).cast<Map>();
    expect(rs[0]['thread_id'], 'B:t1');
    expect(rs[1].containsKey('thread_id'), false);
    expect(f.a.requests.where((r) => r.method.startsWith('bot.')), isEmpty);
    await f.client.close();
  });
}
