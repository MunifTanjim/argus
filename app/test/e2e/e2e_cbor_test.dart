import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';
import 'package:argus/state/terminal_controller.dart';
import 'package:cbor/simple.dart';

import 'loopback.dart';

Uint8List _json(Object? v) => Uint8List.fromList(utf8.encode(jsonEncode(v)));

const _cborInfo = {
  'capabilities': {'cbor': true},
};

Future<void> _pollUntil(
  bool Function() done, {
  Duration timeout = const Duration(seconds: 3),
}) async {
  final deadline = DateTime.now().add(timeout);
  while (DateTime.now().isBefore(deadline)) {
    if (done()) return;
    await Future<void>.delayed(const Duration(milliseconds: 5));
  }
  fail('condition not met within $timeout');
}

Future<void> _opened(E2EClient c, String nodeId) => _pollUntil(
  () => c.connectedNodeIds.contains(nodeId) && c.openingNodeIds.isEmpty,
);

typedef _Rig = ({
  MultiNodeLoopbackLink lnk,
  LoopbackNode node,
  E2EClient client,
});

Future<_Rig> _connect({
  Map<String, Object?>? serverInfo = _cborInfo,
  Map<String, Object?>? nodeCaps = const {'cbor': true},
  bool withSendBytes = true,
}) async {
  final node = LoopbackNode('A', await generateKeyPair(), (m, p) => _json(null))
    ..capabilities = nodeCaps;
  final lnk = MultiNodeLoopbackLink({'A': node}, serverInfo: serverInfo);
  final client = E2EClient(
    lnk.incoming,
    lnk.send,
    await generateKeyPair(),
    sendBytes: withSendBytes ? lnk.sendBytes : null,
  );
  await client.connect();
  await _opened(client, 'A');
  return (lnk: lnk, node: node, client: client);
}

Future<void> _openTerm(E2EClient client) => client.call('terminal.open', {
  'term_id': 't1',
  'terminal_id': 'A:@1',
  'cols': 80,
  'rows': 24,
});

Future<Object?> _input(E2EClient client) => client.call('terminal.input', {
  'term_id': 't1',
  'data': Uint8List.fromList([3, 255]),
});

void main() {
  test(
    'terminal.input goes out as cbor when gateway and node report cbor',
    () async {
      final r = await _connect();
      await _openTerm(r.client);
      expect(await _input(r.client), isNull);

      expect(r.lnk.cborFrames.map((m) => m.method), ['terminal.input']);
      final req = r.node.requests.last;
      expect(req.method, 'terminal.input');
      expect(req.cbor, isTrue);
      expect(cbor.decode(req.params), {
        'term_id': 't1',
        'data': [3, 255],
      });
      final open = r.node.requests.firstWhere(
        (q) => q.method == 'terminal.open',
      );
      expect(open.cbor, isFalse);
      await r.client.close();
    },
  );

  test('stays json when the gateway server.info has no capabilities', () async {
    final r = await _connect(serverInfo: const {});
    await _openTerm(r.client);
    await _input(r.client);
    expect(r.lnk.cborFrames, isEmpty);
    final req = r.node.requests.last;
    expect(req.cbor, isFalse);
    expect(jsonDecode(utf8.decode(req.params)), {
      'term_id': 't1',
      'data': base64.encode([3, 255]),
    });
    await r.client.close();
  });

  test('stays json when the server.info capabilities are malformed', () async {
    final r = await _connect(serverInfo: const {'capabilities': 'cbor'});
    await _openTerm(r.client);
    await _input(r.client);
    expect(r.lnk.cborFrames, isEmpty);
    expect(r.node.requests.last.cbor, isFalse);
    await r.client.close();
  });

  test('stays json when the node identify result has no cbor', () async {
    final r = await _connect(nodeCaps: null);
    await _openTerm(r.client);
    await _input(r.client);
    expect(r.lnk.cborFrames, isEmpty);
    expect(r.node.requests.last.cbor, isFalse);
    await r.client.close();
  });

  test('stays json without sendBytes', () async {
    final r = await _connect(withSendBytes: false);
    await _openTerm(r.client);
    await _input(r.client);
    expect(r.lnk.cborFrames, isEmpty);
    expect(r.node.requests.last.cbor, isFalse);
    await r.client.close();
  });

  test(
    'reopened channel recomputes cbor and pre-identify frames are json',
    () async {
      final r = await _connect();
      final identify = r.node.requests.first;
      expect(identify.method, 'node.identify');
      expect(identify.cbor, isFalse);
      expect(jsonDecode(utf8.decode(identify.params)), {
        'capabilities': {'cbor': true},
      });
      await _openTerm(r.client);
      await _input(r.client);
      expect(r.node.requests.last.cbor, isTrue);

      Map<String, Object?> desc(bool online) => {
        'id': 'A',
        'label': 'A-box',
        'identity_pubkey': base64.encode(r.node.keyPair.publicKey),
        'online': online,
      };
      r.lnk.pushNotification('node.event', {
        'type': 'offline',
        'node': desc(false),
      });
      await _pollUntil(() => r.client.connectedNodeIds.isEmpty);
      r.node.capabilities = null;
      r.lnk.pushNotification('node.event', {
        'type': 'online',
        'node': desc(true),
      });
      await _opened(r.client, 'A');

      final before = r.lnk.cborFrames.length;
      await _openTerm(r.client);
      await _input(r.client);
      expect(r.lnk.cborFrames.length, before);
      expect(r.node.requests.last.cbor, isFalse);
      await r.client.close();
    },
  );

  test(
    'terminal output from a cbor frame reaches onData as raw bytes',
    () async {
      final r = await _connect();
      final got = <List<int>>[];
      final attach = TerminalAttach(
        client: r.client,
        terminalId: 'A:@1',
        cols: 80,
        rows: 24,
        onData: got.add,
      )..start();
      await _pollUntil(
        () => r.node.requests.any((q) => q.method == 'terminal.open'),
      );
      r.node.emitNotificationCbor('terminal.output', {
        'term_id': attach.termId,
        'data': Uint8List.fromList([0, 27, 255]),
      });
      await _pollUntil(() => got.isNotEmpty);
      expect(got.single, [0, 27, 255]);

      attach.send([4]);
      await _pollUntil(
        () => r.node.requests.any((q) => q.method == 'terminal.input'),
      );
      final input = r.node.requests.lastWhere(
        (q) => q.method == 'terminal.input',
      );
      expect(input.cbor, isTrue);
      expect((cbor.decode(input.params) as Map)['data'], [4]);
      attach.dispose();
      await r.client.close();
    },
  );

  test('cbor session.event is stamped with node origin', () async {
    final r = await _connect();
    final events = <({String method, Object? params})>[];
    final sub = r.client.aggregatedEvents.listen(events.add);
    r.node.emitNotificationCbor('session.event', {
      'type': 'updated',
      'session': {'id': 's1'},
    });
    await _pollUntil(() => events.isNotEmpty);
    final p = events.single.params as Map<String, dynamic>;
    expect((p['session'] as Map)['id'], 'A:s1');
    expect((p['session'] as Map)['node_id'], 'A');
    await sub.cancel();
    await r.client.close();
  });
}
