import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import '../transport/gateway_client.dart';
import '../transport/jsonrpc.dart' show RpcError, RpcMessage;
import '../transport/rpc_client.dart';
import 'aggregate.dart';
import 'channel.dart';
import 'handshake.dart';
import 'keypair.dart';
import 'trustlog/trust_store.dart';

/// A node reachable through the blind gateway. [identityPubKey] is the node's
/// base64 Curve25519 static key (its Noise responder identity).
class NodeDescriptor {
  const NodeDescriptor({required this.id, this.label, required this.identityPubKey});
  final String id;
  final String? label;
  final String identityPubKey;
}

/// One established E2E channel to a node.
class NodeChannel {
  NodeChannel(this.nodeId, this.chanId, this.channel);
  final String nodeId;
  final String chanId;
  final Channel channel;
}

/// A notification decrypted from a node, tagged with its origin.
typedef NodeEvent = ({String method, Uint8List params, String nodeId});

/// Talks to nodes over end-to-end encrypted channels relayed by a blind gateway.
/// Gateway-level RPCs (relay.open/ping) go through a reused [RpcClient]; relay
/// frames are demuxed to per-node channels. Single dial (no reconnection here).
class E2EClient implements GatewayClient {
  E2EClient(
    this._incoming,
    this._send,
    this._static, {
    this.handshakeTimeout = const Duration(seconds: 15),
    this.callTimeout = const Duration(seconds: 30),
    Uint8List? genesisHead,
    Uint8List? initialTrustChain,
    bool tofu = false,
    this.trustResyncInterval,
    this.onTrustChainAdvance,
  })  : _trust = tofu
            ? TrustStore.tofu()
            : (genesisHead != null ? TrustStore(genesisHead) : null),
        _initialTrustChain = initialTrustChain {
    _sub = _incoming.listen(_onMessage, onDone: _onDone, cancelOnError: false);
    _gateway = RpcClient(incoming: _gatewayCtrl.stream, sendFrame: _send);
  }

  final Stream<RpcMessage> _incoming;
  final void Function(String) _send;
  final KeyPair _static;
  final Duration handshakeTimeout;
  final Duration callTimeout;
  final TrustStore? _trust;
  final Uint8List? _initialTrustChain;

  /// When set (with a trust store), the client periodically re-pulls the trust
  /// log so mid-session revocations take effect; null disables background re-sync.
  final Duration? trustResyncInterval;

  /// Called with the new chain bytes whenever a background re-sync advances the
  /// verified trust chain, so the caller can persist it. Storage-agnostic.
  final Future<void> Function(Uint8List chain)? onTrustChainAdvance;
  Timer? _resyncTimer;
  Uint8List? get trustChainBytes => _trust?.chainBytes;
  Uint8List? get trustHead => _trust?.head;
  List<Uint8List>? get trustSigners => _trust?.signers;

  /// null when there is no trust store or the network is open (not locked);
  /// true when locked-mode enforcement is active.
  bool? get isLocked => (_trust == null || !_trust.locked) ? null : true;

  /// Whether this device's static identity is authorized by the current trust log.
  bool get isAuthorized => _trust?.deviceAuthorized(_static.publicKey) ?? false;

  /// Whether the trust log has been disabled (break-glass).
  bool get isDisabled => _trust?.disabled ?? false;

  late final StreamSubscription<RpcMessage> _sub;
  final _gatewayCtrl = StreamController<RpcMessage>();
  late final RpcClient _gateway;

  final _byChanId = <String, NodeChannel>{};
  final _handshakes = <String, Completer<Uint8List>>{};
  final _pending = <String, Completer<Uint8List>>{};
  final _events = StreamController<NodeEvent>.broadcast();
  int _nextId = 0;
  bool _closed = false;

  final _byNodeId = <String, NodeChannel>{};
  final _roster = <String, NodeDescriptor>{};
  final _subNode = <String, String>{};
  final _termNode = <String, String>{};

  Stream<NodeEvent> get events => _events.stream;

  Iterable<String> get connectedNodeIds => _byNodeId.keys;

  StreamController<RpcMessage>? _notificationsCtrl;
  StreamSubscription<({String method, Object? params})>? _notificationsSub;

  @override
  Stream<RpcMessage> get notifications {
    final existing = _notificationsCtrl;
    if (existing != null) return existing.stream;
    final ctrl = StreamController<RpcMessage>.broadcast();
    _notificationsCtrl = ctrl;
    _notificationsSub = aggregatedEvents.listen(
      (e) => ctrl.add(RpcMessage(method: e.method, params: e.params)),
      onError: ctrl.addError,
    );
    return ctrl.stream;
  }

  /// The per-node notification stream, decoded and (for session.event) stamped
  /// with composite node origin — the aggregated view the app consumes.
  Stream<({String method, Object? params})> get aggregatedEvents => events.map((e) {
        Object? params;
        try {
          params = jsonDecode(utf8.decode(e.params));
        } catch (_) {
          return (method: e.method, params: null);
        }
        if (e.method == 'session.event' && params is Map<String, dynamic>) {
          final sess = params['session'];
          if (sess is Map<String, dynamic>) {
            params = {
              ...params,
              'session': withOriginJson(sess, e.nodeId, _roster[e.nodeId]?.label),
            };
          }
        }
        return (method: e.method, params: params);
      });

  /// Discovers nodes (nodes.list) and opens an E2E channel to each node that
  /// advertises an identity key. When a genesis head is configured, pulls and
  /// ingests the trust log first, then skips any node whose identity key is not
  /// authorized (fail-closed: a failed pull leaves prior state in place).
  Future<void> connect() async {
    final res = await _gateway.call('nodes.list');
    final nodes = (res is Map ? res['nodes'] : null) as List? ?? const [];
    if (_trust != null) {
      // Rollback anchor: re-verify the last-known-good chain before pulling, so the
      // gateway's chain must be a monotonic extension (a stale/shorter chain is
      // rejected). A tampered/rolled-back stored chain fails verification and is
      // dropped (fail-closed).
      final seed = _initialTrustChain;
      if (seed != null) {
        try {
          await _trust.ingest(seed);
        } catch (_) {/* corrupt/rolled-back seed: ignore, fail-closed */}
      }
      try {
        final pull = await _gateway.call('trustlog.pull');
        final chain = pull is Map ? pull['chain'] : null;
        if (chain is String && chain.isNotEmpty) {
          await _trust.ingest(Uint8List.fromList(base64.decode(chain)));
        }
      } catch (_) {/* keep prior/seeded state (fail-closed) */}
    }
    final toOpen = <NodeDescriptor>[];
    for (final n in nodes) {
      if (n is! Map) continue;
      final key = n['identity_pubkey'];
      if (key is! String || key.isEmpty) continue;
      final pub = base64.decode(key);
      if (_trust != null && _trust.locked && !_trust.disabled && !_trust.deviceAuthorized(pub)) continue;
      toOpen.add(NodeDescriptor(id: n['id'] as String, label: n['label'] as String?, identityPubKey: key));
    }
    await Future.wait(toOpen.map((desc) async {
      final nc = await openChannel(desc);
      _byNodeId[desc.id] = nc;
      _roster[desc.id] = desc;
    }));
    final interval = trustResyncInterval;
    if (_trust != null && interval != null && !_closed) {
      _resyncTimer = Timer.periodic(interval, (_) => resyncNow());
    }
  }

  /// Re-pulls the trust log and, on a verified advance, persists it (via
  /// [onTrustChainAdvance]) and drops channels to now-unauthorized nodes. Also
  /// runs periodically when [trustResyncInterval] is set; exposed for a manual
  /// refresh and for tests. Errors are swallowed (the current view is kept).
  Future<void> resyncNow() async {
    final trust = _trust;
    if (trust == null || _closed) return;
    final before = trust.chainBytes;
    try {
      final pull = await _gateway.call('trustlog.pull');
      final chain = pull is Map ? pull['chain'] : null;
      if (chain is! String || chain.isEmpty) return;
      await trust.ingest(Uint8List.fromList(base64.decode(chain)));
    } catch (_) {
      return; // keep the current verified view (fail-closed)
    }
    final after = trust.chainBytes;
    if (after == null || (before != null && _bytesEqual(before, after))) return;
    await onTrustChainAdvance?.call(after);
    _reevaluateChannels();
  }

  /// Closes channels to nodes no longer authorized by the current trust log.
  /// A nil/disabled/unlocked store closes nothing (disabled intentionally opens
  /// access). Only closes — never opens newly-authorized nodes.
  void _reevaluateChannels() {
    final trust = _trust;
    if (trust == null || !trust.locked || trust.disabled) return;
    final drop = <String>[];
    for (final id in _byNodeId.keys) {
      final desc = _roster[id];
      if (desc == null) continue;
      final pub = base64.decode(desc.identityPubKey);
      if (!trust.deviceAuthorized(pub)) drop.add(id);
    }
    for (final id in drop) {
      final nc = _byNodeId.remove(id);
      _roster.remove(id);
      if (nc != null) _byChanId.remove(nc.chanId);
    }
  }

  static bool _bytesEqual(Uint8List a, Uint8List b) {
    if (a.length != b.length) return false;
    for (var i = 0; i < a.length; i++) {
      if (a[i] != b[i]) return false;
    }
    return true;
  }

  /// Aggregating RPC: reproduces the gateway's cross-node aggregation. Mirrors
  /// RpcClient.call's signature so the app can swap transports.
  @override
  Future<Object?> call(String method, [Object? params]) async {
    switch (method) {
      case 'sessions.list':
      case 'sessions.refresh':
        return _fanoutSessions(method, params);
      case 'sessions.historyProjects':
        return _fanoutHistoryProjects(params);
      case 'transcript.unsubscribe':
        return _routeByHandle(_subNode, stringField(params, 'sub_id'), method, params);
    }
    if (sessionAddressed.contains(method)) return _routeBySession(method, params);
    if (nodeAddressed.contains(method)) return _routeByNode(method, params);
    if (terminalHandleAddressed.contains(method)) {
      return _routeByHandle(_termNode, stringField(params, 'term_id'), method, params);
    }
    return _gateway.call(method, params); // gateway-native passthrough
  }

  Future<Object?> _callNodeDecoded(String nodeId, String method, Object? params) async {
    final nc = _byNodeId[nodeId];
    if (nc == null) throw StateError('unknown node $nodeId');
    final result = await callNode(nc, method, utf8.encode(jsonEncode(params)));
    if (result.isEmpty) return null;
    return jsonDecode(utf8.decode(result));
  }

  Future<List<dynamic>> _fanoutSessions(String method, Object? params) async {
    final entries = _byNodeId.keys.toList();
    final results = await Future.wait(entries.map((nodeId) async {
      try {
        final r = await _callNodeDecoded(nodeId, method, params);
        return (nodeId, r is List ? r : const []);
      } catch (_) {
        return (nodeId, const <dynamic>[]);
      }
    }));
    final merged = <dynamic>[];
    for (final (nodeId, list) in results) {
      final label = _roster[nodeId]?.label;
      for (final s in list) {
        if (s is Map<String, dynamic>) merged.add(withOriginJson(s, nodeId, label));
      }
    }
    return merged;
  }

  Future<List<dynamic>> _fanoutHistoryProjects(Object? params) async {
    final entries = _byNodeId.keys.toList();
    final results = await Future.wait(entries.map((nodeId) async {
      try {
        final r = await _callNodeDecoded(nodeId, 'sessions.historyProjects', params);
        return (nodeId, r is List ? r : const []);
      } catch (_) {
        return (nodeId, const <dynamic>[]);
      }
    }));
    final all = <Map<String, dynamic>>[];
    for (final (nodeId, list) in results) {
      final label = _roster[nodeId]?.label;
      for (final p in list) {
        if (p is Map<String, dynamic>) all.add({...p, 'node_id': nodeId, 'node_label': label});
      }
    }
    all.sort((a, b) =>
        (b['last_activity'] as String? ?? '').compareTo(a['last_activity'] as String? ?? ''));
    return all;
  }

  String? _soleNode() => _byNodeId.length == 1 ? _byNodeId.keys.first : null;

  Future<Object?> _routeBySession(String method, Object? params) async {
    final composite = stringField(params, 'session_id');
    if (composite == null) {
      throw RpcError(-32600, '$method requires session_id');
    }
    final (nodeId, localId, ok) = splitCompositeId(composite);
    if (!ok) {
      throw RpcError(-32600, 'session id is not gateway-qualified: $composite');
    }
    final result = await _callNodeDecoded(nodeId, method, rewriteSessionId(params, localId));
    if (method == 'transcript.subscribe') {
      final sub = stringField(params, 'sub_id');
      if (sub != null && sub.isNotEmpty) _subNode[sub] = nodeId;
    } else if (method == 'terminal.open') {
      final term = stringField(params, 'term_id');
      if (term != null && term.isNotEmpty) _termNode[term] = nodeId;
    }
    return result;
  }

  Future<Object?> _routeByNode(String method, Object? params) async {
    var nodeId = stringField(params, 'node_id') ?? '';
    if (nodeId.isEmpty) {
      nodeId = _soleNode() ?? '';
      if (nodeId.isEmpty) throw RpcError(-32600, '$method requires node_id');
    }
    final result = await _callNodeDecoded(nodeId, method, params);
    if (compositeResultMethods.contains(method) && result is Map<String, dynamic>) {
      final local = result['session_id'];
      if (local is String && local.isNotEmpty) {
        return {...result, 'session_id': compositeId(nodeId, local)};
      }
      return result;
    }
    if (method == 'sessions.historySessions' && result is Map<String, dynamic>) {
      final items = result['items'];
      if (items is List) {
        final label = _roster[nodeId]?.label;
        return {
          ...result,
          'items': [
            for (final it in items)
              if (it is Map<String, dynamic>) {...it, 'node_id': nodeId, 'node_label': label} else it
          ],
        };
      }
    }
    return result;
  }

  Future<Object?> _routeByHandle(
      Map<String, String> table, String? id, String method, Object? params) async {
    if (id == null || id.isEmpty) {
      throw RpcError(-32600, '$method requires a handle id');
    }
    final nodeId = table[id];
    if (nodeId == null) {
      throw RpcError(-32600, '$method: unknown handle $id');
    }
    return _callNodeDecoded(nodeId, method, params);
  }

  Future<NodeChannel> openChannel(NodeDescriptor node) async {
    final res = await _gateway.call('relay.open', {'node_id': node.id}).timeout(handshakeTimeout);
    final chanId = (res as Map)['chan_id'] as String;
    final pub = base64.decode(node.identityPubKey);
    final (hs, msg1) = await HandshakeState.initiate(
        staticKey: _static, remoteStatic: pub, prologue: channelPrologue(node.id, chanId));
    final hc = Completer<Uint8List>();
    _handshakes[chanId] = hc;
    _writeFrame(marshalHandshakeFrame(chanId, msg1));
    final Uint8List msg2;
    try {
      msg2 = await hc.future.timeout(handshakeTimeout);
    } finally {
      _handshakes.remove(chanId);
    }
    final nc = NodeChannel(node.id, chanId, Channel(chanId, hs.finish(msg2)));
    _byChanId[chanId] = nc;
    return nc;
  }

  Future<Uint8List> callNode(NodeChannel nc, String method, List<int> params) {
    if (_closed) return Future.error(StateError('client closed'));
    final idn = ++_nextId;
    final id = idn.toString();
    final c = Completer<Uint8List>();
    _pending[id] = c;
    _writeFrame(nc.channel.sealRequestFrame(idn, method, nc.nodeId, params));
    return c.future.timeout(callTimeout, onTimeout: () {
      _pending.remove(id);
      throw TimeoutException('callNode $method timed out');
    });
  }

  void _onMessage(RpcMessage m) {
    final route = m.route;
    if (route is Map && route['chan_id'] is String) {
      _onRelay(m, route['chan_id'] as String);
    } else {
      if (!_gatewayCtrl.isClosed) _gatewayCtrl.add(m);
    }
  }

  void _onRelay(RpcMessage m, String chanId) {
    if (m.method == methodE2EHandshake) {
      final c = _handshakes[chanId];
      if (c != null && !c.isCompleted) {
        try {
          c.complete(handshakeFromFrame(RelayFrame.fromMessage(m)));
        } catch (_) {/* malformed handshake: leave pending -> openChannel times out */}
      }
      return;
    }
    final nc = _byChanId[chanId];
    if (nc == null) return;
    final f = RelayFrame.fromMessage(m);
    if (m.id != null && m.method == null) {
      final waiter = _pending[m.id];
      if (waiter == null || waiter.isCompleted) return;
      try {
        final r = nc.channel.openResponse(f);
        _pending.remove(m.id);
        if (r.error != null) {
          waiter.completeError(r.error!);
        } else {
          waiter.complete(r.result!); // non-null on the non-error path
        }
      } catch (_) {
        // injected/garbage or desynced frame: drop it, keep the pending slot so
        // the genuine reply (which decrypts at the still-unadvanced nonce) resolves.
      }
    } else if (m.method != null && m.id == null) {
      try {
        final params = nc.channel.openParams(f);
        if (!_events.isClosed) {
          _events.add((method: m.method!, params: params, nodeId: nc.nodeId));
        }
      } catch (_) {/* drop */}
    }
  }

  void _writeFrame(Uint8List frameBytes) => _send('${utf8.decode(frameBytes)}\n');

  void _onDone() {
    if (_closed) return;
    _closed = true;
    _gateway.close();
    if (!_gatewayCtrl.isClosed) _gatewayCtrl.close();
    _failAll(StateError('gateway link closed'));
    if (!_events.isClosed) _events.close();
  }

  void _failAll(Object e) {
    for (final c in [..._handshakes.values, ..._pending.values]) {
      if (!c.isCompleted) c.completeError(e);
    }
    _handshakes.clear();
    _pending.clear();
  }

  @override
  Future<void> close() async {
    if (_closed) return;
    _closed = true;
    _resyncTimer?.cancel();
    await _sub.cancel();
    _gateway.close();
    if (!_gatewayCtrl.isClosed) await _gatewayCtrl.close();
    _failAll(StateError('client closed'));
    if (!_events.isClosed) await _events.close();
    await _notificationsSub?.cancel();
    final ctrl = _notificationsCtrl;
    if (ctrl != null && !ctrl.isClosed) await ctrl.close();
  }
}
