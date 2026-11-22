import 'package:argus/state/pagents.dart';
import 'package:argus/transport/jsonrpc.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../support/fake_gateway_client.dart';

void _last(FakeGatewayClient c, String method, Object? params) {
  expect(c.calls.last.$1, method);
  expect(c.calls.last.$2, params);
}

void main() {
  test('calls each method with the node contract params', () async {
    final client = FakeGatewayClient(
      (m, p) async => switch (m) {
        'pagent.create' || 'pagent.show' || 'pagent.rename' => {
          'id': 'A:a1',
          'name': 'n',
        },
        'pagent.add' => {
          'agent': {'id': 'A:a2'},
          'found_instructions': true,
        },
        'pagent.list' => {
          'agents': [
            {'id': 'A:a1'},
          ],
          'primary': 'A:a1',
        },
        'pagent.thread.send' => {'thread_id': 'A:t1', 'session_id': 'A:s1'},
        'pagent.thread.list' => {
          'threads': [
            {'id': 'A:t1', 'last_active_at': '2026-11-22T10:00:00Z'},
          ],
        },
        'pagent.thread.timeline' => {
          'thread_id': 'A:t1',
          'entries': [
            {
              'id': 'e1',
              'kind': 'argus',
              'label': 'check-back',
              'event': {'id': 7, 'kind': 'check-back', 'payload': {'x': 1}},
            },
          ],
          'sessions': [
            {'session_id': 'A:s1', 'start_index': 0, 'live': true},
          ],
        },
        'pagent.memory.show' => {
          'memory': 'm',
          'topics': ['t'],
          'usage': 3,
          'budget': 10,
        },
        'pagent.memory.log' => {
          'commits': [
            {'hash': 'h', 'subject': 's', 'when': '2026-11-22T10:00:00Z'},
          ],
        },
        'pagent.memory.undo' => {'commit': 'h2'},
        'pagent.memory.proposals' => {
          'proposals': [
            {
              'id': 'A:mp1',
              'kind': 'instructions',
              'ops': [
                {'action': 'add', 'text': 'x'},
              ],
              'created_at': '2026-11-22T10:00:00Z',
            },
          ],
        },
        'pagent.memory.decide' => {'commit': 'c', 'applied': 2, 'rejected': ['r']},
        'pagent.memory.import' => {'staged': 4},
        _ => null,
      },
    );
    final api = PAgentsApi(() => client);

    final list = await api.list();
    _last(client, 'pagent.list', {});
    expect(list.primary, 'A:a1');
    expect(list.agents.single.id, 'A:a1');
    await api.list(includeArchived: true);
    _last(client, 'pagent.list', {'include_archived': true});

    expect((await api.create(name: 'n', nodeId: 'A')).id, 'A:a1');
    _last(client, 'pagent.create', {'name': 'n', 'node_id': 'A'});
    final added = await api.add('/x', importInstructions: true, nodeId: 'A');
    _last(client, 'pagent.add', {
      'dir': '/x',
      'import_instructions': true,
      'node_id': 'A',
    });
    expect(added.foundInstructions, true);
    expect(added.agent.id, 'A:a2');
    await api.show('A:a1');
    _last(client, 'pagent.show', {'agent_id': 'A:a1'});
    await api.rename('A:a1', 'z');
    _last(client, 'pagent.rename', {'agent_id': 'A:a1', 'name': 'z'});
    await api.forget('A:a1', purge: true);
    _last(client, 'pagent.forget', {'agent_id': 'A:a1', 'purge': true});
    await api.setPrimary('A:a1');
    _last(client, 'pagent.setPrimary', {'agent_id': 'A:a1'});
    await api.setPrimary(null);
    _last(client, 'pagent.setPrimary', <String, dynamic>{});

    final sent = await api.send('A:a1', 'hi', threadId: 'A:t1');
    _last(client, 'pagent.thread.send', {
      'agent_id': 'A:a1',
      'thread_id': 'A:t1',
      'text': 'hi',
    });
    expect((sent.threadId, sent.sessionId), ('A:t1', 'A:s1'));
    await api.send('A:a1', 'hi', newThread: true);
    _last(client, 'pagent.thread.send', {
      'agent_id': 'A:a1',
      'text': 'hi',
      'new': true,
    });
    final threads = await api.threads('A:a1');
    _last(client, 'pagent.thread.list', {'agent_id': 'A:a1'});
    expect(threads.single.lastActiveAt, DateTime.utc(2026, 11, 22, 10));
    await api.markRead('A:t1', position: 5);
    _last(client, 'pagent.thread.markRead', {'thread_id': 'A:t1', 'position': 5});

    final tl = await api.timeline('A:t1');
    _last(client, 'pagent.thread.timeline', {'thread_id': 'A:t1'});
    expect(tl.sessions.single.live, true);
    expect(tl.entries.single.event?.kind, 'check-back');
    expect(tl.entries.single.event?.id, 7);

    final mem = await api.memory('A:a1');
    expect(mem.memory, 'm');
    expect(mem.topics, ['t']);
    expect((mem.usage, mem.budget), (3, 10));
    final log = await api.memoryLog('A:a1', limit: 5, profile: true);
    _last(client, 'pagent.memory.log', {
      'agent_id': 'A:a1',
      'limit': 5,
      'profile': true,
    });
    expect(log.single.hash, 'h');
    expect(await api.memoryUndo('A:a1', 'h'), 'h2');
    _last(client, 'pagent.memory.undo', {'agent_id': 'A:a1', 'commit': 'h'});
    final props = await api.proposals('A:a1');
    expect(props.single.isInstructions, true);
    expect(props.single.ops.single.action, 'add');
    final decided = await api.decide('A:mp1', true);
    _last(client, 'pagent.memory.decide', {'proposal_id': 'A:mp1', 'accept': true});
    expect(decided.applied, 2);
    expect(decided.rejected, ['r']);
    expect(await api.importMemory('A:a1', 'c'), 4);
    _last(client, 'pagent.memory.import', {'agent_id': 'A:a1', 'content': 'c'});
  });

  test('throws when not connected', () {
    expect(() => PAgentsApi(() => null).list(), throwsStateError);
  });

  test('store loads the merged list and keeps it on a failed reload', () async {
    var fail = false;
    final client = FakeGatewayClient((m, p) async {
      if (fail) throw const RpcError(-32000, 'boom');
      return {
        'agents': [
          {'id': 'A:a1', 'name': 'x', 'status': 'unreachable'},
          {'id': 'B:a1', 'name': 'y'},
        ],
        'primary': 'B:a1',
      };
    });
    final c = ProviderContainer();
    addTearDown(c.dispose);
    final n = c.read(pagentsProvider.notifier);
    await n.load(client);
    var s = c.read(pagentsProvider);
    expect(s.loaded, true);
    expect(s.agents.length, 2);
    expect(s.agents.first.unreachable, true);
    expect(s.primaryAgent?.id, 'B:a1');

    fail = true;
    await n.load(client);
    s = c.read(pagentsProvider);
    expect(s.agents.length, 2);
    expect(s.error, isNotNull);

    n.clear();
    expect(c.read(pagentsProvider).loaded, false);
  });
}
