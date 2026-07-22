import 'dart:convert';
import 'dart:typed_data';

import '../transport/jsonrpc.dart' show RpcError, RpcMessage;
import 'session.dart';

/// Method for a relay frame carrying a raw Noise handshake message (msg1/msg2),
/// before any sealed app traffic.
const String methodE2EHandshake = 'e2e.handshake';

/// The Noise prologue binding a channel to its node id and chan id. Client and
/// node MUST derive it identically.
Uint8List channelPrologue(String nodeId, String chanId) =>
    Uint8List.fromList(utf8.encode('argus-e2e/v1|$nodeId|$chanId'));

/// Cleartext routing metadata a blind gateway reads to relay a frame.
class RouteHeader {
  const RouteHeader({required this.chanId, this.nodeId, this.subId, this.termId});

  final String chanId;
  final String? nodeId;
  final String? subId;
  final String? termId;

  static RouteHeader? fromJson(Object? j) {
    if (j is! Map) return null;
    final chan = j['chan_id'];
    if (chan is! String) return null;
    return RouteHeader(
      chanId: chan,
      nodeId: j['node_id'] as String?,
      subId: j['sub_id'] as String?,
      termId: j['term_id'] as String?,
    );
  }
}

/// A parsed relay frame. [body] is the JSON body value (a base64 String for
/// sealed or handshake frames); [raw] is the verbatim line.
class RelayFrame {
  RelayFrame({this.method, this.id, this.route, this.body, required this.raw});

  final String? method;
  final Object? id;
  final RouteHeader? route;
  final Object? body;
  final Uint8List raw;

  static RelayFrame parse(List<int> line) {
    final data = line is Uint8List ? line : Uint8List.fromList(line);
    final j = jsonDecode(utf8.decode(data)) as Map<String, dynamic>;
    return RelayFrame(
      method: j['method'] as String?,
      id: j['id'],
      route: RouteHeader.fromJson(j['route']),
      body: j['body'],
      raw: data,
    );
  }

  static RelayFrame fromMessage(RpcMessage m) => RelayFrame(
        method: m.method,
        id: m.id,
        route: RouteHeader.fromJson(m.route),
        body: m.body,
        raw: Uint8List(0),
      );
}

/// Builds a relay frame carrying a raw (unsealed) Noise handshake message.
Uint8List marshalHandshakeFrame(String chanId, List<int> handshake) {
  final frame = <String, Object?>{
    'jsonrpc': '2.0',
    'method': methodE2EHandshake,
    'route': {'chan_id': chanId},
    'body': base64.encode(handshake),
  };
  return Uint8List.fromList(utf8.encode(jsonEncode(frame)));
}

/// Extracts the raw Noise handshake bytes from a handshake frame.
Uint8List handshakeFromFrame(RelayFrame f) {
  final b = f.body;
  if (b is! String) {
    throw const FormatException('handshake frame body is not a string');
  }
  return Uint8List.fromList(base64.decode(b));
}

/// Seals JSON-RPC payloads for one client<->node E2E channel. The routing header
/// travels in cleartext so a blind gateway can relay by chan_id; the inner
/// params/result/error travel sealed in the body.
class Channel {
  Channel(this.chanId, this._session);

  final String chanId;
  final Session _session;

  String _sealBody(List<int> inner) => base64.encode(_session.seal(inner));

  Uint8List _openBody(Object? body) {
    if (body is! String) {
      throw const FormatException('channel body is not a string');
    }
    return _session.open(base64.decode(body));
  }

  /// Seals a request into wire frame bytes. [params] is the raw params JSON.
  Uint8List sealRequestFrame(int id, String method, String nodeId, List<int> params) {
    final route = <String, Object?>{'chan_id': chanId};
    if (nodeId.isNotEmpty) route['node_id'] = nodeId;
    final frame = <String, Object?>{
      'jsonrpc': '2.0',
      'id': id,
      'method': method,
      'route': route,
      'body': _sealBody(params),
    };
    return Uint8List.fromList(utf8.encode(jsonEncode(frame)));
  }

  /// Decrypts a response frame's body into its raw result JSON bytes or rpc error.
  /// Relies on Go's canonical sealedResponse marshaling: `{"result":<raw>}` or
  /// `{"error":<raw>}` (no spaces, exactly one key). Returns raw bytes so int64
  /// results survive on the web (no JSON number decode).
  ({Uint8List? result, RpcError? error}) openResponse(RelayFrame f) {
    final inner = _openBody(f.body);
    if (_hasPrefix(inner, '{"error":')) {
      final m = jsonDecode(utf8.decode(inner)) as Map<String, dynamic>;
      return (result: null, error: RpcError.fromJson(m['error'] as Map<String, dynamic>));
    }
    const p = '{"result":';
    if (_hasPrefix(inner, p) && inner.last == 0x7d /* } */) {
      return (result: Uint8List.sublistView(inner, p.length, inner.length - 1), error: null);
    }
    throw const FormatException('unexpected sealed response shape');
  }

  static bool _hasPrefix(Uint8List b, String p) {
    if (b.length < p.length) return false;
    for (var i = 0; i < p.length; i++) {
      if (b[i] != p.codeUnitAt(i)) return false;
    }
    return true;
  }

  /// Decrypts a request/notification frame's body into its raw params JSON bytes.
  Uint8List openParams(RelayFrame f) => _openBody(f.body);
}
