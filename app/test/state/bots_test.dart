import 'package:argus/state/bots.dart';
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
        'bot.create' || 'bot.show' || 'bot.rename' => {
          'id': 'A:a1',
          'name': 'n',
        },
        'bot.add' => {
          'bot': {'id': 'A:a2'},
          'found_instructions': true,
        },
        'bot.list' => {
          'bots': [
            {'id': 'A:a1', 'primary': true},
          ],
        },
        'bot.thread.send' => {'thread_id': 'A:t1', 'session_id': 'A:s1'},
        'bot.thread.list' => {
          'threads': [
            {'id': 'A:t1', 'last_active_at': '2026-11-22T10:00:00Z'},
          ],
        },
        'bot.thread.timeline' => {
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
        'bot.memory.show' => {
          'memory': 'm',
          'topics': ['t'],
          'usage': 3,
          'budget': 10,
        },
        'bot.memory.log' => {
          'commits': [
            {'hash': 'h', 'subject': 's', 'when': '2026-11-22T10:00:00Z'},
          ],
        },
        'bot.memory.undo' => {'commit': 'h2'},
        'bot.memory.proposals' => {
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
        'bot.memory.decide' => {'commit': 'c', 'applied': 2, 'rejected': ['r']},
        'bot.memory.import' => {'staged': 4},
        _ => null,
      },
    );
    final api = BotsApi(() => client);

    final list = await api.list();
    _last(client, 'bot.list', {});
    expect(list.bots.single.id, 'A:a1');
    expect(list.bots.single.primary, true);
    await api.list(includeArchived: true);
    _last(client, 'bot.list', {'include_archived': true});

    expect((await api.create(name: 'n', nodeId: 'A')).id, 'A:a1');
    _last(client, 'bot.create', {'name': 'n', 'node_id': 'A'});
    final added = await api.add('/x', importInstructions: true, nodeId: 'A');
    _last(client, 'bot.add', {
      'dir': '/x',
      'import_instructions': true,
      'node_id': 'A',
    });
    expect(added.foundInstructions, true);
    expect(added.bot.id, 'A:a2');
    await api.show('A:a1');
    _last(client, 'bot.show', {'bot_id': 'A:a1'});
    await api.rename('A:a1', 'z');
    _last(client, 'bot.rename', {'bot_id': 'A:a1', 'name': 'z'});
    await api.forget('A:a1', purge: true);
    _last(client, 'bot.forget', {'bot_id': 'A:a1', 'purge': true});
    await api.setPrimary('A:a1');
    _last(client, 'bot.setPrimary', {'bot_id': 'A:a1'});
    await api.unsetPrimary('B');
    _last(client, 'bot.setPrimary', {'node_id': 'B'});
    await api.unsetPrimary('');
    _last(client, 'bot.setPrimary', <String, dynamic>{});

    final sent = await api.send('A:a1', 'hi', threadId: 'A:t1');
    _last(client, 'bot.thread.send', {
      'bot_id': 'A:a1',
      'thread_id': 'A:t1',
      'text': 'hi',
    });
    expect((sent.threadId, sent.sessionId), ('A:t1', 'A:s1'));
    await api.send('A:a1', 'hi', newThread: true);
    _last(client, 'bot.thread.send', {
      'bot_id': 'A:a1',
      'text': 'hi',
      'new': true,
    });
    final threads = await api.threads('A:a1');
    _last(client, 'bot.thread.list', {'bot_id': 'A:a1'});
    expect(threads.single.lastActiveAt, DateTime.utc(2026, 11, 22, 10));
    await api.markRead('A:t1', position: 5);
    _last(client, 'bot.thread.markRead', {'thread_id': 'A:t1', 'position': 5});

    final tl = await api.timeline('A:t1');
    _last(client, 'bot.thread.timeline', {'thread_id': 'A:t1'});
    expect(tl.sessions.single.live, true);
    expect(tl.entries.single.event?.kind, 'check-back');
    expect(tl.entries.single.event?.id, 7);

    final mem = await api.memory('A:a1');
    expect(mem.memory, 'm');
    expect(mem.topics, ['t']);
    expect((mem.usage, mem.budget), (3, 10));
    final log = await api.memoryLog('A:a1', limit: 5, profile: true);
    _last(client, 'bot.memory.log', {
      'bot_id': 'A:a1',
      'limit': 5,
      'profile': true,
    });
    expect(log.single.hash, 'h');
    expect(await api.memoryUndo('A:a1', 'h'), 'h2');
    _last(client, 'bot.memory.undo', {'bot_id': 'A:a1', 'commit': 'h'});
    final props = await api.proposals('A:a1');
    expect(props.single.isInstructions, true);
    expect(props.single.ops.single.action, 'add');
    final decided = await api.decide('A:mp1', true);
    _last(client, 'bot.memory.decide', {'proposal_id': 'A:mp1', 'accept': true});
    expect(decided.applied, 2);
    expect(decided.rejected, ['r']);
    expect(await api.importMemory('A:a1', 'c'), 4);
    _last(client, 'bot.memory.import', {'bot_id': 'A:a1', 'content': 'c'});
  });

  test('throws when not connected', () {
    expect(() => BotsApi(() => null).list(), throwsStateError);
  });

  test('store loads the merged list and keeps it on a failed reload', () async {
    var fail = false;
    final client = FakeGatewayClient((m, p) async {
      if (fail) throw const RpcError(-32000, 'boom');
      return {
        'bots': [
          {'id': 'A:a1', 'name': 'x', 'status': 'unreachable'},
          {'id': 'B:a1', 'name': 'y', 'primary': true},
        ],
      };
    });
    final c = ProviderContainer();
    addTearDown(c.dispose);
    final n = c.read(botsProvider.notifier);
    await n.load(client);
    var s = c.read(botsProvider);
    expect(s.loaded, true);
    expect(s.bots.length, 2);
    expect(s.bots.first.unreachable, true);
    expect(s.bots.last.primary, true);

    fail = true;
    await n.load(client);
    s = c.read(botsProvider);
    expect(s.bots.length, 2);
    expect(s.error, isNotNull);

    n.clear();
    expect(c.read(botsProvider).loaded, false);
  });
}
