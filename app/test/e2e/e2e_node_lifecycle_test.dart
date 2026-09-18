import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

import 'loopback.dart';

Uint8List _json(Object? v) => Uint8List.fromList(utf8.encode(jsonEncode(v)));

Map<String, dynamic> _descriptor(
  String id,
  KeyPair kp, {
  required bool online,
}) => {
  'id': id,
  'label': '$id-box',
  'identity_pubkey': base64.encode(kp.publicKey),
  'online': online,
};

Future<Set<Object?>> _sessionIds(E2EClient client) async {
  final list = (await client.call('sessions.list')) as List;
  return {for (final s in list) (s as Map)['id']};
}

Future<void> _pollUntil(
  bool Function() done, {
  Duration timeout = const Duration(seconds: 3),
}) async {
  final deadline = DateTime.now().add(timeout);
  while (DateTime.now().isBefore(deadline)) {
    if (done()) return;
    await Future<void>.delayed(const Duration(milliseconds: 20));
  }
}

typedef _TwoNode = ({
  MultiNodeLoopbackLink lnk,
  E2EClient client,
  LoopbackNode b,
  Set<String> offline,
});

/// Connects a client to a two-node link (A serves s1, B serves s2). When
/// [bOffline] is set, B starts offline so its relay.open is skipped at connect.
Future<_TwoNode> _connectTwoNode({bool bOffline = false}) async {
  final a = LoopbackNode('A', await generateKeyPair(), (m, p) => _json([
        {'id': 's1'},
      ]));
  final b = LoopbackNode('B', await generateKeyPair(), (m, p) => _json([
        {'id': 's2'},
      ]));
  final offline = bOffline ? {'B'} : <String>{};
  final lnk = MultiNodeLoopbackLink({'A': a, 'B': b}, offline: offline);
  final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
  await client.connect();
  return (lnk: lnk, client: client, b: b, offline: offline);
}

void _pushRoster(
  MultiNodeLoopbackLink lnk,
  String type,
  LoopbackNode node, {
  required bool online,
}) {
  lnk.pushNotification('node.event', {
    'type': type,
    'node': _descriptor(node.id, node.keyPair, online: online),
  });
}

void main() {
  test(
    'a node that comes online after connect is adopted and its sessions appear',
    () async {
      final f = await _connectTwoNode(bOffline: true);
      expect(await _sessionIds(f.client), {'A:s1'}); // B skipped: offline

      f.offline.remove('B'); // B reconnects: relay.open now succeeds
      _pushRoster(f.lnk, 'online', f.b, online: true);

      await _pollUntil(() => f.client.connectedNodeIds.contains('B'));
      expect(await _sessionIds(f.client), {'A:s1', 'B:s2'});
      await f.client.close();
    },
  );

  test(
    'a roster node.event is surfaced on the notifications stream',
    () async {
      final f = await _connectTwoNode(bOffline: true);
      final seen = <String>[];
      final sub = f.client.notifications.listen((m) => seen.add(m.method ?? ''));

      f.offline.remove('B');
      _pushRoster(f.lnk, 'online', f.b, online: true);

      await _pollUntil(() => seen.contains('node.event'));
      expect(seen, contains('node.event'));
      await sub.cancel();
      await f.client.close();
    },
  );

  test(
    'a node that goes offline after connect is dropped and its sessions disappear',
    () async {
      final f = await _connectTwoNode();
      expect(await _sessionIds(f.client), {'A:s1', 'B:s2'});

      _pushRoster(f.lnk, 'offline', f.b, online: false);

      await _pollUntil(() => !f.client.connectedNodeIds.contains('B'));
      expect(await _sessionIds(f.client), {'A:s1'});
      expect(f.client.connectedNodeIds.contains('B'), isFalse);
      await f.client.close();
    },
  );
}
