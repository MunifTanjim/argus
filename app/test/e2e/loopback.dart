import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:argus/e2e/e2e.dart';
import 'package:argus/transport/connection.dart' show RpcLink;
import 'package:argus/transport/jsonrpc.dart';

typedef NodeHandler = List<int> Function(String method, Uint8List params);

/// A test node: completes the responder handshake, opens sealed requests,
/// dispatches to [handler], and seals the raw-JSON result back. It also can push
/// sealed notifications. The response/notification sealing lives here (test-only);
/// the production client `Channel` intentionally has no node-side seal.
class LoopbackNode {
  LoopbackNode(this.id, this.keyPair, this.handler, {this.advertisedIdentity});

  final String id;
  final KeyPair keyPair;
  final NodeHandler handler;
  /// When set, this base64 string is reported as [identity_pubkey] in nodes.list
  /// instead of the node's real Noise keypair public key. The Noise handshake
  /// still uses [keyPair]. Allows testing the trust gate in isolation.
  final String? advertisedIdentity;
  Session? _session;
  String? _chanId;

  /// When true, sealed requests are silently dropped after the handshake (no
  /// reply) — used to exercise the client's call timeout. Handshakes still work.
  bool dropRequests = false;

  late void Function(String line) sendToClient;

  Future<void> onClientFrame(RpcMessage m) async {
    final chanId = (m.route as Map)['chan_id'] as String;
    if (m.method == methodE2EHandshake) {
      final msg1 = handshakeFromFrame(RelayFrame.fromMessage(m));
      final (sess, _, msg2) = await HandshakeState.respond(
          staticKey: keyPair, prologue: channelPrologue(id, chanId), msg1: msg1);
      _session = sess;
      _chanId = chanId;
      sendToClient('${utf8.decode(marshalHandshakeFrame(chanId, msg2))}\n');
      return;
    }
    if (dropRequests) return; // simulate an unanswered request
    // A real node drops undecryptable frames rather than crashing; mirror that.
    final Uint8List params;
    try {
      params = Channel(chanId, _session!).openParams(RelayFrame.fromMessage(m));
    } catch (_) {
      return; // decrypt failure: drop, no reply
    }
    (List<int>? ok, ({int code, String message})? err) handlerResult;
    try {
      handlerResult = (handler(m.method!, params), null);
    } catch (e) {
      final code = e is RpcError ? e.code : -32000;
      final msg = e is RpcError ? e.message : '$e';
      handlerResult = (null, (code: code, message: msg));
    }
    final inner = handlerResult.$2 != null
        ? utf8.encode(
            '{"error":{"code":${handlerResult.$2!.code},"message":${jsonEncode(handlerResult.$2!.message)}}}')
        : utf8.encode('{"result":${utf8.decode(handlerResult.$1!)}}');
    final body = base64.encode(_session!.seal(inner));
    sendToClient('${jsonEncode({
          'jsonrpc': '2.0',
          'id': m.id == null ? null : int.parse(m.id!),
          'route': {'chan_id': chanId},
          'body': body,
        })}\n');
  }

  void emitNotification(String method, List<int> params) {
    final body = base64.encode(_session!.seal(params));
    sendToClient('${jsonEncode({
          'jsonrpc': '2.0',
          'method': method,
          'route': {'chan_id': _chanId},
          'body': body,
        })}\n');
  }
}

/// An in-memory gateway RpcLink: answers gateway RPCs (relay.open/ping) and relays
/// sealed/handshake frames to a single [LoopbackNode].
class LoopbackLink implements RpcLink {
  LoopbackLink(this._node) {
    _node.sendToClient = _push;
  }

  final LoopbackNode _node;
  final _ctrl = StreamController<RpcMessage>();
  int _chanSeq = 0;

  /// When false, gateway RPCs (relay.open/ping) are silently ignored — used to
  /// exercise the client's gateway-call timeout.
  bool answerGatewayRpc = true;

  void _push(String line) {
    for (final part in line.split('\n')) {
      if (part.trim().isEmpty) continue;
      if (_ctrl.isClosed) return;
      _ctrl.add(RpcMessage.fromJson(jsonDecode(part) as Map<String, dynamic>));
    }
  }

  @override
  Stream<RpcMessage> get incoming => _ctrl.stream;

  @override
  void send(String frame) {
    for (final part in frame.split('\n')) {
      if (part.trim().isEmpty) continue;
      final j = jsonDecode(part) as Map<String, dynamic>;
      final m = RpcMessage.fromJson(j);
      if (m.route == null) {
        _gatewayRpc(m, j['id']);
      } else {
        _node.onClientFrame(m);
      }
    }
  }

  void _gatewayRpc(RpcMessage m, Object? id) {
    if (!answerGatewayRpc) return;
    switch (m.method) {
      case 'relay.open':
        _push(jsonEncode({'jsonrpc': '2.0', 'id': id, 'result': {'chan_id': 'chan-${_chanSeq++}'}}));
      case 'ping':
        _push(jsonEncode({'jsonrpc': '2.0', 'id': id, 'result': null}));
    }
  }

  @override
  Future<void> close() async {
    if (!_ctrl.isClosed) await _ctrl.close();
  }
}

/// A gateway relaying to several nodes, keyed by node id. Answers nodes.list with
/// each node's identity_pubkey and relay.open(node_id) with a chan bound to that node.
class MultiNodeLoopbackLink implements RpcLink {
  MultiNodeLoopbackLink(this._nodes, {Uint8List? trustChain})
      : _trustChain = trustChain {
    for (final n in _nodes.values) {
      n.sendToClient = _push;
    }
  }

  final Map<String, LoopbackNode> _nodes;

  /// The trust chain the gateway serves on `trustlog.pull`. Mutable so a test can
  /// advance it (simulating a mid-session `lock revoke`) between re-syncs.
  Uint8List? _trustChain;
  set trustChain(Uint8List? chain) => _trustChain = chain;
  final _ctrl = StreamController<RpcMessage>();
  final _chanToNode = <String, LoopbackNode>{};
  int _chanSeq = 0;

  void _push(String line) {
    for (final part in line.split('\n')) {
      if (part.trim().isEmpty || _ctrl.isClosed) continue;
      _ctrl.add(RpcMessage.fromJson(jsonDecode(part) as Map<String, dynamic>));
    }
  }

  @override
  Stream<RpcMessage> get incoming => _ctrl.stream;

  @override
  void send(String frame) {
    for (final part in frame.split('\n')) {
      if (part.trim().isEmpty) continue;
      final j = jsonDecode(part) as Map<String, dynamic>;
      final m = RpcMessage.fromJson(j);
      if (m.route == null) {
        _gatewayRpc(m, j);
      } else {
        final chanId = (m.route as Map)['chan_id'] as String;
        _chanToNode[chanId]?.onClientFrame(m);
      }
    }
  }

  void _gatewayRpc(RpcMessage m, Map<String, dynamic> j) {
    final id = j['id'];
    switch (m.method) {
      case 'nodes.list':
        final nodes = [
          for (final e in _nodes.entries)
            {
              'id': e.key,
              'label': '${e.key}-box',
              'identity_pubkey': e.value.advertisedIdentity ?? base64.encode(e.value.keyPair.publicKey),
              'online': true,
            }
        ];
        _push(jsonEncode({'jsonrpc': '2.0', 'id': id, 'result': {'nodes': nodes}}));
      case 'relay.open':
        final nodeId = (j['params'] as Map)['node_id'] as String;
        final chanId = 'chan-${_chanSeq++}';
        _chanToNode[chanId] = _nodes[nodeId]!;
        _push(jsonEncode({'jsonrpc': '2.0', 'id': id, 'result': {'chan_id': chanId}}));
      case 'ping':
        _push(jsonEncode({'jsonrpc': '2.0', 'id': id, 'result': null}));
      case 'trustlog.pull':
        final tc = _trustChain;
        _push(jsonEncode({
          'jsonrpc': '2.0',
          'id': id,
          'result': {'chain': tc == null ? '' : base64.encode(tc)},
        }));
    }
  }

  @override
  Future<void> close() async {
    if (!_ctrl.isClosed) await _ctrl.close();
  }
}
