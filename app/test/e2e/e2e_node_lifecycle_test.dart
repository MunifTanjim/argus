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

void main() {
  test(
    'a node that comes online after connect is adopted and its sessions appear',
    () async {
      final a = LoopbackNode(
        'A',
        await generateKeyPair(),
        (m, p) => _json([
          {'id': 's1'},
        ]),
      );
      final b = LoopbackNode(
        'B',
        await generateKeyPair(),
        (m, p) => _json([
          {'id': 's2'},
        ]),
      );
      final offline = {'B'};
      final lnk = MultiNodeLoopbackLink({'A': a, 'B': b}, offline: offline);
      final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());

      await client.connect();
      expect(await _sessionIds(client), {'A:s1'}); // B skipped: offline

      offline.remove('B'); // B reconnects: relay.open now succeeds
      lnk.pushNotification('node.event', {
        'type': 'online',
        'node': _descriptor('B', b.keyPair, online: true),
      });

      await _pollUntil(() => client.connectedNodeIds.contains('B'));
      expect(await _sessionIds(client), {'A:s1', 'B:s2'});
      await client.close();
    },
  );

  test(
    'a node that goes offline after connect is dropped and its sessions disappear',
    () async {
      final a = LoopbackNode(
        'A',
        await generateKeyPair(),
        (m, p) => _json([
          {'id': 's1'},
        ]),
      );
      final b = LoopbackNode(
        'B',
        await generateKeyPair(),
        (m, p) => _json([
          {'id': 's2'},
        ]),
      );
      final lnk = MultiNodeLoopbackLink({'A': a, 'B': b});
      final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());

      await client.connect();
      expect(await _sessionIds(client), {'A:s1', 'B:s2'});

      lnk.pushNotification('node.event', {
        'type': 'offline',
        'node': _descriptor('B', b.keyPair, online: false),
      });

      await _pollUntil(() => !client.connectedNodeIds.contains('B'));
      expect(await _sessionIds(client), {'A:s1'});
      expect(client.connectedNodeIds.contains('B'), isFalse);
      await client.close();
    },
  );
}
