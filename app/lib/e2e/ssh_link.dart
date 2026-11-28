part of 'e2e_client.dart';

/// Opens a node's SSH link: runs [command] on [target] and returns its stdio,
/// within [timeout].
typedef SshOpener =
    Future<NodeBridge> Function(
      SshNodeTarget target,
      String command,
      Duration timeout,
    );

/// Carries one channel over a node's local-socket protocol (plain JSON-RPC,
/// no route, body or Noise), so the channel machinery runs unchanged on it:
/// channel frames written by the client become direct requests, and the
/// node's replies and notifications come back as channel frames on [_chanId].
/// Frames without a route (the link's own pings, replies to node requests)
/// pass through, as do replies to them (`d`-prefixed ids).
class _BridgeRpcLink implements RpcLink {
  _BridgeRpcLink(this._bridge, this._chanId) {
    _sub = _bridge.stream.listen(
      _onBytes,
      onError: (Object _) => close(),
      onDone: close,
      cancelOnError: false,
    );
  }

  final NodeBridge _bridge;
  final String _chanId;
  final _reader = FrameReader();
  final _ctrl = StreamController<RpcMessage>.broadcast();
  late final StreamSubscription<Uint8List> _sub;
  bool _closed = false;

  @override
  Stream<RpcMessage> get incoming => _ctrl.stream;

  void _onBytes(Uint8List chunk) {
    final List<Object> frames;
    try {
      frames = _reader.add(chunk);
    } catch (_) {
      unawaited(close());
      return;
    }
    for (final f in frames) {
      // CBOR is not negotiated on this link.
      if (f is JsonFrame) _onLine(f.line);
    }
  }

  void _onLine(String line) {
    final Map<String, dynamic> j;
    try {
      j = jsonDecode(line) as Map<String, dynamic>;
    } catch (_) {
      return;
    }
    final id = j['id'];
    final method = j['method'];
    final route = {'chan_id': _chanId};
    RpcMessage m;
    if (method is String && id == null) {
      m = RpcMessage(
        method: method,
        route: route,
        body: base64.encode(utf8.encode(jsonEncode(j['params']))),
      );
    } else if (method == null &&
        id != null &&
        !(id is String && id.startsWith('d'))) {
      final inner = j['error'] != null
          ? '{"error":${jsonEncode(j['error'])}}'
          : '{"result":${jsonEncode(j['result'])}}';
      m = RpcMessage(
        id: id.toString(),
        route: route,
        body: base64.encode(utf8.encode(inner)),
      );
    } else {
      try {
        m = RpcMessage.fromJson(j);
      } catch (_) {
        return;
      }
    }
    if (!_ctrl.isClosed) _ctrl.add(m);
  }

  @override
  void send(String frame) {
    if (_closed) return;
    for (final line in frame.split('\n')) {
      if (line.trim().isEmpty) continue;
      final j = jsonDecode(line) as Map<String, dynamic>;
      final body = j['body'];
      final String out;
      if (j['route'] is Map && body is String) {
        final params = utf8.decode(base64.decode(body));
        out =
            '{"jsonrpc":"2.0","id":${jsonEncode(j['id'])},'
            '"method":${jsonEncode(j['method'])},"params":$params}';
      } else {
        out = line;
      }
      _bridge.add(utf8.encode('$out\n'));
    }
  }

  @override
  void sendBytes(Uint8List frame) {}

  @override
  Future<void> close() async {
    if (_closed) return;
    _closed = true;
    unawaited(_sub.cancel());
    if (!_ctrl.isClosed) unawaited(_ctrl.close());
    await _bridge.close();
  }
}

/// Node SSH links. Mirrors the Go client's sshlink.go.
extension _Ssh on E2EClient {
  bool get _sshEnabled => _sshOpener != null && _sshTargets.isNotEmpty;

  /// The SSH destination configured for [desc], by id then by label, both
  /// case-insensitive. A label several open nodes share is skipped.
  String? _sshTargetFor(NodeDescriptor desc) {
    if (!_sshEnabled) return null;
    final id = desc.id.toLowerCase();
    for (final e in _sshTargets.entries) {
      if (e.key.toLowerCase() == id) return e.value;
    }
    final label = desc.label?.toLowerCase() ?? '';
    if (label.isEmpty) return null;
    for (final e in _sshTargets.entries) {
      if (e.key.toLowerCase() != label) continue;
      final shared = _roster.values
          .where((d) => d.label?.toLowerCase() == label)
          .length;
      if (shared > 1) {
        _logOnce(
          'ssh-label\x00${e.key}',
          'ssh destination "${e.key}" matches the label of several nodes; skipping it',
        );
        return null;
      }
      return e.value;
    }
    return null;
  }

  /// Dials [desc] over its SSH destination and switches the node onto the new
  /// link. Null when the node stays where it is.
  Future<NodeChannel?> _trySsh(NodeDescriptor desc) async {
    final target = _sshTargetFor(desc);
    if (target == null) return null;
    final NodeChannel nc;
    try {
      nc = await _dialSsh(desc, target);
    } catch (e) {
      _logOnce(
        '${desc.id}\x00ssh\x00$e',
        'node ${desc.id} ssh link unavailable: $e',
      );
      return null;
    }
    return await _swapLink(desc.id, nc) ? nc : null;
  }

  /// Runs the bridge command on [target] and identifies the node over its
  /// local-socket protocol. The SSH host key authenticates the node; identify
  /// only checks it is the node expected. Trust re-checks and tip state stay
  /// keyed by the identity key the relay link was opened with. The node is
  /// not switched onto the link.
  Future<NodeChannel> _dialSsh(NodeDescriptor desc, String target) async {
    final t = parseSshNodeTarget(target);
    final cur = _byNodeId[desc.id];
    // SSH is preferred: it replaces a relay or a tcp link.
    if (_closed || cur == null || cur.via == viaSsh) {
      throw StateError('node is not on the relay or tcp');
    }
    final dial = _sshOpener!(t, _sshCommand, handshakeTimeout);
    final NodeBridge bridge;
    try {
      bridge = await dial.timeout(handshakeTimeout);
    } on TimeoutException {
      // A connection that completes after the deadline is closed.
      unawaited(dial.then((b) => b.close(), onError: (Object _) {}));
      rethrow;
    }
    final chanId = 's${hexEncode(_randomBytes(8))}';
    final link = DirectLink(_BridgeRpcLink(bridge, chanId));
    link.onRelay = (m, c) => _onRelay(m, c, link);
    NodeChannel? nc;
    try {
      if (_closed) throw StateError('client closed');
      nc = _directChannel(
        desc.id,
        chanId,
        Channel.plain(chanId),
        link,
        via: viaSsh,
      );
      // No cbor declared: the link carries JSON only.
      final r = await callNode(
        nc,
        'node.identify',
        utf8.encode('{}'),
      ).timeout(handshakeTimeout);
      final res = r.isEmpty ? null : jsonDecode(utf8.decode(r));
      final gotId = res is Map ? res['id'] : null;
      if (gotId != desc.id) {
        throw StateError('ssh target is node "$gotId", not "${desc.id}"');
      }
      if (!_plaintext &&
          (res as Map)['identity_pubkey'] != desc.identityPubKey) {
        throw StateError("ssh target's identity key does not match the roster");
      }
      if (!_plaintext) _recordTip(desc, res);
      nc.replaces = cur;
      return nc;
    } catch (_) {
      nc?.close();
      link.close();
      rethrow;
    }
  }
}
