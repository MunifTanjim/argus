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
        staticKey: keyPair,
        prologue: channelPrologue(id, chanId),
        msg1: msg1,
      );
      _session = sess;
      _chanId = chanId;
      sendToClient('${utf8.decode(marshalHandshakeFrame(chanId, msg2))}\n');
      return;
    }
    if (dropRequests) return; // simulate an unanswered request
    // A real node drops undecryptable frames rather than crashing; mirror that.
    final Uint8List params;
    try {
      params = Channel.noise(
        chanId,
        _session!,
      ).openParams(RelayFrame.fromMessage(m));
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
            '{"error":{"code":${handlerResult.$2!.code},"message":${jsonEncode(handlerResult.$2!.message)}}}',
          )
        : utf8.encode('{"result":${utf8.decode(handlerResult.$1!)}}');
    final body = base64.encode(_session!.seal(inner));
    sendToClient(
      '${jsonEncode({
        'jsonrpc': '2.0',
        'id': m.id == null ? null : int.parse(m.id!),
        'route': {'chan_id': chanId},
        'body': body,
      })}\n',
    );
  }

  void emitNotification(String method, List<int> params) {
    final body = base64.encode(_session!.seal(params));
    sendToClient(
      '${jsonEncode({
        'jsonrpc': '2.0',
        'method': method,
        'route': {'chan_id': _chanId},
        'body': body,
      })}\n',
    );
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
        _push(
          jsonEncode({
            'jsonrpc': '2.0',
            'id': id,
            'result': {'chan_id': 'chan-${_chanSeq++}'},
          }),
        );
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
  MultiNodeLoopbackLink(
    this._nodes, {
    Uint8List? trustChain,
    Set<String>? offline,
    Set<String>? failRelayOpen,
  }) : _offline = offline ?? const {},
       _failRelayOpen = failRelayOpen ?? const {} {
    for (final n in _nodes.values) {
      n.sendToClient = _push;
    }
    if (trustChain != null) {
      this.trustChain = trustChain;
    }
  }

  final Map<String, LoopbackNode> _nodes;

  /// Node ids reported `online: false` in nodes.list. A relay.open for one also
  /// fails, mirroring a within-grace node with no live uplink.
  final Set<String> _offline;

  /// Node ids reported online but whose relay.open returns an error, mirroring a
  /// node that dropped between the roster snapshot and the open.
  final Set<String> _failRelayOpen;

  /// Every node id passed to relay.open, in order. Lets a test assert that an
  /// offline node is skipped before any open is attempted.
  final relayOpenCalls = <String>[];

  /// The gateway's entry store, populated when [trustChain] is set. Used to
  /// answer trustlog.sync with the correct delta for the caller's heads.
  final EntryStore _entryStore = EntryStore();

  /// Accumulates entries from [chain] into the gateway's entry store, serving
  /// the differential delta on `trustlog.sync`. Mutable so a test can advance
  /// the chain (simulating a mid-session `lock revoke`) between re-syncs.
  set trustChain(Uint8List chain) {
    _entryStore.putAll(chainEntries(chain));
  }

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
              'identity_pubkey':
                  e.value.advertisedIdentity ??
                  base64.encode(e.value.keyPair.publicKey),
              'online': !_offline.contains(e.key),
            },
        ];
        _push(
          jsonEncode({
            'jsonrpc': '2.0',
            'id': id,
            'result': {'nodes': nodes},
          }),
        );
      case 'relay.open':
        final nodeId = (j['params'] as Map)['node_id'] as String;
        relayOpenCalls.add(nodeId);
        if (_offline.contains(nodeId) || _failRelayOpen.contains(nodeId)) {
          _push(
            jsonEncode({
              'jsonrpc': '2.0',
              'id': id,
              'error': {'code': -32004, 'message': 'unknown node: $nodeId'},
            }),
          );
          return;
        }
        final chanId = 'chan-${_chanSeq++}';
        _chanToNode[chanId] = _nodes[nodeId]!;
        _push(
          jsonEncode({
            'jsonrpc': '2.0',
            'id': id,
            'result': {'chan_id': chanId},
          }),
        );
      case 'ping':
        _push(jsonEncode({'jsonrpc': '2.0', 'id': id, 'result': null}));
      case 'trustlog.sync':
        // 'known' lists every entry hash the caller holds; the gateway computes
        // the delta by set subtraction (mirrors Go gateway.Delta).
        // A truncated offer under-reports by construction and can look disjoint
        // when it is not — suppress disjoint for truncated offers, matching the
        // real gateway.
        final params = j['params'];
        final rawKnown = params is Map ? params['known'] : null;
        final offerTruncated = params is Map
            ? params['truncated'] == true
            : false;
        // Decode the caller's offered hashes (base64 binary → hex for comparison).
        final knownHex = <String>{};
        if (rawKnown is List) {
          for (final h in rawKnown) {
            if (h is String && h.isNotEmpty) {
              try {
                knownHex.add(hexEncode(Uint8List.fromList(base64.decode(h))));
              } catch (_) {}
            }
          }
        }
        // Set subtraction: return every entry whose hash is not in knownHex.
        final all = _entryStore.all();
        final List<Uint8List> entries = [
          for (final raw in all)
            if (!knownHex.contains(hexEncode(hashEntry(unmarshalEntry(raw)))))
              raw,
        ];
        // disjoint: non-empty, non-truncated known shares no entry with this store.
        bool disjoint = false;
        if (knownHex.isNotEmpty && !offerTruncated) {
          final storeHex = {
            for (final raw in all) hexEncode(hashEntry(unmarshalEntry(raw))),
          };
          disjoint = knownHex.every((h) => !storeHex.contains(h));
        }
        _push(
          jsonEncode({
            'jsonrpc': '2.0',
            'id': id,
            'result': {
              'entries': [for (final e in entries) base64.encode(e)],
              'want': <String>[],
              if (disjoint) 'disjoint': true,
            },
          }),
        );
    }
  }

  /// Pushes a gateway-level (non-routed) notification to the client, simulating
  /// a server-sent [node.event] or similar gateway notification. This is how
  /// tests inject offline/removed/beacon events without going through a node
  /// channel.
  void pushNotification(String method, Object? params) {
    _push(jsonEncode({'jsonrpc': '2.0', 'method': method, 'params': params}));
  }

  @override
  Future<void> close() async {
    if (!_ctrl.isClosed) await _ctrl.close();
  }
}
