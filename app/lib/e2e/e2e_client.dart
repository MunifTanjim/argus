import 'dart:async';
import 'dart:convert';
import 'dart:developer' as developer;
import 'dart:math' show Random;
import 'dart:typed_data';

import 'package:cbor/simple.dart';
import 'package:flutter/foundation.dart' show visibleForTesting;
import 'package:pointycastle/digests/sha256.dart';
import 'package:pointycastle/macs/hmac.dart';
import 'package:pointycastle/api.dart' show KeyParameter;

import '../transport/connection.dart' show RpcLink;
import '../transport/frame_reader.dart' show FrameReader, JsonFrame;
import '../transport/gateway_client.dart';
import '../transport/jsonrpc.dart'
    show RpcError, RpcMessage, cborToDart, encodeRequest, jsonSafe;
import '../transport/rpc_client.dart';
import '../transport/ssh_bridge.dart'
    show NodeBridge, SshNodeTarget, defaultNodeSshCommand, parseSshNodeTarget;
import 'aggregate.dart';
import 'bytes.dart' show bytesEqual, hexDecode, hexEncode;
import 'channel.dart';
import 'handshake.dart';
import 'keypair.dart';
import 'trustlog/assemble.dart' show assembleChainsReport, chainEntries;
import 'trustlog/codec.dart' show hashEntry, unmarshalChain;
import 'trustlog/entry.dart' show Entry;
import 'trustlog/entry_store.dart' show EntryStore;
import 'trustlog/superseding.dart' show detectSupersession;
import 'trustlog/trust_store.dart';

part 'p2p.dart';
part 'ssh_link.dart';

/// A node reachable through the blind gateway. [identityPubKey] is the node's
/// base64 Curve25519 static key (its Noise responder identity).
class NodeDescriptor {
  const NodeDescriptor({
    required this.id,
    this.label,
    required this.identityPubKey,
    this.online = true,
    this.lockDisabled = false,
    this.p2p = false,
    this.p2pEndpoints = const [],
  });
  final String id;
  final String? label;
  final String identityPubKey;
  final bool online;

  /// The node turned off locked-mode enforcement for itself, so it accepts any
  /// device. Gateway-forgeable: a hint for which nodes to try, never trust.
  final bool lockDisabled;

  /// The node declared the p2p capability.
  final bool p2p;

  /// Direct URLs the node accepts clients on. Gateway-forgeable: a hint for
  /// where to dial; admission still authenticates the node.
  final List<String> p2pEndpoints;
}

/// Tracks consecutive unreconciled ticks for a single node's tip.
class _TipMissState {
  _TipMissState({required this.tip, this.misses = 0});
  final Uint8List tip;
  int misses;
}

/// Number of consecutive unreconciled ticks before the equivocation flag is set.
const int _tipMissThreshold = 2;

/// A channel to a node on one link: the gateway relay or a direct connection.
class NodeChannel {
  NodeChannel._(
    this.nodeId,
    this.chanId,
    this.channel, {
    required this.via,
    required Object link,
    required void Function(String line) send,
    void Function(Uint8List framed)? sendBytes,
    required void Function() onClose,
    this.routeNotes = true,
  }) : _link = link,
       _send = send,
       _sendBytes = sendBytes,
       _onClose = onClose;

  final String nodeId;
  final String chanId;
  final Channel channel;

  /// The link this channel travels on: `gateway`, `tcp` or `ssh`.
  final String via;

  // Frames are accepted only from this link.
  final Object _link;
  final void Function(String line) _send;
  final void Function(Uint8List framed)? _sendBytes;
  final void Function() _onClose;
  final _done = Completer<void>();
  bool _closed = false;

  /// The link's cbor declarations allow CBOR; set after node.identify.
  bool cbor = false;

  /// Notifications are delivered only while set. A standby or replaced link
  /// still resolves replies to its calls.
  bool routeNotes;

  /// The link a pending switch replaces.
  NodeChannel? replaces;

  /// Completes once the link has ended, closed by the client or dropped.
  Future<void> get done => _done.future;
  bool get isDone => _done.isCompleted;

  /// Writes a JSON frame.
  void send(Uint8List frame) => _send('${utf8.decode(frame)}\n');

  /// Writes a framed CBOR frame.
  void sendCbor(Uint8List framed) => _sendBytes!(framed);

  void _end() {
    if (!_done.isCompleted) _done.complete();
  }

  /// Ends the channel and its link, and removes it from the client.
  void close() {
    if (_closed) return;
    _closed = true;
    _end();
    _onClose();
  }
}

typedef _Pending = ({
  Completer<Uint8List> c,
  NodeChannel nc,
  void Function(({Uint8List? result, RpcError? error}) r)? inline,
});

typedef NodeEvent = ({
  String method,
  Uint8List params,
  String nodeId,
  bool cbor,
});

/// Methods whose params travel as CBOR on a channel that negotiated it.
const cborMethods = {'terminal.output', 'terminal.input'};

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
    this.upgradeCallTimeout = const Duration(minutes: 6),
    Uint8List? genesisHash,
    this._initialTrustChain,
    bool tofu = false,
    TrustStore? trustStore,
    this.trustResyncInterval,
    this.onTrustChainAdvance,
    bool plaintext = false,
    void Function(Uint8List)? sendBytes,
    DirectDialer? directDialer,
    String? p2pToken,
    SshOpener? sshOpener,
    Map<String, String> sshTargets = const {},
    String sshCommand = defaultNodeSshCommand,
  }) : _sendBytes = sendBytes,
       _directDialer = directDialer,
       _p2pToken = p2pToken,
       _sshOpener = sshOpener,
       _sshTargets = sshTargets,
       _sshCommand = sshCommand,
       _trust =
           trustStore ??
           (tofu
               ? TrustStore.tofu()
               : (genesisHash != null ? TrustStore(genesisHash) : null)),
       _plaintext = plaintext {
    _sub = _incoming.listen(_onMessage, onDone: _onDone, cancelOnError: false);
    _gateway = RpcClient(incoming: _gatewayCtrl.stream, sendFrame: _send);
    _gatewayNotifSub = _gateway.notifications.listen(_onGatewayNotification);
  }

  final Stream<RpcMessage> _incoming;
  final void Function(String) _send;
  // CBOR is used only when set.
  final void Function(Uint8List)? _sendBytes;
  bool _gatewayCbor = false;
  final KeyPair _static;
  final Duration handshakeTimeout;
  final Duration callTimeout;
  // Outlasts the daemon's own five-minute download budget.
  final Duration upgradeCallTimeout;
  final TrustStore? _trust;
  final Uint8List? _initialTrustChain;
  final bool _plaintext;

  /// Dials direct endpoints; null keeps every node on the relay.
  final DirectDialer? _directDialer;

  /// The client token plaintext direct admission presents.
  final String? _p2pToken;

  /// Opens node SSH links; null or no [_sshTargets] keeps SSH off.
  final SshOpener? _sshOpener;

  /// SSH destinations by node id or label.
  final Map<String, String> _sshTargets;

  /// The remote command node SSH links run.
  final String _sshCommand;

  /// How often a node on the relay retries its upgrade.
  static Duration p2pRetryInterval = const Duration(seconds: 60);

  /// Keepalive on direct links, so a half-open link is detected.
  static Duration p2pKeepaliveInterval = const Duration(seconds: 20);
  static Duration p2pKeepaliveTimeout = const Duration(seconds: 10);
  static int p2pKeepaliveFailures = 1;

  /// How long a replaced link stays open for its pending calls.
  static Duration switchSettleTimeout = const Duration(seconds: 5);

  // The gateway connection, as a link identity.
  static final _gatewayLink = Object();
  bool _gatewayP2P = false;
  Future<void> _serverInfo = Future.value();
  final _upgrading = <String, Object>{};
  final _upgradeWake = <String, Completer<void>>{};
  // Bumped by retryUpgradesNow, so a wake during an attempt is not lost.
  int _wakeGen = 0;
  final _p2pWarned = <String>{};
  final _paths = StreamController<NodePath>.broadcast();
  final _reconciles = StreamController<NodeSessions>.broadcast();

  /// Each node's link: on every open (gateway), switch and fallback, and an
  /// empty via when the node loses its link (roster, trust, gateway loss).
  Stream<NodePath> get nodePaths => _paths.stream;

  /// A node's sessions from its new link after a switch; they replace the
  /// node's sessions in the view, before the switch is reported on
  /// [nodePaths].
  Stream<NodeSessions> get sessionReconciles => _reconciles.stream;

  /// The link each open node uses, by node id.
  Map<String, String> get nodeVias => {
    for (final e in _byNodeId.entries) e.key: e.value.via,
  };

  /// Retries every relay node's upgrade, and SSH for nodes on tcp, now (app
  /// resume, a trusted host key).
  void retryUpgradesNow() {
    _wakeGen++;
    _wakeUpgrades();
  }

  /// Opens and identifies a direct link to [node] on the first endpoint that
  /// admits it, without switching the node onto it. Null when the node may
  /// not be dialed or no endpoint answered.
  Future<NodeChannel?> dialTcp(NodeDescriptor node) async {
    final cur = _byNodeId[node.id];
    Object? err;
    for (final raw in _directEndpoints(node)) {
      final u = Uri.tryParse(raw);
      if (u == null ||
          (u.scheme != 'ws' && u.scheme != 'wss') ||
          u.host.isEmpty) {
        _logOnce(
          'endpoint\x00$raw',
          'node ${node.id}: ignoring direct endpoint "$raw": not a ws or wss URL',
        );
        continue;
      }
      try {
        final nc = _plaintext
            ? await _dialPlainEndpoint(node, raw)
            : await _dialEndpoint(node, raw);
        nc.replaces = cur;
        return nc;
      } catch (e) {
        err = e;
      }
    }
    if (err != null) {
      _logOnce(
        '${node.id}\x00$err',
        'node ${node.id} direct link unavailable: $err',
      );
    }
    return null;
  }

  final _entryStore = EntryStore();
  // Last unplaced count that triggered a warning. 0 means no active warning;
  // reset to 0 when the count returns to 0 so a later recurrence is reported again.
  int _lastUnplacedLogged = 0;
  // Latch for the disjoint warning: log on false→true, stay quiet while true,
  // reset to false when the flag clears so a later recurrence is reported again.
  bool _lastDisjointLogged = false;

  /// When set (with a trust store), the client periodically re-pulls the trust
  /// log so mid-session revocations take effect; null disables background re-sync.
  final Duration? trustResyncInterval;

  /// Called with the new chain bytes whenever a background re-sync advances the
  /// verified trust chain, so the caller can persist it. Storage-agnostic.
  final Future<void> Function(Uint8List chain)? onTrustChainAdvance;
  Timer? _resyncTimer;
  bool _resyncInFlight = false;
  bool _resyncPending = false;
  Uint8List? get trustChainBytes => _trust?.chainBytes;
  Uint8List? get trustTip => _trust?.tip;
  List<Uint8List>? get trustSigners => _trust?.signers;

  Uint8List? _supersededBy;
  Uint8List? _supersedingChain;

  /// The successor root, or null. Non-null only when this device's pinned root
  /// was disabled and a different root is served.
  Uint8List? get supersededByGenesis => _supersededBy;

  /// True when a live successor is held. False when superseded but no live root
  /// exists yet.
  bool get canAdoptSupersedingRoot => _supersedingChain != null;

  /// null when there is no trust store or the network is open (not locked);
  /// true when locked-mode enforcement is active.
  bool? get isLocked => (_trust == null || !_trust.locked) ? null : true;

  /// Whether this device's static identity is authorized by the current trust log.
  bool get isAuthorized => _trust?.deviceAuthorized(_static.publicKey) ?? false;

  /// Whether the trust log has been disabled (break-glass).
  bool get isDisabled => _trust?.disabled ?? false;

  /// Whether the client has detected a trust-log equivocation: one or more
  /// nodes reported a tip over its authenticated channel that could not be
  /// reconciled with the client's resolved chain after [_tipMissThreshold]
  /// consecutive pulls. Once set, this flag is never cleared for the lifetime
  /// of the session.
  bool get equivocation => _equivocation;

  @visibleForTesting
  void debugCheckTipConsistency() => _checkTipConsistency();
  @visibleForTesting
  Set<String>? get debugKnownSet => _knownSet;
  @visibleForTesting
  int get debugLastUnplacedLogged => _lastUnplacedLogged;

  late final StreamSubscription<RpcMessage> _sub;
  final _gatewayCtrl = StreamController<RpcMessage>();
  late final RpcClient _gateway;

  final _byChanId = <String, NodeChannel>{};
  final _handshakes = <String, ({Object link, Completer<Uint8List> c})>{};
  final _pending = <String, _Pending>{};
  final _events = StreamController<NodeEvent>.broadcast();
  int _nextId = 0;
  bool _closed = false;

  final _byNodeId = <String, NodeChannel>{};
  final _opening = <String, Future<void>>{};
  // Completes once a node's channel is registered, before the open's
  // node.identify, so a call to an opening node waits no longer than needed.
  final _registering = <String, Future<void>>{};
  // Completes once connect has loaded the trust log. Before that a TOFU store
  // holds no chain and reads as an open network, so adoption must wait.
  final _trustLoaded = Completer<void>();
  final _roster = <String, NodeDescriptor>{};
  final _subNode = <String, String>{};
  final _termNode = <String, String>{};

  // Tip cross-check state (mirrors Go E2EClient tip fields).
  // [_authTip] maps identity-pub hex to the tip each node reported over its
  // authenticated channel (node.identify), refreshed once per sync tick.
  // [_tipMiss] tracks consecutive unreconciled ticks per node; cleared on reconcile.
  // [_everConnected] records identity-pub hex keys for which a channel was
  // successfully opened at any point; used by [_checkTipConsistency] to
  // distinguish "once connected, now offline" (skip stale tip) from "never
  // connected" (still check — legitimate equivocation signal).
  final _authTip = <String, Uint8List>{};
  final _tipMiss = <String, _TipMissState>{};
  final _everConnected = <String>{}; // identity-pub hex; never removed
  bool _equivocation = false;
  Uint8List? _knownSetTip; // caches the known-set key
  Set<String>? _knownSet; // resolved chain entry-hash set for tip checks

  Stream<NodeEvent> get events => _events.stream;

  Set<String> get connectedNodeIds => _byNodeId.keys.toSet();

  /// Their sessions are not yet loaded.
  Set<String> get openingNodeIds => _registering.keys.toSet();

  /// Lets a fan-out reach the nodes still opening too.
  Future<void> _channelsSettled() => Future.wait(_registering.values.toList());

  /// Completes once the channels [connect] started have opened or failed.
  @visibleForTesting
  Future<void> get channelsSettled => _channelsSettled();

  /// Every node a fan-out reaches, the ones still opening included.
  Future<List<String>> _settledNodeIds() async {
    await _channelsSettled();
    return _byNodeId.keys.toList();
  }

  /// Fans out [method] (sessions.list / sessions.refresh) to every connected node,
  /// invoking [onNode] with each node's composited sessions AS THAT NODE RESPONDS
  /// — not gated on the slowest. A node that errors or times out is skipped (its
  /// prior sessions stay in the store). Completes when all nodes have settled.
  Future<void> forEachNodeSessions(
    String method,
    void Function(String nodeId, List<Map<String, dynamic>> sessions) onNode,
  ) async {
    final entries = {..._byNodeId.keys, ..._registering.keys};
    await Future.wait(
      entries.map((nodeId) async {
        try {
          final r = await _callNodeDecoded(nodeId, method, null);
          final list = r is List ? r : const [];
          final label = _roster[nodeId]?.label;
          final out = <Map<String, dynamic>>[
            for (final s in list)
              if (s is Map<String, dynamic>) withOriginJson(s, nodeId, label),
          ];
          onNode(nodeId, out);
        } catch (_) {}
      }),
    );
  }

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

  /// The per-node notification stream, decoded and (for session.event,
  /// tasks.changed, and terminal.changed) stamped with node origin — the
  /// aggregated view the app consumes.
  Stream<({String method, Object? params})> get aggregatedEvents => events.map((
    e,
  ) {
    Object? params;
    try {
      params = e.cbor
          ? cborToDart(cbor.decode(e.params))
          : jsonDecode(utf8.decode(e.params));
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
    } else if (e.method == 'terminal.changed') {
      params = {'node_id': e.nodeId};
    }
    return (method: e.method, params: params);
  });

  static bool _hasCbor(Object? info) {
    final caps = info is Map ? info['capabilities'] : null;
    return caps is Map && caps['cbor'] == true;
  }

  /// Discovers nodes (nodes.list) and opens an E2E channel to each node that
  /// advertises an identity key. When a genesis hash is configured, pulls and
  /// ingests the trust log first, then skips any node whose identity key is not
  /// authorized (fail-closed: a failed pull leaves prior state in place).
  ///
  /// It returns once the trust log is loaded; the channels keep opening in the
  /// background.
  Future<void> connect() async {
    final serverInfo = _gateway
        .call('server.info')
        .then<Object?>((i) => i, onError: (Object _) => null);
    _serverInfo = serverInfo.then((i) {
      final caps = i is Map ? i['capabilities'] : null;
      _gatewayP2P = caps is Map && caps['p2p'] == true;
    });
    final res = await _gateway.call('nodes.list');
    final nodes = (res is Map ? res['nodes'] : null) as List? ?? const [];
    if (_sendBytes != null) _gatewayCbor = _hasCbor(await serverInfo);
    if (_trust != null) {
      // Rollback anchor: re-verify the last-known-good chain before pulling, so the
      // gateway's chain must be a monotonic extension (a stale/shorter chain is
      // rejected). A tampered/rolled-back stored chain fails verification and is
      // dropped (fail-closed).
      final seed = _initialTrustChain;
      if (seed != null) {
        try {
          await _trust.ingest(seed);
        } catch (_) {
          /* corrupt/rolled-back seed: ignore, fail-closed */
        }
      }
      try {
        await _syncTrustLog(_trust);
      } catch (_) {
        /* keep prior/seeded state (fail-closed) */
      }
    }
    if (!_trustLoaded.isCompleted) _trustLoaded.complete();
    unawaited(_openEligible(nodes));
    final interval = trustResyncInterval;
    if (_trust != null && interval != null && !_closed) {
      _resyncTimer = Timer.periodic(interval, (_) => _kickResync());
    }
  }

  Future<void> _openEligible(List<Object?> nodes) => Future.wait([
    for (final n in nodes)
      if (n is Map<String, dynamic>) _parseNodeDescriptor(n),
  ].where(_eligible).map(_openNode));

  bool get _enforcing {
    final trust = _trust;
    return trust != null && trust.locked && !trust.disabled;
  }

  /// Whether locked-mode nodes will reject this device's channels, as the trust
  /// log does not authorize its identity key.
  bool get _selfUnauthorized =>
      _enforcing && !_trust!.deviceAuthorized(_static.publicKey);

  /// The connect/adopt gate. An offline node (within grace, no live relay peer)
  /// is skipped: relay.open would fail, and it attaches on a later roster update.
  bool _eligible(NodeDescriptor desc) {
    if (!desc.online) return false;
    if (_plaintext) return true;
    if (desc.identityPubKey.isEmpty) return false;
    if (_enforcing &&
        !_trust!.deviceAuthorized(base64.decode(desc.identityPubKey)))
      return false;
    return !_selfUnauthorized || desc.lockDisabled;
  }

  /// Opens a channel to [desc] unless one is open or opening; a second caller
  /// waits for the in-flight open, since a node is registered only once its
  /// handshake finishes. Mirrors Go openIfEligible.
  Future<void> _openNode(NodeDescriptor desc) {
    if (_closed || _byNodeId.containsKey(desc.id)) return Future.value();
    return _opening[desc.id] ??= () {
      final registered = Completer<void>();
      _registering[desc.id] = registered.future;
      return _doOpenNode(desc, registered).whenComplete(() {
        _opening.remove(desc.id);
        _registering.remove(desc.id);
        if (!registered.isCompleted) registered.complete();
      });
    }();
  }

  /// Opens a channel to [desc], records it in the roster, and reads its
  /// authenticated tip. A failed open is logged and swallowed so one unreachable
  /// node does not abort the caller (mirrors the Go client).
  Future<void> _doOpenNode(
    NodeDescriptor desc,
    Completer<void> registered,
  ) async {
    try {
      final nc = await openChannel(desc);
      _byNodeId[desc.id] = nc;
      _roster[desc.id] = desc;
      // A trust advance during the handshake may have revoked the node.
      if (_closed || !_eligible(desc)) {
        _loseNode(desc.id);
        return;
      }
      _emitPath(desc.id, nc.via);
      registered.complete();
      // Record identity pub hex so checkTipConsistency can distinguish
      // "was connected, now offline" from "never connected", then read the
      // node's tip over its authenticated channel (node.identify), declaring
      // cbor there.
      try {
        String? key;
        if (desc.identityPubKey.isNotEmpty) {
          key = hexEncode(base64.decode(desc.identityPubKey));
          _everConnected.add(key);
        }
        final res = await _callNodeDecoded(desc.id, 'node.identify', {
          'capabilities': {'cbor': true},
        });
        if (res is Map<String, dynamic>) {
          if (key != null) _authTip[key] = _decodeTip(res['tip']);
          nc.cbor = _gatewayCbor && _hasCbor(res);
        }
      } catch (e) {
        developer.log(
          'node ${desc.id} identify failed: $e',
          name: 'e2e',
          level: 900,
        );
      }
      _startUpgrade(desc.id);
    } catch (e) {
      developer.log(
        'skipping node ${desc.id}: open channel failed: $e',
        name: 'e2e',
        level: 900,
      );
    }
  }

  /// Adopts a node that joined the roster or came back online after [connect].
  /// Applies the same eligibility gate as connect (online, then the trust gate in
  /// e2e mode) and skips a node that is already connected. Mirrors Go
  /// E2EClient.adoptNode.
  Future<void> _adoptNode(NodeDescriptor desc) async {
    await _trustLoaded.future;
    if (_closed || !_eligible(desc)) return;
    // An open node keeps its link; a later upgrade uses its new endpoints.
    if (_byNodeId.containsKey(desc.id)) _roster[desc.id] = desc;
    await _openNode(desc);
  }

  /// Forgets a node that left the roster or went offline: drops its channel and
  /// prunes its tip state, so its sessions stop appearing and its stale tip cannot
  /// accumulate equivocation misses. Mirrors Go E2EClient.loseNode.
  void _loseNode(String id) {
    final desc = _roster.remove(id);
    _upgrading.remove(id);
    final wake = _upgradeWake.remove(id);
    if (wake != null && !wake.isCompleted) wake.complete();
    final nc = _byNodeId.remove(id);
    if (nc != null) {
      nc.close();
      _emitPath(id, '');
    }
    if (desc != null && desc.identityPubKey.isNotEmpty) {
      try {
        final key = hexEncode(base64.decode(desc.identityPubKey));
        _authTip.remove(key);
        _tipMiss.remove(key);
      } catch (_) {}
    }
  }

  /// Re-pulls the trust log and, on a verified advance, persists it (via
  /// [onTrustChainAdvance]) and drops channels to now-unauthorized nodes. Also
  /// runs periodically when [trustResyncInterval] is set; exposed for a manual
  /// refresh and for tests. Errors are swallowed (the current view is kept).
  Future<void> resyncNow() async {
    final trust = _trust;
    if (trust == null || _closed) return;
    final wasUnauthorized = _selfUnauthorized;
    final before = trust.chainBytes;
    try {
      await _syncTrustLog(trust);
    } catch (_) {
      return; // keep the current verified view (fail-closed)
    }
    final after = trust.chainBytes;
    final changed =
        after != null && (before == null || !bytesEqual(before, after));
    if (changed) {
      await onTrustChainAdvance?.call(after);
      _reevaluateChannels();
    }
    // Connect skipped every node while this device was unauthorized.
    if (wasUnauthorized && !_selfUnauthorized && !_closed) {
      try {
        final res = await _gateway.call('nodes.list');
        await _openEligible(
          (res is Map ? res['nodes'] : null) as List? ?? const [],
        );
      } catch (e) {
        developer.log(
          'nodes.list after authorization failed: $e',
          name: 'e2e',
          level: 900,
        );
      }
    }
    // Re-read each node's tip over its authenticated channel, then cross-check
    // those tips against the resolved chain on every successful pull — regardless
    // of whether the chain advanced this tick. Mirrors Go's syncTrustLog which
    // always calls refreshAuthTips + checkTipConsistency.
    await _refreshAuthTips();
    _checkTipConsistency();
  }

  /// Coalesces resync requests: a burst of trust-changed nudges (or a nudge
  /// overlapping the periodic tick) collapses to at most one in-flight run plus
  /// one queued follow-up — mirrors Go's buffered(1) kick consumed by the single
  /// trustSyncLoop, bounding gateway-driven work.
  Future<void> _kickResync() async {
    if (_resyncInFlight) {
      _resyncPending = true;
      return;
    }
    _resyncInFlight = true;
    try {
      do {
        _resyncPending = false;
        await resyncNow();
      } while (_resyncPending && !_closed);
    } finally {
      _resyncInFlight = false;
    }
  }

  /// Calls trustlog.sync, stores returned raw entries, assembles complete
  /// genesis-rooted chains, and ingests each into [trust]. Derives the offer
  /// from the entry store at call time — every retained entry hash is advertised,
  /// so the gateway computes the delta by set subtraction. Throws on RPC error
  /// (caller decides whether to swallow).
  Future<void> _syncTrustLog(TrustStore trust) async {
    // Seed from the verified trust chain first so the offer always reflects
    // what is locally held, including on the first call after a restart.
    final cb = trust.chainBytes;
    if (cb != null) {
      try {
        _entryStore.putAll(chainEntries(cb));
      } catch (_) {}
    }

    final (known, truncated) = _entryStore.hashes();
    final result = await _gateway.call('trustlog.sync', {
      'known': [for (final h in known) base64.encode(h)],
      if (truncated) 'truncated': true,
    });
    final gatewayEntries = <Uint8List>[];
    if (result is Map) {
      final disjoint = result['disjoint'] == true;
      if (disjoint && !_lastDisjointLogged) {
        developer.log(
          'trust log: this device shares no history with the network\'s; '
          'it is likely pinned to a different trust root',
          name: 'e2e',
          level: 900,
        );
      }
      _lastDisjointLogged = disjoint;

      // Decode gateway entries into the merge slice only — store write happens
      // after assembly, so only entries belonging to a complete genesis-rooted
      // chain are ever retained. Mirrors Go syncTrustChains exactly.
      final rawList = result['entries'];
      if (rawList is List) {
        for (final e in rawList) {
          if (e is String && e.isNotEmpty) {
            try {
              gatewayEntries.add(Uint8List.fromList(base64.decode(e)));
            } catch (_) {}
          }
        }
      }
    }

    final merged = <Uint8List>[];
    merged.addAll(gatewayEntries);
    final cb2 = trust.chainBytes;
    if (cb2 != null) {
      try {
        merged.addAll(chainEntries(cb2));
      } catch (_) {}
    }
    merged.addAll(_entryStore.all());

    final (chains, unplaced) = assembleChainsReport(merged);
    final prevUnplaced = _lastUnplacedLogged;
    _lastUnplacedLogged = unplaced;
    if (unplaced > 0 && unplaced != prevUnplaced) {
      developer.log(
        'trust-log sync has $unplaced unplaced entries; gateway may hold an incomplete branch',
        name: 'e2e',
        level: 900,
      );
    }

    var refused = 0;
    for (final chain in chains) {
      List<Uint8List> raw;
      try {
        raw = chainEntries(chain);
      } catch (_) {
        continue;
      }
      final (_, r) = _entryStore.putAll(raw);
      refused += r;
      try {
        await trust.ingest(chain);
      } catch (_) {
        /* bad branch: skip, keep best state so far */
      }
    }
    if (refused > 0) {
      developer.log(
        'trust-log entry store at ceiling; entries refused after assembly: $refused',
        name: 'e2e',
        level: 900,
      );
    }
    _detectSupersession(chains);
  }

  /// Only a device whose own pinned root is disabled looks for a successor,
  /// mirroring the node's detectSupersedingChain. Recomputed every sync
  /// ("observe, not trip") so a second re-lock names the newest root, not a stale
  /// one.
  void _detectSupersession(List<Uint8List> chains) {
    final trust = _trust;
    final own = trust?.genesisHash;
    if (trust == null || !trust.disabled || own == null) {
      _supersededBy = null;
      _supersedingChain = null;
      return;
    }
    final s = detectSupersession(chains, own);
    _supersededBy = s?.genesis;
    _supersedingChain = s?.liveChain;
  }

  /// Adopts the live successor surfaced by [supersededByGenesis], re-pinning the
  /// store to it. Returns false when no live successor is held. Persists the new
  /// anchor and re-evaluates channels.
  Future<bool> adoptSupersedingRoot() async {
    final chain = _supersedingChain;
    final trust = _trust;
    if (chain == null || trust == null) return false;
    await trust.reanchor(chain);
    _supersededBy = null;
    _supersedingChain = null;
    await onTrustChainAdvance?.call(chain);
    _reevaluateChannels();
    return true;
  }

  /// Closes channels to nodes no longer authorized by the current trust log.
  /// A nil/disabled/unlocked store closes nothing (disabled intentionally opens
  /// access). Only closes — never opens newly-authorized nodes. Also prunes tip
  /// state for each dropped node so its stale tip cannot accumulate misses and
  /// false-positive the equivocation flag.
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
      _loseNode(id);
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
      case 'project.list':
        return _fanoutProjects(params);
      case 'terminal.list':
        return _fanoutTerminals(params);
      case 'transcript.unsubscribe':
        final sub = stringField(params, 'sub_id');
        try {
          return await _routeByHandle(_subNode, sub, method, params);
        } finally {
          _subNode.remove(sub);
        }
    }
    if (terminalAddressed.contains(method) ||
        (method == 'terminal.open' && stringField(params, 'terminal_id') != null)) {
      return _routeByTerminal(method, params);
    }
    if (sessionAddressed.contains(method))
      return _routeBySession(method, params);
    if (workspaceAddressed.contains(method))
      return _routeByWorkspace(method, params);
    if (projectAddressed.contains(method))
      return _routeByProject(method, params);
    if (serverAddressed.contains(method)) {
      return routesToNode(params)
          ? _routeByNode(method, params)
          : _gateway.call(method, params);
    }
    if (nodeAddressed.contains(method)) return _routeByNode(method, params);
    if (terminalHandleAddressed.contains(method)) {
      return _routeByHandle(
        _termNode,
        stringField(params, 'term_id'),
        method,
        params,
      );
    }
    if (pushFanoutMethods.contains(method)) return _fanoutPush(method, params);
    return _gateway.call(method, params); // gateway-native passthrough
  }

  Future<Object?> _callNodeDecoded(
    String nodeId,
    String method,
    Object? params,
  ) async {
    var nc = _byNodeId[nodeId];
    final registering = _registering[nodeId];
    if (nc == null && registering != null) {
      await registering;
      nc = _byNodeId[nodeId];
    }
    if (nc == null) throw StateError('unknown node $nodeId');
    final result = nc.cbor && cborMethods.contains(method)
        ? await _callNodeCbor(nc, method, params)
        : await callNode(nc, method, utf8.encode(jsonEncode(jsonSafe(params))));
    if (result.isEmpty) return null;
    return jsonDecode(utf8.decode(result));
  }

  Future<List<dynamic>> _fanoutSessions(String method, Object? params) async {
    final entries = await _settledNodeIds();
    final results = await Future.wait(
      entries.map((nodeId) async {
        try {
          final r = await _callNodeDecoded(nodeId, method, params);
          return (nodeId, r is List ? r : const []);
        } catch (_) {
          return (nodeId, const <dynamic>[]);
        }
      }),
    );
    final merged = <dynamic>[];
    for (final (nodeId, list) in results) {
      final label = _roster[nodeId]?.label;
      for (final s in list) {
        if (s is Map<String, dynamic>)
          merged.add(withOriginJson(s, nodeId, label));
      }
    }
    return merged;
  }

  Future<List<dynamic>> _fanoutHistoryProjects(Object? params) async {
    final entries = await _settledNodeIds();
    final results = await Future.wait(
      entries.map((nodeId) async {
        try {
          final r = await _callNodeDecoded(
            nodeId,
            'sessions.historyProjects',
            params,
          );
          return (nodeId, r is List ? r : const []);
        } catch (_) {
          return (nodeId, const <dynamic>[]);
        }
      }),
    );
    final all = <Map<String, dynamic>>[];
    for (final (nodeId, list) in results) {
      final label = _roster[nodeId]?.label;
      for (final p in list) {
        if (p is Map<String, dynamic>)
          all.add({...p, 'node_id': nodeId, 'node_label': label});
      }
    }
    all.sort(
      (a, b) => (b['last_activity'] as String? ?? '').compareTo(
        a['last_activity'] as String? ?? '',
      ),
    );
    return all;
  }

  // Unlike the session fan-out, a failure on every node surfaces, so the drawer
  // can show an error instead of an empty tree. A partial failure lists the
  // failed nodes, so the caller can keep their last good projects.
  Future<Map<String, dynamic>> _fanoutProjects(Object? params) async {
    final entries = await _settledNodeIds();
    final results = await Future.wait(
      entries.map((nodeId) async {
        try {
          final r = await _callNodeDecoded(nodeId, 'project.list', params);
          final list = r is Map ? r['projects'] : null;
          return (nodeId, list is List ? list : const <dynamic>[], null as Object?);
        } catch (e) {
          return (nodeId, const <dynamic>[], e as Object?);
        }
      }),
    );
    final errors = [for (final r in results) if (r.$3 != null) r.$3!];
    if (entries.isNotEmpty && errors.length == entries.length) throw errors.first;
    final merged = <Map<String, dynamic>>[];
    for (final (nodeId, list, _) in results) {
      final label = _roster[nodeId]?.label;
      for (final p in list) {
        if (p is Map<String, dynamic>)
          merged.add(projectWithOriginJson(p, nodeId, label));
      }
    }
    return {
      'projects': merged,
      'failed_nodes': [for (final r in results) if (r.$3 != null) r.$1],
    };
  }

  Future<Object?> _routeByTerminal(String method, Object? params) async {
    final composite = stringField(params, 'terminal_id');
    if (composite == null) {
      throw RpcError(-32600, '$method requires terminal_id');
    }
    final (nodeId, localId, ok) = splitCompositeId(composite);
    if (!ok) {
      throw RpcError(-32600, 'terminal id is not gateway-qualified: $composite');
    }
    final result = await _callNodeDecoded(nodeId, method, {
      ...(params as Map).cast<String, dynamic>(),
      'terminal_id': localId,
    });
    if (method == 'terminal.open') {
      final term = stringField(params, 'term_id');
      if (term != null && term.isNotEmpty) _termNode[term] = nodeId;
    }
    return result;
  }

  // Ordered by node so the list does not reshuffle between loads.
  Future<Map<String, dynamic>> _fanoutTerminals(Object? params) async {
    final entries = await _settledNodeIds();
    final results = await Future.wait(
      entries.map((nodeId) async {
        try {
          final r = await _callNodeDecoded(nodeId, 'terminal.list', params);
          final list = r is Map ? r['terminals'] : null;
          return (nodeId, list is List ? list : const <dynamic>[], null as Object?);
        } catch (e) {
          // A node older than terminals answers method-not-found: it has none.
          final old = e is RpcError && e.isMethodNotFound;
          return (nodeId, const <dynamic>[], old ? null : e as Object?);
        }
      }),
    );
    final errors = [for (final r in results) if (r.$3 != null) r.$3!];
    if (entries.isNotEmpty && errors.length == entries.length) throw errors.first;
    final merged = <Map<String, dynamic>>[];
    for (final (nodeId, list, _) in results) {
      final label = _roster[nodeId]?.label;
      for (final t in list) {
        if (t is Map<String, dynamic>) merged.add(terminalWithOriginJson(t, nodeId, label));
      }
    }
    return {
      'terminals': sortByNode(merged, (t) => '${t['node_label'] ?? ''}\u0000${t['node_id']}'),
      'failed_nodes': [for (final r in results) if (r.$3 != null) r.$1],
    };
  }

  Future<Object?> _routeByWorkspace(String method, Object? params) async {
    final composite = stringField(params, 'workspace_id');
    if (composite == null) {
      throw RpcError(-32600, '$method requires workspace_id');
    }
    final (nodeId, localId, ok) = splitCompositeId(composite);
    if (!ok) {
      throw RpcError(-32600, 'workspace id is not gateway-qualified: $composite');
    }
    return _callNodeDecoded(nodeId, method, {
      ...(params as Map).cast<String, dynamic>(),
      'workspace_id': localId,
    });
  }

  Future<Object?> _routeByProject(String method, Object? params) async {
    final composite = stringField(params, 'project_id');
    if (composite == null) {
      throw RpcError(-32600, '$method requires project_id');
    }
    final (nodeId, localId, ok) = splitCompositeId(composite);
    if (!ok) {
      throw RpcError(-32600, 'project id is not gateway-qualified: $composite');
    }
    final res = await _callNodeDecoded(nodeId, method, {
      ...(params as Map).cast<String, dynamic>(),
      'project_id': localId,
    });
    if (method != 'workspace.create' || res is! Map) return res;
    final ws = res['workspace_id'];
    if (ws is! String || ws.isEmpty) return res;
    return {
      ...res.cast<String, dynamic>(),
      'workspace_id': compositeId(nodeId, ws),
    };
  }

  /// Fans out a push.register/unregister/test call to every connected node channel.
  /// Succeeds (at-least-one) if any node accepts. For push.test, returns
  /// [pushGoneCode] only when every node reported gone. If no nodes are connected,
  /// falls back to the gateway so a plain-RPC connection still works.
  Future<Object?> _fanoutPush(String method, Object? params) async {
    final nodeIds = await _settledNodeIds();
    if (nodeIds.isEmpty) return _gateway.call(method, params);
    final results = await Future.wait(
      nodeIds.map((nodeId) async {
        try {
          return (
            null as Object?,
            await _callNodeDecoded(nodeId, method, params),
          );
        } on Object catch (e) {
          return (e, null as Object?);
        }
      }),
    );
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
    // A subscription is recorded before the call so an unsubscribe sent while
    // the subscribe is in flight still reaches the node.
    final sub = method == 'transcript.subscribe'
        ? stringField(params, 'sub_id')
        : null;
    if (sub != null && sub.isNotEmpty) _subNode[sub] = nodeId;
    final Object? result;
    try {
      result = await _callNodeDecoded(
        nodeId,
        method,
        rewriteSessionId(params, localId),
      );
    } catch (_) {
      if (sub != null) _subNode.remove(sub);
      rethrow;
    }
    if (method == 'terminal.open') {
      final term = stringField(params, 'term_id');
      if (term != null && term.isNotEmpty) _termNode[term] = nodeId;
    }
    return result;
  }

  Future<Object?> _routeByNode(String method, Object? params) async {
    var nodeId = stringField(params, 'node_id') ?? '';
    if (nodeId.isEmpty) {
      await _channelsSettled();
      nodeId = _soleNode() ?? '';
      if (nodeId.isEmpty) throw RpcError(-32600, '$method requires node_id');
    }
    final result = await _callNodeDecoded(nodeId, method, params);
    if (method == 'terminal.create' && result is Map<String, dynamic>) {
      return terminalWithOriginJson(result, nodeId, _roster[nodeId]?.label);
    }
    if (compositeResultMethods.contains(method) &&
        result is Map<String, dynamic>) {
      final local = result['session_id'];
      if (local is String && local.isNotEmpty) {
        return {...result, 'session_id': compositeId(nodeId, local)};
      }
      return result;
    }
    if (method == 'sessions.historySessions' &&
        result is Map<String, dynamic>) {
      final items = result['items'];
      if (items is List) {
        final label = _roster[nodeId]?.label;
        return {
          ...result,
          'items': [
            for (final it in items)
              if (it is Map<String, dynamic>)
                {...it, 'node_id': nodeId, 'node_label': label}
              else
                it,
          ],
        };
      }
    }
    return result;
  }

  Future<Object?> _routeByHandle(
    Map<String, String> table,
    String? id,
    String method,
    Object? params,
  ) async {
    if (id == null || id.isEmpty) {
      throw RpcError(-32600, '$method requires a handle id');
    }
    final nodeId = table[id];
    if (nodeId == null) {
      throw RpcError(-32600, '$method: unknown handle $id');
    }
    return _callNodeDecoded(nodeId, method, params);
  }

  NodeDescriptor _parseNodeDescriptor(Map<String, dynamic> n) {
    final caps = n['capabilities'];
    final eps = n['p2p_endpoints'];
    return NodeDescriptor(
      id: n['id'] as String? ?? '',
      label: n['label'] as String?,
      identityPubKey: n['identity_pubkey'] as String? ?? '',
      online: n['online'] as bool? ?? true,
      lockDisabled: n['lock_disabled'] as bool? ?? false,
      p2p: caps is Map && caps['p2p'] == true,
      p2pEndpoints: eps is List
          ? [
              for (final e in eps)
                if (e is String) e,
            ]
          : const [],
    );
  }

  /// Decodes a node.identify result's `tip` field (base64) into raw bytes.
  /// Returns an empty list when the field is absent or malformed.
  Uint8List _decodeTip(Object? raw) {
    if (raw is String && raw.isNotEmpty) {
      try {
        return Uint8List.fromList(base64.decode(raw));
      } catch (_) {}
    }
    return Uint8List(0);
  }

  /// Handles a gateway-level notification. Filters for [node.event]:
  /// - type [trust-changed]: pulls the trust log promptly (mirrors Go's kick →
  ///   syncTrustLog nudge).
  /// - type [online] or [added]: adopts the node so a node that was offline at
  ///   connect, or that reconnected, gets a channel and its sessions appear.
  /// - type [offline] or [removed]: drops the node's channel and tip state so its
  ///   sessions stop appearing and its stale tip cannot accumulate misses.
  /// Mirrors Go E2EClient.onPeerNotify.
  void _onGatewayNotification(RpcMessage msg) {
    if (msg.method != 'node.event') return;
    final params = msg.params;
    if (params is! Map<String, dynamic>) return;
    final evType = params['type'];
    if (evType == 'trust-changed') {
      unawaited(_kickResync());
      return;
    }
    final nodeJson = params['node'];
    if (nodeJson is! Map<String, dynamic>) return;
    final desc = _parseNodeDescriptor(nodeJson);
    // Surface the roster change to the app so views bound to the node list (e.g.
    // the settings screen) refresh without waiting for a reconnect.
    if (evType == 'online' || evType == 'added') {
      // Off the read loop: adoptNode calls the gateway and awaits msg2, both of
      // which are answered on this very stream. Surface only once the channel is
      // open, so a reload on this event already reaches the new node.
      unawaited(_adoptNode(desc).whenComplete(() {
        final ctrl = _notificationsCtrl;
        if (ctrl != null && !ctrl.isClosed) ctrl.add(msg);
      }));
    } else if (evType == 'offline' || evType == 'removed') {
      _loseNode(desc.id);
      _notificationsCtrl?.add(msg);
    }
  }

  /// Re-reads each connected node's trust-log tip over its authenticated Noise
  /// channel (node.identify) and records it in [_authTip]. The tip is bound to the
  /// trust-log-authorized identity, unlike the forgeable gateway roster; it is the
  /// only source the equivocation check trusts. A node that fails to answer is
  /// logged and left on its previously-known tip. Mirrors Go E2EClient.refreshAuthTips.
  Future<void> _refreshAuthTips() async {
    final targets = <(String, String)>[]; // (nodeId, identity-pub hex)
    for (final nodeId in _byNodeId.keys) {
      final desc = _roster[nodeId];
      if (desc == null || desc.identityPubKey.isEmpty) continue;
      try {
        targets.add((nodeId, hexEncode(base64.decode(desc.identityPubKey))));
      } catch (_) {}
    }
    await Future.wait(
      targets.map((tg) async {
        try {
          final res = await _callNodeDecoded(tg.$1, 'node.identify', null);
          if (res is Map<String, dynamic>)
            _authTip[tg.$2] = _decodeTip(res['tip']);
        } catch (e) {
          developer.log(
            'node ${tg.$1} identify (tip refresh) failed: $e',
            name: 'e2e',
            level: 900,
          );
        }
      }),
    );
  }

  /// Cross-checks each connected node's authenticated tip against the current
  /// resolved trust-log chain. Mirrors Go E2EClient.checkTipConsistency:
  /// a tip not present in the client's linear chain history is tracked per-node;
  /// if the same unreconciled tip persists for [_tipMissThreshold] consecutive
  /// ticks, [_equivocation] is set. Tips for nodes that WERE connected (in
  /// [_everConnected]) but are no longer connected are skipped — a legitimate
  /// fork that orphans an offline node's cached tip must not accumulate misses
  /// and false-positive the flag. Nodes that reported a tip but were NEVER
  /// connected are still checked. No-op when the trust store is absent or the
  /// chain is empty.
  void _checkTipConsistency() {
    final trust = _trust;
    if (trust == null) return;
    final chainBytes = trust.chainBytes;
    if (chainBytes == null || chainBytes.isEmpty) return;
    if (_authTip.isEmpty)
      return; // no tips yet: skip the chain parse/hash entirely
    final tip = trust.tip;
    var known = _knownSet;
    if (known == null ||
        tip == null ||
        !bytesEqual(tip, _knownSetTip ?? const [])) {
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
      _knownSet = known;
      _knownSetTip = tip == null ? null : Uint8List.fromList(tip);
    }
    final connected = <String>{};
    for (final nodeId in _byNodeId.keys) {
      final desc = _roster[nodeId];
      if (desc == null || desc.identityPubKey.isEmpty) continue;
      try {
        connected.add(hexEncode(base64.decode(desc.identityPubKey)));
      } catch (_) {}
    }
    for (final entry in _authTip.entries) {
      final key = entry.key;
      final tipBytes = entry.value;
      if (_everConnected.contains(key) && !connected.contains(key)) continue;
      if (tipBytes.isEmpty) {
        _tipMiss.remove(key); // no tip yet: clear any prior miss
        continue;
      }
      if (known.contains(hexEncode(tipBytes))) {
        _tipMiss.remove(key); // tip reconciled: reset miss streak
        continue;
      }
      var ms = _tipMiss[key];
      if (ms == null || !bytesEqual(ms.tip, tipBytes)) {
        ms = _TipMissState(tip: Uint8List.fromList(tipBytes), misses: 1);
        _tipMiss[key] = ms;
      } else {
        ms.misses++;
      }
      if (ms.misses >= _tipMissThreshold && !_equivocation) {
        developer.log(
          'equivocation detected — node tip diverges from resolved chain: key=$key tip=${hexEncode(tipBytes)}',
          name: 'e2e',
          level: 900, // WARNING
        );
        _equivocation = true;
      }
    }
  }

  /// Opens a relay channel to [node]. A [standby] channel delivers no
  /// notifications until a switch makes it the node's link.
  Future<NodeChannel> openChannel(
    NodeDescriptor node, {
    bool standby = false,
  }) async {
    final res = await _gateway
        .call('relay.open', {'node_id': node.id})
        .timeout(handshakeTimeout);
    final chanId = (res as Map)['chan_id'] as String;
    if (_plaintext) {
      return _relayChannel(node.id, chanId, Channel.plain(chanId), standby);
    }
    final pub = base64.decode(node.identityPubKey);
    final (hs, msg1) = await HandshakeState.initiate(
      staticKey: _static,
      remoteStatic: pub,
      prologue: channelPrologue(node.id, chanId),
    );
    final hc = Completer<Uint8List>();
    _handshakes[chanId] = (link: _gatewayLink, c: hc);
    _send('${utf8.decode(marshalHandshakeFrame(chanId, msg1))}\n');
    final Uint8List msg2;
    try {
      msg2 = await hc.future.timeout(handshakeTimeout);
    } finally {
      _handshakes.remove(chanId);
    }
    return _relayChannel(
      node.id,
      chanId,
      Channel.noise(chanId, hs.finish(msg2)),
      standby,
    );
  }

  NodeChannel _relayChannel(
    String nodeId,
    String chanId,
    Channel channel,
    bool standby,
  ) {
    late final NodeChannel nc;
    nc = NodeChannel._(
      nodeId,
      chanId,
      channel,
      via: viaGateway,
      link: _gatewayLink,
      send: _send,
      sendBytes: _sendBytes,
      routeNotes: !standby,
      onClose: () {
        _unregister(nc);
        if (_closed) return;
        unawaited(
          _gateway
              .call('relay.close', {'chan_id': chanId})
              .catchError((Object _) => null),
        );
      },
    );
    _byChanId[chanId] = nc;
    return nc;
  }

  /// Removes [nc] from the maps and fails the calls waiting on it.
  void _unregister(NodeChannel nc) {
    if (_byChanId[nc.chanId] == nc) _byChanId.remove(nc.chanId);
    if (_byNodeId[nc.nodeId] == nc) _byNodeId.remove(nc.nodeId);
    _failPending(nc, StateError('channel to ${nc.nodeId} closed'));
  }

  void _failPending(NodeChannel nc, Object err) {
    final ids = [
      for (final e in _pending.entries)
        if (e.value.nc == nc) e.key,
    ];
    for (final id in ids) {
      final c = _pending.remove(id)!.c;
      if (!c.isCompleted) c.completeError(err);
    }
  }

  int _pendingOn(NodeChannel nc) =>
      _pending.values.where((p) => p.nc == nc).length;

  Duration callTimeoutFor(String method) =>
      method == 'server.upgrade' ? upgradeCallTimeout : callTimeout;

  Future<Uint8List> callNode(NodeChannel nc, String method, List<int> params) =>
      _call(method, nc, (id) {
        nc.send(nc.channel.sealRequestFrame(id, method, nc.nodeId, params));
      });

  Future<Uint8List> _callNodeCbor(
    NodeChannel nc,
    String method,
    Object? params,
  ) {
    final body = Uint8List.fromList(cbor.encode(params));
    return _call(method, nc, (id) {
      nc.sendCbor(nc.channel.sealRequestCbor(id, method, nc.nodeId, body));
    });
  }

  /// Registers a pending reply on [nc] for a fresh request id, then sends the
  /// sealed request via [send]; the reply resolves to the raw result JSON
  /// bytes. [inline] sees the reply before any later frame on the link.
  Future<Uint8List> _call(
    String method,
    NodeChannel nc,
    void Function(int id) send, {
    void Function(({Uint8List? result, RpcError? error}) r)? inline,
  }) {
    if (_closed) return Future.error(StateError('client closed'));
    if (nc.isDone) {
      return Future.error(StateError('channel to ${nc.nodeId} closed'));
    }
    final idn = ++_nextId;
    final id = idn.toString();
    final c = Completer<Uint8List>();
    _pending[id] = (c: c, nc: nc, inline: inline);
    send(idn);
    return c.future.timeout(
      callTimeoutFor(method),
      onTimeout: () {
        _pending.remove(id);
        throw TimeoutException('callNode $method timed out');
      },
    );
  }

  void _onMessage(RpcMessage m) {
    final route = m.route;
    if (route is Map && route['chan_id'] is String) {
      _onRelay(m, route['chan_id'] as String, _gatewayLink);
    } else {
      if (!_gatewayCtrl.isClosed) _gatewayCtrl.add(m);
    }
  }

  /// Handles a channel frame that arrived on [link]; a frame for a channel on
  /// another link is dropped.
  void _onRelay(RpcMessage m, String chanId, Object link) {
    if (m.method == methodE2EHandshake) {
      final h = _handshakes[chanId];
      if (h != null && h.link == link && !h.c.isCompleted) {
        try {
          h.c.complete(handshakeFromFrame(RelayFrame.fromMessage(m)));
        } catch (_) {
          /* malformed handshake: leave pending -> openChannel times out */
        }
      }
      return;
    }
    final nc = _byChanId[chanId];
    if (nc == null || nc._link != link || nc.isDone) return;
    final f = RelayFrame.fromMessage(m);
    if (m.id != null && m.method == null) {
      final p = _pending[m.id];
      if (p == null || p.nc != nc || p.c.isCompleted) {
        // A late reply (its call timed out) is opened and discarded, so the
        // channel's nonces stay in step.
        try {
          nc.channel.openResponse(f);
        } catch (_) {}
        return;
      }
      final ({Uint8List? result, RpcError? error}) r;
      try {
        r = nc.channel.openResponse(f);
      } catch (_) {
        // injected/garbage or desynced frame: drop it, keep the pending slot so
        // the genuine reply (which decrypts at the still-unadvanced nonce) resolves.
        return;
      }
      _pending.remove(m.id);
      p.inline?.call(r);
      if (r.error != null) {
        p.c.completeError(r.error!);
      } else {
        p.c.complete(r.result!); // non-null on the non-error path
      }
    } else if (m.method != null && m.id == null) {
      try {
        // Opened even when dropped, so the channel's nonces stay in step.
        final params = nc.channel.openParams(f);
        if (nc.routeNotes && !_events.isClosed) {
          _events.add((
            method: m.method!,
            params: params,
            nodeId: nc.nodeId,
            cbor: f.cbor,
          ));
        }
      } catch (_) {
        /* drop */
      }
    }
  }

  /// Ends every link. Each open node is reported as having none.
  void _closeLinks() {
    _wakeUpgrades();
    _upgrading.clear();
    final open = _byNodeId.keys.toList();
    final chans = {..._byNodeId.values, ..._byChanId.values};
    _byNodeId.clear();
    for (final id in open) {
      _emitPath(id, '');
    }
    for (final nc in chans) {
      nc.close();
    }
    if (!_paths.isClosed) _paths.close();
    if (!_reconciles.isClosed) _reconciles.close();
  }

  void _onDone() {
    if (_closed) return;
    _closed = true;
    _closeLinks();
    _gateway.close();
    if (!_gatewayCtrl.isClosed) _gatewayCtrl.close();
    _failAll(StateError('gateway link closed'));
    if (!_events.isClosed) _events.close();
  }

  void _failAll(Object e) {
    for (final c in [
      for (final h in _handshakes.values) h.c,
      for (final p in _pending.values) p.c,
    ]) {
      if (!c.isCompleted) c.completeError(e);
    }
    _handshakes.clear();
    _pending.clear();
  }

  @override
  Future<void> close() async {
    if (_closed) return;
    _closed = true;
    if (!_trustLoaded.isCompleted) _trustLoaded.complete();
    _resyncTimer?.cancel();
    _closeLinks();
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
