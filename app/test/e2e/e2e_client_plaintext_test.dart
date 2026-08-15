import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';
import 'package:argus/transport/connection.dart' show RpcLink;
import 'package:argus/transport/jsonrpc.dart';

Uint8List _json(Object? v) => Uint8List.fromList(utf8.encode(jsonEncode(v)));

/// A loopback gateway+node that advertises no identity key and uses plaintext
/// (unencrypted) relay frames — mirrors the Go TestPlaintextClientOpensChannelWithNoIdentityKey.
class _PlaintextLoopbackLink implements RpcLink {
  _PlaintextLoopbackLink(this._nodeId, this._handler);

  final String _nodeId;
  final List<int> Function(String method, Uint8List params) _handler;

  final _ctrl = StreamController<RpcMessage>();
  int _chanSeq = 0;

  final List<String> sent = [];

  void _push(String line) {
    if (_ctrl.isClosed) return;
    _ctrl.add(RpcMessage.fromJson(jsonDecode(line) as Map<String, dynamic>));
  }

  @override
  Stream<RpcMessage> get incoming => _ctrl.stream;

  @override
  void send(String frame) {
    for (final part in frame.split('\n')) {
      if (part.trim().isEmpty) continue;
      sent.add(part);
      final j = jsonDecode(part) as Map<String, dynamic>;
      final m = RpcMessage.fromJson(j);
      if (m.route == null) {
        _handleGatewayRpc(m, j['id']);
      } else {
        _handleNodeFrame(m, j);
      }
    }
  }

  void _handleGatewayRpc(RpcMessage m, Object? id) {
    switch (m.method) {
      case 'nodes.list':
        _push(
          jsonEncode({
            'jsonrpc': '2.0',
            'id': id,
            'result': {
              'nodes': [
                {
                  'id': _nodeId,
                  'label': '$_nodeId-box',
                  'identity_pubkey': '', // plaintext node: no identity key
                  'online': true,
                },
              ],
            },
          }),
        );
      case 'relay.open':
        final chanId = 'chan-${_chanSeq++}';
        _push(
          jsonEncode({
            'jsonrpc': '2.0',
            'id': id,
            'result': {'chan_id': chanId},
          }),
        );
      case 'ping':
        _push(jsonEncode({'jsonrpc': '2.0', 'id': id, 'result': null}));
    }
  }

  void _handleNodeFrame(RpcMessage m, Map<String, dynamic> j) {
    final chanId = (m.route as Map)['chan_id'] as String;
    // Plaintext: open params directly (identity cipher = no-op).
    final plain = Channel.plain(chanId);
    final f = RelayFrame.fromMessage(m);
    final params = plain.openParams(f);
    final result = _handler(m.method!, params);
    final inner = utf8.encode('{"result":${utf8.decode(result)}}');
    // Plain channel: body is just base64(inner) — no encryption.
    final body = base64.encode(inner);
    _push(
      jsonEncode({
        'jsonrpc': '2.0',
        'id': m.id == null ? null : int.parse(m.id!),
        'route': {'chan_id': chanId},
        'body': body,
      }),
    );
  }

  @override
  Future<void> close() async {
    if (!_ctrl.isClosed) await _ctrl.close();
  }
}

void main() {
  test(
    'plaintext client opens a channel with no identity key and no handshake',
    () async {
      final link = _PlaintextLoopbackLink(
        'node-plain',
        (method, params) => _json([
          {'id': 's1', 'title': 'plain-session'},
        ]),
      );

      final client = E2EClient(
        link.incoming,
        link.send,
        await generateKeyPair(),
        plaintext: true,
      );

      await client.connect();

      // Channel should be open to the plaintext node.
      expect(
        client.connectedNodeIds,
        contains('node-plain'),
        reason: 'plaintext node must be connected despite empty identityPubKey',
      );

      // A node call must succeed end-to-end.
      final list = (await client.call('sessions.list')) as List;
      expect(list, hasLength(1));
      expect((list.first as Map)['id'], 'node-plain:s1');

      // No e2e.handshake frame must have been sent.
      final handshakeFrames = link.sent.where((f) {
        try {
          final j = jsonDecode(f) as Map<String, dynamic>;
          return j['method'] == 'e2e.handshake';
        } catch (_) {
          return false;
        }
      }).toList();
      expect(
        handshakeFrames,
        isEmpty,
        reason: 'plaintext client must not send an e2e.handshake frame',
      );

      await client.close();
    },
  );
}
