import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:argus/e2e/e2e.dart';
import 'package:argus/transport/connection.dart' show RpcLink;
import 'package:argus/transport/frame_reader.dart' show frameCbor;
import 'package:argus/transport/jsonrpc.dart';
import 'package:cbor/simple.dart';

typedef NodeHandler = List<int> Function(String method, Uint8List params);

/// The response/notification sealing lives here (test-only); the production
/// client `Channel` intentionally has no node-side seal.
class LoopbackNode {
  LoopbackNode(
    this.id,
    this.keyPair,
    this.handler, {
    this.advertisedIdentity,
    this.plaintext = false,
  });

  final String id;
  final KeyPair keyPair;
  final NodeHandler handler;

  /// When set, this base64 string is reported as [identity_pubkey] in nodes.list
  /// instead of the node's real Noise keypair public key. The Noise handshake
  /// still uses [keyPair]. Allows testing the trust gate in isolation.
  final String? advertisedIdentity;

  /// Plaintext channels: no handshake, unsealed bodies.
  final bool plaintext;

  // Per channel, so a relay and a direct channel to this node can coexist.
  final _sessions = <String, Session>{};
  final _sinks = <String, void Function(String line)>{};
  final _byteSinks = <String, void Function(Uint8List framed)>{};
  String? _chanId;

  /// The channel the latest handshake (or plaintext frame) opened.
  String? get lastChanId => _chanId;

  /// When true, sealed requests are silently dropped after the handshake (no
  /// reply) — used to exercise the client's call timeout. Handshakes still work.
  bool dropRequests = false;

  /// When set, the node waits for this gate before invoking the user [handler].
  /// Lets a test hold one node's response open while another answers.
  Completer<void>? gate;

  /// When set, the node waits for this gate before answering the handshake, so
  /// a test can hold its channel open while others connect.
  Completer<void>? handshakeGate;

  /// The trust-log tip this node reports over its authenticated channel in
  /// answer to node.identify. Settable so a test can make a node advertise an
  /// off-chain tip to drive the client's tip-consistency check.
  Uint8List? tip;

  /// Reported as `capabilities` in the node.identify result when set.
  Map<String, Object?>? capabilities;

  /// Every opened request, in arrival order.
  final requests =
      <({String method, bool cbor, Uint8List params, String chanId})>[];

  late void Function(String line) sendToClient;
  late void Function(Uint8List framed) sendBytesToClient;

  Uint8List _seal(String chanId, List<int> inner) =>
      plaintext ? Uint8List.fromList(inner) : _sessions[chanId]!.seal(inner);

  /// Handles a frame from the client. [reply] and [replyBytes] carry this
  /// channel's frames back on the link the frame came from; the gateway's
  /// sinks by default.
  Future<void> onClientFrame(
    RpcMessage m, {
    void Function(String line)? reply,
    void Function(Uint8List framed)? replyBytes,
  }) async {
    final chanId = (m.route as Map)['chan_id'] as String;
    if (m.method == methodE2EHandshake) {
      await handshakeGate?.future;
      final msg1 = handshakeFromFrame(RelayFrame.fromMessage(m));
      final (sess, _, msg2) = await HandshakeState.respond(
        staticKey: keyPair,
        prologue: channelPrologue(id, chanId),
        msg1: msg1,
      );
      _sessions[chanId] = sess;
      _sinks[chanId] = reply ?? (l) => sendToClient(l);
      _byteSinks[chanId] = replyBytes ?? (f) => sendBytesToClient(f);
      _chanId = chanId;
      _sinks[chanId]!('${utf8.decode(marshalHandshakeFrame(chanId, msg2))}\n');
      return;
    }
    if (plaintext && !_sinks.containsKey(chanId)) {
      _sinks[chanId] = reply ?? (l) => sendToClient(l);
      _byteSinks[chanId] = replyBytes ?? (f) => sendBytesToClient(f);
      _chanId = chanId;
    }
    if (dropRequests) return;
    // A real node drops undecryptable frames rather than crashing; mirror that.
    final Uint8List params;
    try {
      final ch = plaintext
          ? Channel.plain(chanId)
          : Channel.noise(chanId, _sessions[chanId]!);
      params = ch.openParams(RelayFrame.fromMessage(m));
    } catch (_) {
      return;
    }
    requests.add((
      method: m.method!,
      cbor: m.cbor,
      params: params,
      chanId: chanId,
    ));
    final sink = _sinks[chanId]!;
    final byteSink = _byteSinks[chanId]!;
    (List<int>? ok, ({int code, String message})? err) handlerResult;
    if (m.method == 'node.identify') {
      // Answer node.identify with this node's id and (optional) trust-log tip,
      // over the authenticated channel — the source the client's tip check trusts.
      final t = tip;
      handlerResult = (
        utf8.encode(
          jsonEncode({
            'id': id,
            if (t != null) 'tip': base64.encode(t),
            if (capabilities != null) 'capabilities': capabilities,
          }),
        ),
        null,
      );
    } else {
      if (gate != null) await gate!.future;
      try {
        handlerResult = (handler(m.method!, params), null);
      } catch (e) {
        final code = e is RpcError ? e.code : -32000;
        final msg = e is RpcError ? e.message : '$e';
        handlerResult = (null, (code: code, message: msg));
      }
    }
    if (m.cbor) {
      final err = handlerResult.$2;
      final inner = cbor.encode(
        err != null
            ? {
                'error': {'code': err.code, 'message': err.message},
              }
            : {'result': jsonDecode(utf8.decode(handlerResult.$1!))},
      );
      byteSink(
        frameCbor(
          Uint8List.fromList(
            cbor.encode({
              'jsonrpc': '2.0',
              'id': int.parse(m.id!),
              'route': {'chan_id': chanId},
              'body': _seal(chanId, inner),
            }),
          ),
        ),
      );
      return;
    }
    final inner = handlerResult.$2 != null
        ? utf8.encode(
            '{"error":{"code":${handlerResult.$2!.code},"message":${jsonEncode(handlerResult.$2!.message)}}}',
          )
        : utf8.encode('{"result":${utf8.decode(handlerResult.$1!)}}');
    final body = base64.encode(_seal(chanId, inner));
    sink(
      '${jsonEncode({
        'jsonrpc': '2.0',
        'id': m.id == null ? null : int.parse(m.id!),
        'route': {'chan_id': chanId},
        'body': body,
      })}\n',
    );
  }

  /// Sends a notification on [chanId], the latest channel by default.
  void emitNotification(String method, List<int> params, {String? chanId}) {
    final ch = chanId ?? _chanId!;
    final body = base64.encode(_seal(ch, params));
    (_sinks[ch] ?? sendToClient)(
      '${jsonEncode({
        'jsonrpc': '2.0',
        'method': method,
        'route': {'chan_id': ch},
        'body': body,
      })}\n',
    );
  }

  void emitNotificationCbor(String method, Object? params) {
    final ch = _chanId!;
    (_byteSinks[ch] ?? sendBytesToClient)(
      frameCbor(
        Uint8List.fromList(
          cbor.encode({
            'jsonrpc': '2.0',
            'method': method,
            'route': {'chan_id': ch},
            'body': _seal(ch, cbor.encode(params)),
          }),
        ),
      ),
    );
  }
}

RpcMessage _fromCborFrame(Uint8List framed) =>
    RpcMessage.fromCbor(cbor.decode(framed.sublist(5)) as Map);

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
  void sendBytes(Uint8List frame) {}
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
    Set<String>? lockDisabled,
    this.serverInfo,
    this.p2pEndpoints = const {},
  }) : _offline = offline ?? const {},
       _failRelayOpen = failRelayOpen ?? const {},
       _lockDisabled = lockDisabled ?? const {} {
    for (final n in _nodes.values) {
      n.sendToClient = _push;
      n.sendBytesToClient = (f) {
        if (!_ctrl.isClosed) _ctrl.add(_fromCborFrame(f));
      };
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

  /// Node ids reported `lock_disabled: true` in nodes.list.
  final Set<String> _lockDisabled;

  /// The server.info result; server.info goes unanswered when null.
  final Map<String, Object?>? serverInfo;

  /// Direct endpoints by node id. A node listed here is reported with them and
  /// with the p2p capability in nodes.list.
  final Map<String, List<String>> p2pEndpoints;

  /// Every chan id passed to relay.close, in order.
  final relayCloseCalls = <String>[];

  /// Every CBOR frame the client sent.
  final cborFrames = <RpcMessage>[];

  /// Every node id passed to relay.open, in order. Lets a test assert that an
  /// offline node is skipped before any open is attempted.
  final relayOpenCalls = <String>[];

  /// Runs after relay.open is answered, while the client waits for the handshake.
  void Function(String nodeId)? onRelayOpen;

  /// Runs before trustlog.sync is answered, while the client waits for the chain.
  void Function()? beforeTrustSync;

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

  /// Number of trustlog.sync calls answered. Lets a test assert that a prompt
  /// pull was triggered (e.g. by a trust-changed node.event nudge).
  int trustSyncCount = 0;

  void _push(String line) {
    for (final part in line.split('\n')) {
      if (part.trim().isEmpty || _ctrl.isClosed) continue;
      _ctrl.add(RpcMessage.fromJson(jsonDecode(part) as Map<String, dynamic>));
    }
  }

  @override
  Stream<RpcMessage> get incoming => _ctrl.stream;

  @override
  void sendBytes(Uint8List frame) {
    final m = _fromCborFrame(frame);
    cborFrames.add(m);
    final chanId = (m.route as Map)['chan_id'] as String;
    _chanToNode[chanId]?.onClientFrame(m);
  }

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
              if (_lockDisabled.contains(e.key)) 'lock_disabled': true,
              if (p2pEndpoints[e.key] != null) ...{
                'p2p_endpoints': p2pEndpoints[e.key],
                'capabilities': {'p2p': true},
              },
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
        onRelayOpen?.call(nodeId);
      case 'ping':
        _push(jsonEncode({'jsonrpc': '2.0', 'id': id, 'result': null}));
      case 'relay.close':
        relayCloseCalls.add((j['params'] as Map)['chan_id'] as String);
        _push(jsonEncode({'jsonrpc': '2.0', 'id': id, 'result': null}));
      case 'server.info':
        final info = serverInfo;
        if (info == null) return;
        _push(jsonEncode({'jsonrpc': '2.0', 'id': id, 'result': info}));
      case 'trustlog.sync':
        trustSyncCount++;
        beforeTrustSync?.call();
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
        final all = _entryStore.all();
        final List<Uint8List> entries = [
          for (final raw in all)
            if (!knownHex.contains(hexEncode(hashEntry(unmarshalEntry(raw)))))
              raw,
        ];
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
  /// tests inject offline/removed/trust-changed events without going through a
  /// node channel.
  void pushNotification(String method, Object? params) {
    _push(jsonEncode({'jsonrpc': '2.0', 'method': method, 'params': params}));
  }

  @override
  Future<void> close() async {
    if (!_ctrl.isClosed) await _ctrl.close();
  }
}

/// A node's direct listener as seen by one client connection: answers the
/// direct requests (ping, plaintext admission) and carries the node's channel
/// frames on this link.
class LoopbackDirectLink implements RpcLink {
  LoopbackDirectLink(
    this.node, {
    this.tokenHash,
    this.badMac = false,
    this.identifyOpener = false,
  });

  final LoopbackNode node;

  /// The token hash plaintext admission accepts; null on an E2EE node.
  final String? tokenHash;

  /// Answers token.challenge with a MAC that does not prove the hash.
  final bool badMac;

  /// Sends a node.identify request before answering token.present, as a node
  /// does to a master-token opener, and waits for the reply.
  final bool identifyOpener;

  /// Every line the client sent on this link.
  final sent = <String>[];

  /// The client's replies to this link's node.identify requests.
  final identifyReplies = <RpcMessage>[];

  final _ctrl = StreamController<RpcMessage>.broadcast();
  Completer<void>? _identifyReply;
  bool closed = false;

  void _push(String line) {
    for (final part in line.split('\n')) {
      if (part.trim().isEmpty || _ctrl.isClosed) continue;
      _ctrl.add(RpcMessage.fromJson(jsonDecode(part) as Map<String, dynamic>));
    }
  }

  void _reply(Object? id, {Object? result, Map<String, Object?>? error}) =>
      _push(
        jsonEncode({
          'jsonrpc': '2.0',
          'id': id,
          if (error != null) 'error': error else 'result': result,
        }),
      );

  @override
  Stream<RpcMessage> get incoming => _ctrl.stream;

  @override
  void sendBytes(Uint8List frame) {
    if (closed) return;
    final m = _fromCborFrame(frame);
    node.onClientFrame(m, reply: _push, replyBytes: _pushBytes);
  }

  void _pushBytes(Uint8List f) {
    if (!_ctrl.isClosed) _ctrl.add(_fromCborFrame(f));
  }

  @override
  void send(String frame) {
    if (closed) return;
    for (final part in frame.split('\n')) {
      if (part.trim().isEmpty) continue;
      sent.add(part);
      final j = jsonDecode(part) as Map<String, dynamic>;
      final m = RpcMessage.fromJson(j);
      if (m.route != null) {
        node.onClientFrame(m, reply: _push, replyBytes: _pushBytes);
      } else if (m.method != null) {
        unawaited(_direct(m.method!, j['id'], j['params']));
      } else if (m.id == 'identify') {
        identifyReplies.add(m);
        _identifyReply?.complete();
      }
    }
  }

  Future<void> _direct(String method, Object? id, Object? params) async {
    final p = params is Map ? params : const {};
    switch (method) {
      case 'ping':
        _reply(id);
      case 'token.challenge':
        final hash = tokenHash;
        if (hash == null || p2pTag(hash, p['nonce'] as String) != p['tag']) {
          _reply(id, error: {'code': -32600, 'message': 'not admitted'});
          await close();
          return;
        }
        final mac = badMac
            ? p2pMac(
                hash,
                'someone-else',
                p['chan_id'] as String,
                p['nonce'] as String,
              )
            : p2pMac(
                hash,
                node.id,
                p['chan_id'] as String,
                p['nonce'] as String,
              );
        _reply(id, result: {'mac': mac});
      case 'token.present':
        if (identifyOpener) {
          _identifyReply = Completer<void>();
          _push(
            jsonEncode({
              'jsonrpc': '2.0',
              'id': 'identify',
              'method': 'node.identify',
            }),
          );
          await _identifyReply!.future;
        }
        if (hashToken(p['token'] as String) != tokenHash) {
          _reply(id, error: {'code': -32600, 'message': 'not admitted'});
          await close();
          return;
        }
        _reply(id);
      default:
        _reply(id, error: {'code': -32601, 'message': 'method not found'});
    }
  }

  /// Ends the link from the node's side.
  Future<void> drop() => close();

  @override
  Future<void> close() async {
    closed = true;
    if (!_ctrl.isClosed) await _ctrl.close();
  }
}
