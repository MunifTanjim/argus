part of 'e2e_client.dart';

/// The link a node's channel travels on.
const viaGateway = 'gateway';
const viaTcp = 'tcp';

/// Dials a direct endpoint (a ws/wss URL) within [timeout], following no
/// redirects.
typedef DirectDialer = Future<RpcLink> Function(String url, Duration timeout);

/// A node's link: [via] is `gateway` or `tcp`, or empty when the node lost its
/// link.
typedef NodePath = ({String nodeId, String via});

/// A node's sessions from `sessions.list` on its new link after a switch,
/// stamped with node origin. They replace the node's sessions in the view.
typedef NodeSessions = ({String nodeId, List<Map<String, dynamic>> sessions});

final _rng = Random.secure();

Uint8List _randomBytes(int n) =>
    Uint8List.fromList([for (var i = 0; i < n; i++) _rng.nextInt(256)]);

/// hex(SHA-256(token)), the value nodes know a client token by.
String hashToken(String token) =>
    hexEncode(SHA256Digest().process(Uint8List.fromList(utf8.encode(token))));

String _p2pHmac(String tokenHash, String msg) {
  final key = hexDecode(tokenHash);
  if (key.isEmpty) throw ArgumentError('token hash is not hex');
  final mac = HMac(SHA256Digest(), 64)..init(KeyParameter(key));
  return hexEncode(mac.process(Uint8List.fromList(utf8.encode(msg))));
}

/// The client's proof of a token hash in plaintext direct admission.
String p2pTag(String tokenHash, String nonce) =>
    _p2pHmac(tokenHash, 'client|$nonce');

/// The node's proof of the same hash, bound to its id and the channel.
String p2pMac(String tokenHash, String nodeId, String chanId, String nonce) =>
    _p2pHmac(tokenHash, 'node|$nodeId|$chanId|$nonce');

/// "p" and 16 lowercase hex digits.
String newDirectChanId() => 'p${hexEncode(_randomBytes(8))}';

bool _constantTimeEquals(String a, String b) {
  final x = utf8.encode(a), y = utf8.encode(b);
  if (x.length != y.length) return false;
  var d = 0;
  for (var i = 0; i < x.length; i++) {
    d |= x[i] ^ y[i];
  }
  return d == 0;
}

/// Calls [onSwitch] each time [nodeId]'s channel moves from one link to
/// another. A node losing its link is only recorded: the reconnect or the
/// node's return resyncs. Null when [client] has no direct links.
StreamSubscription<NodePath>? followNodeSwitches(
  Object client,
  String nodeId,
  void Function() onSwitch,
) {
  if (client is! E2EClient || nodeId.isEmpty) return null;
  var last = client.nodeVias[nodeId] ?? '';
  return client.nodePaths.listen((p) {
    if (p.nodeId != nodeId) return;
    final prev = last;
    last = p.via;
    if (prev.isNotEmpty && p.via.isNotEmpty && p.via != prev) onSwitch();
  });
}

/// One direct connection to a node: it carries a single channel's frames and
/// answers the node's direct requests. Keepalive closes it when a ping goes
/// unanswered.
class DirectLink {
  DirectLink(this._raw) {
    _sub = _raw.incoming.listen(
      _onMessage,
      onError: (Object _) => close(),
      onDone: close,
      cancelOnError: false,
    );
    _keepalive = Timer.periodic(E2EClient.p2pKeepaliveInterval, (_) => _ping());
  }

  final RpcLink _raw;
  late final StreamSubscription<RpcMessage> _sub;
  late final Timer _keepalive;

  /// Receives the channel frames, by chan id.
  void Function(RpcMessage m, String chanId)? onRelay;

  final _pending = <String, Completer<Object?>>{};
  final _done = Completer<void>();
  int _nextId = 0;
  int _fails = 0;
  bool _pinging = false;

  /// Completes once the link is closed, from either side.
  Future<void> get done => _done.future;
  bool get isClosed => _done.isCompleted;

  void sendLine(String line) {
    if (!isClosed) _raw.send(line);
  }

  void sendBytes(Uint8List framed) {
    if (!isClosed) _raw.sendBytes(framed);
  }

  /// A direct JSON-RPC request to the node (not on the channel).
  Future<Object?> call(String method, [Object? params, Duration? timeout]) {
    if (isClosed) return Future.error(StateError('direct link closed'));
    final id = 'd${++_nextId}';
    final c = Completer<Object?>();
    _pending[id] = c;
    _raw.send(encodeRequest(id, method, params));
    if (timeout == null) return c.future;
    return c.future.timeout(
      timeout,
      onTimeout: () {
        _pending.remove(id);
        throw TimeoutException('direct $method timed out');
      },
    );
  }

  void _onMessage(RpcMessage m) {
    final route = m.route;
    if (route is Map && route['chan_id'] is String) {
      onRelay?.call(m, route['chan_id'] as String);
    } else if (m.id != null && m.method != null) {
      _serve(m);
    } else if (m.isResponse) {
      final c = _pending.remove(m.id);
      if (c == null) return;
      if (m.error != null) {
        c.completeError(m.error!);
      } else {
        c.complete(m.result);
      }
    }
  }

  /// Answers ping; any other request (node.identify during plaintext
  /// admission) gets method-not-found at once, so the node does not wait.
  void _serve(RpcMessage m) {
    final id = int.tryParse(m.id!) ?? m.id;
    sendLine(
      '${jsonEncode({
        'jsonrpc': '2.0',
        'id': id,
        if (m.method == 'ping') 'result': null else 'error': {'code': -32601, 'message': 'method not found: ${m.method}'},
      })}\n',
    );
  }

  Future<void> _ping() async {
    if (_pinging || isClosed) return;
    _pinging = true;
    try {
      await call('ping', null, E2EClient.p2pKeepaliveTimeout);
      _fails = 0;
    } on RpcError {
      _fails = 0; // an error reply still proves the node is there
    } catch (_) {
      if (++_fails >= E2EClient.p2pKeepaliveFailures) close();
    } finally {
      _pinging = false;
    }
  }

  void close() {
    if (_done.isCompleted) return;
    _done.complete();
    _keepalive.cancel();
    unawaited(_sub.cancel());
    final err = StateError('direct link closed');
    for (final c in _pending.values) {
      if (!c.isCompleted) c.completeError(err);
    }
    _pending.clear();
    unawaited(_raw.close().catchError((Object _) {}));
  }
}

/// The background upgrade of relay nodes to direct links. Mirrors the Go
/// client's p2p.go.
extension _P2P on E2EClient {
  void _emitPath(String nodeId, String via) {
    if (!_paths.isClosed) _paths.add((nodeId: nodeId, via: via));
  }

  void _logOnce(String key, String message) {
    if (_p2pWarned.add(key)) {
      developer.log(message, name: 'e2e', level: 900);
    }
  }

  /// Runs [nodeId]'s upgrade loop unless one is running.
  void _startUpgrade(String nodeId) {
    if (_closed ||
        _directDialer == null ||
        _upgrading.containsKey(nodeId) ||
        _byNodeId[nodeId] == null) {
      return;
    }
    final run = Object();
    _upgrading[nodeId] = run;
    unawaited(_upgradeLoop(nodeId, run));
  }

  /// The node's descriptor while [run] is its upgrade and the node is open.
  NodeDescriptor? _upgradeTarget(String id, Object run) {
    if (_upgrading[id] != run) return null;
    if (_closed || _byNodeId[id] == null) {
      _upgrading.remove(id);
      return null;
    }
    return _roster[id];
  }

  /// Moves a relay node to a direct link, retrying every [p2pRetryInterval]
  /// (or at once on [retryUpgradesNow]) while it stays on the relay, and
  /// falls back to the relay when the direct link drops. Ends once the node
  /// is lost.
  Future<void> _upgradeLoop(String id, Object run) async {
    await _serverInfo;
    if (!_gatewayP2P) {
      _upgrading.remove(id);
      return;
    }
    while (true) {
      final desc = _upgradeTarget(id, run);
      if (desc == null) return;
      final direct = await _tryUpgrade(desc);
      if (direct != null && !await _holdDirect(desc, direct)) {
        if (_upgrading[id] == run) _upgrading.remove(id);
        return;
      }
      if (_upgradeTarget(id, run) == null) return;
      await _waitRetry(id);
    }
  }

  Future<void> _waitRetry(String id) async {
    final c = Completer<void>();
    _upgradeWake[id] = c;
    final t = Timer(E2EClient.p2pRetryInterval, () {
      if (!c.isCompleted) c.complete();
    });
    await c.future;
    t.cancel();
    if (_upgradeWake[id] == c) _upgradeWake.remove(id);
  }

  void _wakeUpgrades() {
    for (final c in _upgradeWake.values.toList()) {
      if (!c.isCompleted) c.complete();
    }
    _upgradeWake.clear();
  }

  Future<NodeChannel?> _tryUpgrade(NodeDescriptor desc) async {
    final direct = await dialTcp(desc);
    if (direct == null) return null;
    return await _swapLink(desc.id, direct) ? direct : null;
  }

  /// The direct endpoints of [desc] while it may be dialed: on the relay, the
  /// gateway and the node both p2p-capable, and a key (E2EE) or a token
  /// (plaintext) to be admitted with.
  List<String> _directEndpoints(NodeDescriptor desc) {
    final cur = _byNodeId[desc.id];
    if (_closed ||
        _directDialer == null ||
        !_gatewayP2P ||
        !desc.p2p ||
        cur == null ||
        cur.via != viaGateway) {
      return const [];
    }
    if (_plaintext ? (_p2pToken ?? '').isEmpty : desc.identityPubKey.isEmpty) {
      return const [];
    }
    return desc.p2pEndpoints;
  }

  /// Waits for [direct] to end. A remote drop falls back to the relay; a link
  /// closed by the client (roster, trust, close) does not. Reports whether
  /// the upgrade loop goes on.
  Future<bool> _holdDirect(NodeDescriptor desc, NodeChannel direct) async {
    await direct.done;
    if (_closed) return false;
    if (_byNodeId[desc.id] != direct) return true;
    try {
      await _fallBack(desc, direct);
    } catch (e) {
      developer.log(
        'node ${desc.id} direct link dropped and the relay failed: $e',
        name: 'e2e',
        level: 900,
      );
      // A roster event that landed meanwhile has already accounted for it.
      if (_byNodeId[desc.id] == direct) _loseNode(desc.id);
    }
    return true;
  }

  /// Opens a relay channel to [desc] and switches the node onto it from the
  /// dead link [direct].
  Future<void> _fallBack(NodeDescriptor desc, NodeChannel direct) async {
    final relay = await openChannel(desc, standby: true);
    relay.replaces = direct;
    final Object? res;
    try {
      res = await _identify(relay);
    } catch (_) {
      relay.close();
      rethrow;
    }
    relay.cbor = _gatewayCbor && E2EClient._hasCbor(res);
    _recordTip(desc, res);
    if (!await _swapLink(desc.id, relay)) {
      throw StateError('node ${desc.id} is no longer open');
    }
  }

  Future<Object?> _identify(NodeChannel nc) async {
    final r = await callNode(
      nc,
      'node.identify',
      utf8.encode(
        jsonEncode({
          'capabilities': {'cbor': true},
        }),
      ),
    );
    return r.isEmpty ? null : jsonDecode(utf8.decode(r));
  }

  void _recordTip(NodeDescriptor desc, Object? res) {
    if (res is! Map<String, dynamic> || desc.identityPubKey.isEmpty) return;
    try {
      _authTip[hexEncode(base64.decode(desc.identityPubKey))] = _decodeTip(
        res['tip'],
      );
    } catch (_) {}
  }

  Future<DirectLink> _connectDirect(String url) async {
    final dial = _directDialer!(url, handshakeTimeout);
    final RpcLink raw;
    try {
      raw = await dial.timeout(handshakeTimeout);
    } on TimeoutException {
      // A connection that completes after the deadline is closed.
      unawaited(dial.then((l) => l.close(), onError: (Object _) {}));
      rethrow;
    }
    final link = DirectLink(raw);
    link.onRelay = (m, chanId) => _onRelay(m, chanId, link);
    return link;
  }

  /// Registers a standby channel on [link]. Its close drops the connection
  /// and removes it from the client's maps; a remote drop fails its calls at
  /// once but leaves it registered for [_holdDirect] to replace.
  NodeChannel _directChannel(
    String nodeId,
    String chanId,
    Channel channel,
    DirectLink link,
  ) {
    late final NodeChannel nc;
    nc = NodeChannel._(
      nodeId,
      chanId,
      channel,
      via: viaTcp,
      link: link,
      send: link.sendLine,
      sendBytes: link.sendBytes,
      routeNotes: false,
      onClose: () {
        _unregister(nc);
        link.close();
      },
    );
    _byChanId[chanId] = nc;
    unawaited(
      link.done.then((_) {
        nc._end();
        _failPending(nc, StateError('direct link to $nodeId closed'));
      }),
    );
    return nc;
  }

  /// Connects to [url], runs the Noise handshake on a fresh direct channel and
  /// identifies the node. The node is not switched onto it.
  Future<NodeChannel> _dialEndpoint(NodeDescriptor desc, String url) async {
    final chanId = newDirectChanId();
    final (hs, msg1) = await HandshakeState.initiate(
      staticKey: _static,
      remoteStatic: base64.decode(desc.identityPubKey),
      prologue: channelPrologue(desc.id, chanId),
    );
    final link = await _connectDirect(url);
    NodeChannel? nc;
    try {
      if (_closed) throw StateError('client closed');
      final hc = Completer<Uint8List>();
      _handshakes[chanId] = (link: link, c: hc);
      link.sendLine('${utf8.decode(marshalHandshakeFrame(chanId, msg1))}\n');
      final Uint8List msg2;
      try {
        msg2 = await Future.any([
          hc.future,
          link.done.then<Uint8List>(
            (_) => throw StateError('direct link closed'),
          ),
        ]).timeout(handshakeTimeout);
      } finally {
        _handshakes.remove(chanId);
      }
      nc = _directChannel(
        desc.id,
        chanId,
        Channel.noise(chanId, hs.finish(msg2)),
        link,
      );
      final res = await _identify(nc);
      // No gateway on this link: the node's declaration alone decides CBOR.
      nc.cbor = E2EClient._hasCbor(res);
      _recordTip(desc, res);
      return nc;
    } catch (_) {
      nc?.close();
      link.close();
      rethrow;
    }
  }

  /// Connects to [url] and admits the client with its token, which is written
  /// only after the node proved it holds the token's hash. The hash itself is
  /// never sent. The node is not switched onto the new link.
  Future<NodeChannel> _dialPlainEndpoint(
    NodeDescriptor desc,
    String url,
  ) async {
    final token = _p2pToken!;
    final chanId = newDirectChanId();
    final nonce = base64.encode(_randomBytes(32));
    final hash = hashToken(token);
    final tag = p2pTag(hash, nonce);
    final want = p2pMac(hash, desc.id, chanId, nonce);
    final link = await _connectDirect(url);
    NodeChannel? nc;
    try {
      if (_closed) throw StateError('client closed');
      final chal = await link.call('token.challenge', {
        'chan_id': chanId,
        'nonce': nonce,
        'tag': tag,
      }, handshakeTimeout);
      final mac = chal is Map ? chal['mac'] : null;
      if (mac is! String || !_constantTimeEquals(mac, want)) {
        throw StateError('node did not prove the token');
      }
      await link.call('token.present', {
        'chan_id': chanId,
        'token': token,
      }, handshakeTimeout);
      if (_closed) throw StateError('client closed');
      nc = _directChannel(desc.id, chanId, Channel.plain(chanId), link);
      // The node creates its side of the channel on the first channel frame.
      final res = await _identify(nc);
      nc.cbor = E2EClient._hasCbor(res);
      return nc;
    } catch (_) {
      nc?.close();
      link.close();
      rethrow;
    }
  }

  /// Makes [next] the node's link, only while [next.replaces] still is: a
  /// node lost and reopened meanwhile keeps its new channel. The old link
  /// stops delivering notifications at once (replies to its pending calls
  /// still route) and closes once those settle or [switchSettleTimeout]
  /// passes. The node's sessions are reconciled from sessions.list on [next]
  /// before the path change is reported.
  Future<bool> _swapLink(String nodeId, NodeChannel next) async {
    final old = _byNodeId[nodeId];
    if (_closed || old == null || old != next.replaces) {
      next.close();
      return false;
    }
    _byNodeId[nodeId] = next;
    next.replaces = null; // retired links must not stay reachable
    old.routeNotes = false;
    unawaited(_retire(old));

    // The reply is handled before any later frame on the link: notifications
    // before it are covered by the list and stay dropped, later ones are
    // delivered.
    try {
      await _call(
        'sessions.list',
        next,
        (id) => next.send(
          next.channel.sealRequestFrame(
            id,
            'sessions.list',
            nodeId,
            utf8.encode('null'),
          ),
        ),
        inline: (r) {
          next.routeNotes = true;
          final result = r.result;
          if (result != null) _reconcileSessions(nodeId, result);
        },
      );
    } catch (e) {
      next.routeNotes = true;
      developer.log(
        'node $nodeId: sessions.list after switch failed: $e',
        name: 'e2e',
        level: 900,
      );
    }
    // A node lost during the list has already been reported with an empty via.
    // A link that dropped meanwhile is never reported: its fallback is.
    if (_byNodeId[nodeId] == next && !next.isDone) {
      _emitPath(nodeId, next.via);
    }
    return true;
  }

  void _reconcileSessions(String nodeId, Uint8List result) {
    final Object? list;
    try {
      list = result.isEmpty ? const [] : jsonDecode(utf8.decode(result));
    } catch (_) {
      return;
    }
    if (list is! List || _reconciles.isClosed) return;
    final label = _roster[nodeId]?.label;
    _reconciles.add((
      nodeId: nodeId,
      sessions: [
        for (final s in list)
          if (s is Map<String, dynamic>) withOriginJson(s, nodeId, label),
      ],
    ));
  }

  /// Closes a replaced link once its pending calls settle, or after
  /// [switchSettleTimeout].
  Future<void> _retire(NodeChannel nc) async {
    final sw = Stopwatch()..start();
    while (!nc.isDone &&
        _pendingOn(nc) > 0 &&
        sw.elapsed < E2EClient.switchSettleTimeout) {
      await Future<void>.delayed(const Duration(milliseconds: 20));
    }
    nc.close();
  }
}
