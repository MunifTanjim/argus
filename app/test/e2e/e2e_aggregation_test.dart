import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';
import 'package:argus/transport/jsonrpc.dart';

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

  test('sessions.list composites workspace_id per node', () async {
    final a = LoopbackNode('A', await generateKeyPair(),
        (m, p) => _json([{'id': 's1', 'workspace_id': 'w1'}]));
    final b = LoopbackNode('B', await generateKeyPair(),
        (m, p) => _json([{'id': 's2', 'workspace_id': 'w1'}, {'id': 's3'}]));
    final lnk = MultiNodeLoopbackLink({'A': a, 'B': b});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.connect();

    final list = (await client.call('sessions.list')) as List;
    final byId = {for (final s in list) (s as Map)['id']: s as Map};
    expect(byId['A:s1']!['workspace_id'], 'A:w1');
    expect(byId['B:s2']!['workspace_id'], 'B:w1');
    expect(byId['B:s3']!['workspace_id'], isNull);
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
    expect(list.map((p) => (p as Map)['name']), ['pb', 'pa']);
    expect((list.first as Map)['node_id'], 'B');
    await client.close();
  });

  test('passthrough goes to the gateway, not a node', () async {
    final a = LoopbackNode('A', await generateKeyPair(), (m, p) => _json(null));
    final link = MultiNodeLoopbackLink({'A': a});
    final client = E2EClient(link.incoming, link.send, await generateKeyPair());
    await client.connect();
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
    expect(seenSessionId, 's1');
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
    expect(await one.call('agents.list'), isA<Map>());
    await one.close();

    final b = LoopbackNode('B', await generateKeyPair(), (m, p) => _json({'agents': []}));
    final twoLnk = MultiNodeLoopbackLink({'A': a, 'B': b});
    final two = E2EClient(twoLnk.incoming, twoLnk.send, await generateKeyPair());
    await two.connect();
    await expectLater(two.call('agents.list'), throwsA(anything));
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
    final got = client.aggregatedEvents.firstWhere((e) => e.method == 'session.event');
    a.emitNotification('session.event', _json({'type': 'updated', 'session': {'id': 's1'}}));
    final ev = await got.timeout(const Duration(seconds: 2));
    final sess = (ev.params as Map)['session'] as Map;
    expect(sess['id'], 'A:s1');
    expect(sess['node_id'], 'A');
    await client.close();
  });

  test('terminal.changed notifications carry the node id', () async {
    final a = LoopbackNode('A', await generateKeyPair(), (m, p) => _json(null));
    final lnk = MultiNodeLoopbackLink({'A': a});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.connect();
    final got = client.aggregatedEvents.firstWhere((e) => e.method == 'terminal.changed');
    a.emitNotification('terminal.changed', _json({}));
    final ev = await got.timeout(const Duration(seconds: 2));
    expect(ev.params, {'node_id': 'A'});
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

  test('project.list merges nodes and composites project and workspace ids',
      () async {
    final a = LoopbackNode('A', await generateKeyPair(), (m, p) => _json({
          'projects': [
            {
              'id': 'p1',
              'name': 'argus',
              'workspaces': [
                {'id': 'w1', 'dir': '/a'},
              ],
            },
          ],
        }));
    final b = LoopbackNode('B', await generateKeyPair(), (m, p) => _json({
          'projects': [
            {'id': 'p1', 'name': 'infra', 'workspaces': []},
          ],
        }));
    final lnk = MultiNodeLoopbackLink({'A': a, 'B': b});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.connect();

    final r = await client.call('project.list') as Map;
    final byId = {for (final p in r['projects'] as List) (p as Map)['id']: p};
    expect(byId.keys.toSet(), {'A:p1', 'B:p1'});
    expect(byId['A:p1']!['node_id'], 'A');
    expect(byId['A:p1']!['node_label'], 'A-box');
    expect(((byId['A:p1']!['workspaces'] as List).single as Map)['id'], 'A:w1');
    await client.close();
  });

  test('project.list drops a failing node and throws when all fail', () async {
    final a = LoopbackNode('A', await generateKeyPair(), (m, p) => _json({
          'projects': [
            {'id': 'p1', 'name': 'argus', 'workspaces': []},
          ],
        }));
    final b = LoopbackNode(
        'B', await generateKeyPair(), (m, p) => throw StateError('B down'));
    final lnk = MultiNodeLoopbackLink({'A': a, 'B': b});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.connect();
    final r = await client.call('project.list') as Map;
    expect((r['projects'] as List).map((p) => (p as Map)['id']), ['A:p1']);
    expect(r['failed_nodes'], ['B']);
    await client.close();

    final c = LoopbackNode(
        'C', await generateKeyPair(), (m, p) => throw StateError('C down'));
    final lnk2 = MultiNodeLoopbackLink({'C': c});
    final client2 =
        E2EClient(lnk2.incoming, lnk2.send, await generateKeyPair());
    await client2.connect();
    await expectLater(client2.call('project.list'), throwsA(anything));
    await client2.close();
  });

  test('workspace reads route by the composite id with the local id',
      () async {
    String? seenMethod;
    String? seenId;
    final a = LoopbackNode('A', await generateKeyPair(), (m, p) {
      seenMethod = m;
      seenId = (jsonDecode(utf8.decode(p)) as Map)['workspace_id'] as String?;
      return _json({'entries': []});
    });
    final lnk = MultiNodeLoopbackLink({'A': a});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.connect();
    await client.call('workspace.listDir', {'workspace_id': 'A:w:1', 'path': ''});
    expect(seenMethod, 'workspace.listDir');
    expect(seenId, 'w:1');
    await expectLater(
      client.call('workspace.readFile', {'workspace_id': 'bare', 'path': 'x'}),
      throwsA(isA<RpcError>().having((e) => e.code, 'code', -32600)),
    );
    await client.close();
  });

  test('workspace writes route by the composite id with the local id',
      () async {
    final seen = <(String, String?)>[];
    final a = LoopbackNode('A', await generateKeyPair(), (m, p) {
      seen.add((m, (jsonDecode(utf8.decode(p)) as Map)['workspace_id'] as String?));
      return _json(null);
    });
    final lnk = MultiNodeLoopbackLink({'A': a});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.connect();
    for (final m in [
      'workspace.remove',
      'workspace.setTarget',
      'workspace.runSetup',
      'workspace.setupLog',
    ]) {
      await client.call(m, {'workspace_id': 'A:w1'});
    }
    expect(seen, [
      ('workspace.remove', 'w1'),
      ('workspace.setTarget', 'w1'),
      ('workspace.runSetup', 'w1'),
      ('workspace.setupLog', 'w1'),
    ]);
    await client.close();
  });

  test('project methods route by the composite project id', () async {
    String? seenMethod;
    String? seenId;
    final a = LoopbackNode('A', await generateKeyPair(), (m, p) {
      seenMethod = m;
      seenId = (jsonDecode(utf8.decode(p)) as Map)['project_id'] as String?;
      return _json({'branches': []});
    });
    final lnk = MultiNodeLoopbackLink({'A': a});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.connect();
    await client.call('project.branches', {'project_id': 'A:p:1'});
    expect(seenMethod, 'project.branches');
    expect(seenId, 'p:1');
    await expectLater(
      client.call('project.rename', {'project_id': 'bare', 'name': 'x'}),
      throwsA(isA<RpcError>().having((e) => e.code, 'code', -32600)),
    );
    await expectLater(
      client.call('project.forget', {}),
      throwsA(isA<RpcError>().having((e) => e.code, 'code', -32600)),
    );
    await client.close();
  });

  test('workspace.create composites the result workspace id', () async {
    final a = LoopbackNode('A', await generateKeyPair(),
        (m, p) => _json({'workspace_id': 'w9', 'dir': '/src/x'}));
    final lnk = MultiNodeLoopbackLink({'A': a});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.connect();
    final r = await client.call('workspace.create', {'project_id': 'A:p1'}) as Map;
    expect(r['workspace_id'], 'A:w9');
    expect(r['dir'], '/src/x');
    await client.close();
  });

  test('terminal.list merges, composites, sorts, and lists failed nodes', () async {
    Uint8List list(String n) => _json({
          'terminals': [
            {'id': '@1', 'command': 'zsh-$n'},
            {'id': '@2', 'command': 'vim-$n'},
          ],
        });
    final b = LoopbackNode('B', await generateKeyPair(), (m, p) => list('B'));
    final a = LoopbackNode('A', await generateKeyPair(), (m, p) => list('A'));
    final c = LoopbackNode('C', await generateKeyPair(), (m, p) => throw StateError('boom'));
    final lnk = MultiNodeLoopbackLink({'B': b, 'A': a, 'C': c});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.connect();
    final r = await client.call('terminal.list') as Map;
    final ids = [for (final t in r['terminals'] as List) (t as Map)['id']];
    expect(ids, ['A:@1', 'A:@2', 'B:@1', 'B:@2']);
    expect(((r['terminals'] as List).first as Map)['node_label'], 'A-box');
    expect(r['failed_nodes'], ['C']);
    await client.close();
  });

  test('terminal.list treats method-not-found as a node without terminals', () async {
    final a = LoopbackNode('A', await generateKeyPair(),
        (m, p) => throw const RpcError(-32601, 'method not found: terminal.list'));
    final lnk = MultiNodeLoopbackLink({'A': a});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.connect();
    final r = await client.call('terminal.list') as Map;
    expect(r['terminals'], isEmpty);
    expect(r['failed_nodes'], isEmpty);
    await client.close();
  });

  test('terminal.open by terminal_id routes, then input follows the handle', () async {
    String? openId;
    var inputNode = '';
    final a = LoopbackNode('A', await generateKeyPair(), (m, p) {
      if (m == 'terminal.open') {
        openId = (jsonDecode(utf8.decode(p)) as Map)['terminal_id'] as String?;
      }
      if (m == 'terminal.input') inputNode = 'A';
      return _json(null);
    });
    final b = LoopbackNode('B', await generateKeyPair(), (m, p) => _json(null));
    final lnk = MultiNodeLoopbackLink({'A': a, 'B': b});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.connect();
    await client.call('terminal.open', {'term_id': 't1', 'terminal_id': 'A:@3', 'cols': 80, 'rows': 24});
    await client.call('terminal.input', {'term_id': 't1', 'data': 'eA=='});
    expect(openId, '@3');
    expect(inputNode, 'A');
    await client.close();
  });

  test('terminal.kill and terminal.rename split the composite id', () async {
    final seen = <String, String?>{};
    final a = LoopbackNode('A', await generateKeyPair(), (m, p) {
      seen[m] = (jsonDecode(utf8.decode(p)) as Map)['terminal_id'] as String?;
      return _json(null);
    });
    final lnk = MultiNodeLoopbackLink({'A': a});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.connect();
    await client.call('terminal.kill', {'terminal_id': 'A:@4'});
    await client.call('terminal.rename', {'terminal_id': 'A:@4', 'name': 'x'});
    expect(seen['terminal.kill'], '@4');
    expect(seen['terminal.rename'], '@4');
    await client.close();
  });

  test('terminal.create composites the result', () async {
    final a = LoopbackNode('A', await generateKeyPair(),
        (m, p) => _json({'id': '@5', 'cwd': '~', 'command': 'zsh'}));
    final lnk = MultiNodeLoopbackLink({'A': a});
    final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
    await client.connect();
    final r = await client.call('terminal.create', {'node_id': 'A'}) as Map;
    expect(r['id'], 'A:@5');
    expect(r['node_id'], 'A');
    expect(r['node_label'], 'A-box');
    await client.close();
  });
}
