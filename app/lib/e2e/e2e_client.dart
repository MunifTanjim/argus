import 'dart:async';
import 'dart:convert';
import 'dart:developer' as developer;
import 'dart:typed_data';

import 'package:flutter/foundation.dart' show visibleForTesting;

import '../transport/gateway_client.dart';
import '../transport/jsonrpc.dart' show RpcError, RpcMessage;
import '../transport/rpc_client.dart';
import 'aggregate.dart';
import 'beacon.dart';
import 'bytes.dart' show bytesEqual, hexEncode;
import 'channel.dart';
import 'handshake.dart';
import 'keypair.dart';
import 'trustlog/codec.dart' show hashEntry, unmarshalChain;
import 'trustlog/entry.dart' show Entry;
import 'trustlog/trust_store.dart';

/// A node reachable through the blind gateway. [identityPubKey] is the node's
/// base64 Curve25519 static key (its Noise responder identity).
/// [beaconPubKey] is the node's base64 Ed25519 beacon public key (anti-
/// equivocation); [beacon] is the node's latest signed HEAD beacon.
class NodeDescriptor {
  const NodeDescriptor({
    required this.id,
    this.label,
    required this.identityPubKey,
    this.beaconPubKey,
    this.beacon,
  });
  final String id;
  final String? label;
  final String identityPubKey;
  final String? beaconPubKey;
  final Beacon? beacon;
}

/// Tracks consecutive unreconciled ticks for a single node's beacon tip.
class _BeaconMissState {
  _BeaconMissState({required this.tip, this.misses = 0});
  final Uint8List tip;
  int misses;
}

/// Number of consecutive unreconciled ticks before the equivocation flag is set.
const int _beaconMissThreshold = 2;

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
    Uint8List? genesisHash,
    Uint8List? initialTrustChain,
    bool tofu = false,
    this.trustResyncInterval,
    this.onTrustChainAdvance,
  })  : _trust = tofu
            ? TrustStore.tofu()
            : (genesisHash != null ? TrustStore(genesisHash) : null),
        _initialTrustChain = initialTrustChain {
    _sub = _incoming.listen(_onMessage, onDone: _onDone, cancelOnError: false);
    _gateway = RpcClient(incoming: _gatewayCtrl.stream, sendFrame: _send);
    _gatewayNotifSub = _gateway.notifications.listen(_onGatewayNotification);
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
  Uint8List? get trustTip => _trust?.tip;
  List<Uint8List>? get trustSigners => _trust?.signers;

  /// null when there is no trust store or the network is open (not locked);
  /// true when locked-mode enforcement is active.
  bool? get isLocked => (_trust == null || !_trust.locked) ? null : true;

  /// Whether this device's static identity is authorized by the current trust log.
  bool get isAuthorized => _trust?.deviceAuthorized(_static.publicKey) ?? false;

  /// Whether the trust log has been disabled (break-glass).
  bool get isDisabled => _trust?.disabled ?? false;

  /// Whether the client has detected a trust-log equivocation: one or more
  /// nodes reported a HEAD beacon whose tip could not be reconciled with the
  /// client's resolved chain after [_beaconMissThreshold] consecutive pulls.
  /// Once set, this flag is never cleared for the lifetime of the session.
  bool get equivocation => _equivocation;

  @visibleForTesting
  void debugCheckBeaconConsistency() => _checkBeaconConsistency();
  @visibleForTesting
  Set<String>? get debugBeaconKnown => _beaconKnown;

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

  // Beacon cross-check state (mirrors Go E2EClient beacon fields).
  // [_beacons] maps identity-pub hex to the latest verified beacon.
  // [_beaconCtr] tracks the last accepted counter for replay/stale detection.
  // [_beaconMiss] tracks consecutive unreconciled ticks per node; cleared on
  // reconcile or when the node's counter advances.
  // [_everConnected] records identity-pub hex keys for which a channel was
  // successfully opened at any point; used by [_checkBeaconConsistency] to
  // distinguish "once connected, now offline" (skip stale beacon) from "never
  // connected" (still check — legitimate equivocation signal).
  final _beacons = <String, Beacon>{};
  final _beaconCtr = <String, int>{};
  final _beaconMiss = <String, _BeaconMissState>{};
  final _everConnected = <String>{}; // identity-pub hex; never removed
  bool _equivocation = false;
  Uint8List? _beaconKnownTip; // caches the known-set key
  Set<String>? _beaconKnown; // resolved chain entry-hash set for beacon checks

  Stream<NodeEvent> get events => _events.stream;

  Iterable<String> get connectedNodeIds => _byNodeId.keys;

  StreamController<RpcMessage>? _notificationsCtrl;
  StreamSubscription<({String method, Object? params})>? _notificationsSub;
  StreamSubscription<RpcMessage>? _gatewayNotifSub;

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

  /// The per-node notification stream, decoded and (for session.event and
  /// tasks.changed) stamped with composite node origin — the aggregated view the
  /// app consumes.
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
        } else if (e.method == 'tasks.changed' && params is Map<String, dynamic>) {
          final sid = params['session_id'];
          if (sid is String && sid.isNotEmpty) {
            params = {...params, 'session_id': compositeId(e.nodeId, sid)};
          }
        }
        return (method: e.method, params: params);
      });

  /// Discovers nodes (nodes.list) and opens an E2E channel to each node that
  /// advertises an identity key. When a genesis hash is configured, pulls and
  /// ingests the trust log first, then skips any node whose identity key is not
  /// authorized (fail-closed: a failed pull leaves prior state in place).
  Future<void> connect() async {
    final res = await _gateway.call('nodes.list');
    final nodes = (res is Map ? res['nodes'] : null) as List? ?? const [];
    // Seed the initial beacon map from the roster snapshot before the trust-log
    // pull, so the first cross-check already has whatever beacons the gateway
    // advertises. Mirrors Go E2EClient.Connect beacon seeding order.
    for (final n in nodes) {
      if (n is! Map<String, dynamic>) continue;
      await _ingestBeaconFromDescriptor(_parseNodeDescriptor(n));
    }
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
        if (pull is Map) {
          // The pull result carries a list of competing branches (chains).
          // Each element is a base64-encoded chain; ingest all in order so the
          // genesis-pinned fork-choice resolves the winner.
          final chains = pull['chains'];
          if (chains is List) {
            for (final c in chains) {
              if (c is String && c.isNotEmpty) {
                try {
                  await _trust.ingest(Uint8List.fromList(base64.decode(c)));
                } catch (_) {/* bad branch: skip, keep best state so far */}
              }
            }
          }
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
      toOpen.add(_parseNodeDescriptor(n as Map<String, dynamic>));
    }
    await Future.wait(toOpen.map((desc) async {
      final nc = await openChannel(desc);
      _byNodeId[desc.id] = nc;
      _roster[desc.id] = desc;
      // Record identity pub hex so checkBeaconConsistency can distinguish
      // "was connected, now offline" from "never connected".
      if (desc.identityPubKey.isNotEmpty) {
        try {
          _everConnected.add(hexEncode(base64.decode(desc.identityPubKey)));
        } catch (_) {}
      }
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
      if (pull is Map) {
        final chains = pull['chains'];
        if (chains is List) {
          for (final c in chains) {
            if (c is String && c.isNotEmpty) {
              try {
                await trust.ingest(Uint8List.fromList(base64.decode(c)));
              } catch (_) {/* bad branch: skip */}
            }
          }
        }
      }
    } catch (_) {
      return; // keep the current verified view (fail-closed)
    }
    final after = trust.chainBytes;
    final changed = after != null && (before == null || !bytesEqual(before, after));
    if (changed) {
      await onTrustChainAdvance?.call(after!);
      _reevaluateChannels();
    }
    // Cross-check beacon tips against the resolved chain on every successful
    // pull — regardless of whether the chain advanced this tick. Mirrors Go's
    // syncTrustLog which always calls checkBeaconConsistency + deliverBeacons.
    _checkBeaconConsistency();
    await _deliverBeacons();
  }

  /// Closes channels to nodes no longer authorized by the current trust log.
  /// A nil/disabled/unlocked store closes nothing (disabled intentionally opens
  /// access). Only closes — never opens newly-authorized nodes. Also prunes
  /// beacon state for each dropped node so its stale tip cannot accumulate
  /// misses and false-positive the equivocation flag.
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
      final desc = _roster[id];
      final nc = _byNodeId.remove(id);
      _roster.remove(id);
      if (nc != null) _byChanId.remove(nc.chanId);
      // Prune beacon state so the revoked node's stale tip cannot accumulate
      // misses and false-positive the equivocation flag.
      if (desc != null && desc.identityPubKey.isNotEmpty) {
        try {
          final key = hexEncode(base64.decode(desc.identityPubKey));
          _beacons.remove(key);
          _beaconCtr.remove(key);
          _beaconMiss.remove(key);
        } catch (_) {}
      }
    }
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
    if (pushFanoutMethods.contains(method)) return _fanoutPush(method, params);
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

  /// Fans out a push.register/unregister/test call to every connected node channel.
  /// Succeeds (at-least-one) if any node accepts. For push.test, returns
  /// [pushGoneCode] only when every node reported gone. If no nodes are connected,
  /// falls back to the gateway so a plain-RPC connection still works.
  Future<Object?> _fanoutPush(String method, Object? params) async {
    final nodeIds = _byNodeId.keys.toList();
    if (nodeIds.isEmpty) return _gateway.call(method, params);
    final results = await Future.wait(nodeIds.map((nodeId) async {
      try {
        return (null as Object?, await _callNodeDecoded(nodeId, method, params));
      } on Object catch (e) {
        return (e, null as Object?);
      }
    }));
    Object? lastResult;
    var successCount = 0;
    final errors = <Object>[];
    var goneCount = 0;
    for (final (err, res) in results) {
      if (err == null) {
        successCount++;
        lastResult = res;
      } else {
        errors.add(err);
        if (err is RpcError && err.code == pushGoneCode) goneCount++;
      }
    }
    if (successCount > 0) return lastResult;
    if (method == 'push.test' && goneCount == nodeIds.length) {
      throw const RpcError(pushGoneCode, 'push target gone');
    }
    throw errors.first;
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

  /// Parses a raw nodes.list/node.event JSON map into a [NodeDescriptor],
  /// including beacon fields when present.
  NodeDescriptor _parseNodeDescriptor(Map<String, dynamic> n) {
    final beaconJson = n['beacon'];
    Beacon? beacon;
    if (beaconJson is Map<String, dynamic>) {
      try {
        beacon = Beacon.fromJson(beaconJson);
      } catch (_) {}
    }
    return NodeDescriptor(
      id: n['id'] as String? ?? '',
      label: n['label'] as String?,
      identityPubKey: n['identity_pubkey'] as String? ?? '',
      beaconPubKey: n['beacon_pubkey'] as String?,
      beacon: beacon,
    );
  }

  /// Validates and stores the beacon from a [NodeDescriptor]. Guards mirror Go
  /// E2EClient.ingestBeaconFromDescriptor:
  ///   1. beacon, identityPubKey, and beaconPubKey must all be present.
  ///   2. [verifyBeacon] must pass (Ed25519 signature check).
  ///   3. beacon.beaconPub must equal the roster-announced beaconPubKey.
  ///   4. beacon.counter must be strictly greater than the last accepted counter.
  /// A beacon failing any guard is silently dropped.
  Future<void> _ingestBeaconFromDescriptor(NodeDescriptor nd) async {
    final beacon = nd.beacon;
    final beaconPubKeyStr = nd.beaconPubKey;
    if (beacon == null || nd.identityPubKey.isEmpty || beaconPubKeyStr == null || beaconPubKeyStr.isEmpty) {
      return;
    }
    final Uint8List identityPub;
    final Uint8List expectedBeaconPub;
    try {
      identityPub = Uint8List.fromList(base64.decode(nd.identityPubKey));
      expectedBeaconPub = Uint8List.fromList(base64.decode(beaconPubKeyStr));
    } catch (_) {
      return;
    }
    if (!await verifyBeacon(beacon)) return; // signature invalid: drop
    if (!bytesEqual(beacon.beaconPub, expectedBeaconPub)) return; // key mismatch: drop
    final key = hexEncode(identityPub);
    final curCtr = _beaconCtr[key] ?? 0;
    if (beacon.counter <= curCtr) return; // stale or replayed: ignore
    _beacons[key] = beacon;
    _beaconCtr[key] = beacon.counter;
    _beaconMiss.remove(key); // counter advanced: new beacon supersedes any miss streak
  }

  /// Removes beacon state for the node described by [nd]. Used when a node goes
  /// offline or is removed from the roster so its stale cached beacon tip cannot
  /// accumulate misses and false-positive the equivocation flag.
  void _pruneBeaconForDescriptor(NodeDescriptor nd) {
    if (nd.identityPubKey.isEmpty) return;
    try {
      final key = hexEncode(base64.decode(nd.identityPubKey));
      _beacons.remove(key);
      _beaconCtr.remove(key);
      _beaconMiss.remove(key);
    } catch (_) {}
  }

  /// Handles a gateway-level notification. Filters for [node.event]:
  /// - type [beacon]: ingests the updated beacon.
  /// - type [offline] or [removed]: prunes the node's beacon state so stale
  ///   tips cannot accumulate misses after the node leaves the roster or goes
  ///   offline. Mirrors Go E2EClient.onPeerNotify.
  void _onGatewayNotification(RpcMessage msg) {
    if (msg.method != 'node.event') return;
    final params = msg.params;
    if (params is! Map<String, dynamic>) return;
    final nodeJson = params['node'];
    if (nodeJson is! Map<String, dynamic>) return;
    final evType = params['type'];
    if (evType == 'beacon') {
      _ingestBeaconFromDescriptor(_parseNodeDescriptor(nodeJson)).catchError((Object e, StackTrace st) {
        developer.log('e2e_client: beacon ingest error', name: 'e2e', error: e, stackTrace: st);
      });
    } else if (evType == 'offline' || evType == 'removed') {
      _pruneBeaconForDescriptor(_parseNodeDescriptor(nodeJson));
    }
  }

  /// Cross-checks all collected node beacons against the current resolved
  /// trust-log chain. Mirrors Go E2EClient.checkBeaconConsistency:
  /// a beacon whose Tip is not present in the client's linear chain history is
  /// tracked per-node; if the same unreconciled tip persists for
  /// [_beaconMissThreshold] consecutive ticks, [_equivocation] is set.
  /// Beacons for nodes that WERE connected (in [_everConnected]) but are no
  /// longer connected are skipped — a legitimate fork that orphans an offline
  /// node's cached tip must not accumulate misses and false-positive the flag.
  /// Nodes that report beacons but were NEVER connected are still checked.
  /// No-op when the trust store is absent or the chain is empty.
  void _checkBeaconConsistency() {
    final trust = _trust;
    if (trust == null) return;
    final chainBytes = trust.chainBytes;
    if (chainBytes == null || chainBytes.isEmpty) return;
    if (_beacons.isEmpty) return; // no beacons yet: skip the chain parse/hash entirely
    final tip = trust.tip;
    var known = _beaconKnown;
    if (known == null || tip == null || !bytesEqual(tip, _beaconKnownTip ?? const [])) {
      List<Entry> entries;
      try {
        entries = unmarshalChain(chainBytes);
      } catch (_) {
        return; // parse failure: be lenient rather than false-positive
      }
      if (entries.isEmpty) return;
      known = <String>{};
      for (final e in entries) {
        known.add(hexEncode(hashEntry(e)));
      }
      _beaconKnown = known;
      _beaconKnownTip = tip == null ? null : Uint8List.fromList(tip);
    }
    // Build the set of currently-connected identity-pub hex keys.
    final connected = <String>{};
    for (final nodeId in _byNodeId.keys) {
      final desc = _roster[nodeId];
      if (desc == null || desc.identityPubKey.isEmpty) continue;
      try {
        connected.add(hexEncode(base64.decode(desc.identityPubKey)));
      } catch (_) {}
    }
    for (final entry in _beacons.entries) {
      final key = entry.key;
      final b = entry.value;
      // Belt-and-suspenders: skip beacons for nodes that WERE connected but
      // are no longer connected. A legitimate fork that orphans an offline
      // node's stale cached tip must not accumulate misses and trigger the
      // flag. Nodes that report beacons but were NEVER connected are not
      // skipped — those beacons are still checked for equivocation.
      if (_everConnected.contains(key) && !connected.contains(key)) continue;
      if (b.tip.isEmpty) {
        _beaconMiss.remove(key); // no tip yet: clear any prior miss
        continue;
      }
      if (known.contains(hexEncode(b.tip))) {
        _beaconMiss.remove(key); // tip reconciled: reset miss streak
        continue;
      }
      // Tip not in resolved chain: track consecutive unreconciled ticks.
      var ms = _beaconMiss[key];
      if (ms == null || !bytesEqual(ms.tip, b.tip)) {
        ms = _BeaconMissState(tip: Uint8List.fromList(b.tip), misses: 1);
        _beaconMiss[key] = ms;
      } else {
        ms.misses++;
      }
      if (ms.misses >= _beaconMissThreshold && !_equivocation) {
        developer.log(
          'equivocation detected — node beacons diverge from resolved chain: key=$key tip=${hexEncode(b.tip)}',
          name: 'e2e',
          level: 900, // WARNING
        );
        _equivocation = true;
      }
    }
  }

  /// Couriers each collected signed node beacon to every OTHER connected node
  /// via the beacon.deliver E2E method. Mirrors Go E2EClient.deliverBeacons.
  /// Delivery is best-effort (errors silently ignored). A node's own beacon is
  /// never delivered back to that same node.
  Future<void> _deliverBeacons() async {
    if (_beacons.isEmpty || _byNodeId.isEmpty) return;
    // Build identity-pub hex → nodeId mapping from current channels + roster.
    final identHexToNode = <String, String>{};
    for (final nodeId in _byNodeId.keys) {
      final desc = _roster[nodeId];
      if (desc == null || desc.identityPubKey.isEmpty) continue;
      try {
        final pub = base64.decode(desc.identityPubKey);
        identHexToNode[hexEncode(pub)] = nodeId;
      } catch (_) {}
    }
    // Collect beacons with their source node ID.
    final todo = <({Beacon beacon, String sourceId})>[];
    for (final entry in _beacons.entries) {
      final srcId = identHexToNode[entry.key];
      if (srcId == null) continue; // node disconnected since beacon collected
      todo.add((beacon: entry.value, sourceId: srcId));
    }
    final targetIds = _byNodeId.keys.toList();
    for (final item in todo) {
      for (final targetId in targetIds) {
        if (targetId == item.sourceId) continue; // never deliver back to source
        try {
          await _callNodeDecoded(targetId, 'beacon.deliver', item.beacon.toJson());
        } catch (_) {/* best-effort */}
      }
    }
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
    await _gatewayNotifSub?.cancel();
    _gateway.close();
    if (!_gatewayCtrl.isClosed) await _gatewayCtrl.close();
    _failAll(StateError('client closed'));
    if (!_events.isClosed) await _events.close();
    await _notificationsSub?.cancel();
    final ctrl = _notificationsCtrl;
    if (ctrl != null && !ctrl.isClosed) await ctrl.close();
  }
}
