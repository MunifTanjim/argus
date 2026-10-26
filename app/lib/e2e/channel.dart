import 'dart:convert';
import 'dart:typed_data';

import 'package:cbor/simple.dart';

import '../transport/frame_reader.dart' show frameCbor;
import '../transport/jsonrpc.dart'
    show RpcError, RpcMessage, cborToDart, jsonSafe;
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
  const RouteHeader({
    required this.chanId,
    this.nodeId,
    this.subId,
    this.termId,
  });

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
/// sealed or handshake frames) or the sealed bytes of a CBOR frame; [raw] is
/// the verbatim line.
class RelayFrame {
  RelayFrame({
    this.method,
    this.id,
    this.route,
    this.body,
    required this.raw,
    this.cbor = false,
  });

  final String? method;
  final Object? id;
  final RouteHeader? route;
  final Object? body;
  final Uint8List raw;
  final bool cbor;

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
    cbor: m.cbor,
  );
}

Uint8List marshalHandshakeFrame(String chanId, List<int> handshake) {
  final frame = <String, Object?>{
    'jsonrpc': '2.0',
    'method': methodE2EHandshake,
    'route': {'chan_id': chanId},
    'body': base64.encode(handshake),
  };
  return Uint8List.fromList(utf8.encode(jsonEncode(frame)));
}

Uint8List handshakeFromFrame(RelayFrame f) {
  final b = f.body;
  if (b is! String) {
    throw const FormatException('handshake frame body is not a string');
  }
  return Uint8List.fromList(base64.decode(b));
}

abstract class ChannelCipher {
  Uint8List seal(List<int> plaintext);
  Uint8List open(List<int> sealed);
}

class _NoiseCipher implements ChannelCipher {
  _NoiseCipher(this._s);
  final Session _s;
  @override
  Uint8List seal(List<int> p) => _s.seal(p);
  @override
  Uint8List open(List<int> b) => _s.open(b);
}

class _IdentityCipher implements ChannelCipher {
  @override
  Uint8List seal(List<int> p) => Uint8List.fromList(p);
  @override
  Uint8List open(List<int> b) => Uint8List.fromList(b);
}

/// Seals JSON-RPC payloads for one client<->node E2E channel. The routing header
/// travels in cleartext so a blind gateway can relay by chan_id; the inner
/// params/result/error travel sealed in the body.
class Channel {
  Channel(this.chanId, this._cipher);
  Channel.noise(String chanId, Session s) : this(chanId, _NoiseCipher(s));
  Channel.plain(String chanId) : this(chanId, _IdentityCipher());

  final String chanId;
  final ChannelCipher _cipher;

  String _sealBody(List<int> inner) => base64.encode(_cipher.seal(inner));

  Uint8List _openBody(Object? body) {
    if (body is List<int>) return _cipher.open(body);
    if (body is! String) {
      throw const FormatException('channel body is not a string');
    }
    return _cipher.open(base64.decode(body));
  }

  /// Seals a request into wire frame bytes. [params] is the raw params JSON.
  Uint8List sealRequestFrame(
    int id,
    String method,
    String nodeId,
    List<int> params,
  ) {
    final frame = _requestEnvelope(id, method, nodeId, _sealBody(params));
    return Uint8List.fromList(utf8.encode(jsonEncode(frame)));
  }

  /// Seals a request into a framed CBOR frame. [params] is the CBOR-encoded params.
  Uint8List sealRequestCbor(
    int id,
    String method,
    String nodeId,
    Uint8List params,
  ) {
    final frame = _requestEnvelope(id, method, nodeId, _cipher.seal(params));
    return frameCbor(Uint8List.fromList(cbor.encode(frame)));
  }

  Map<String, Object?> _requestEnvelope(
    int id,
    String method,
    String nodeId,
    Object body,
  ) {
    final route = <String, Object?>{'chan_id': chanId};
    if (nodeId.isNotEmpty) route['node_id'] = nodeId;
    return {
      'jsonrpc': '2.0',
      'id': id,
      'method': method,
      'route': route,
      'body': body,
    };
  }

  /// Decrypts a response frame's body into its raw result JSON bytes or rpc error.
  /// A CBOR frame's result is converted to JSON, byte strings becoming base64.
  /// Relies on Go's canonical sealedResponse marshaling: `{"result":<raw>}` or
  /// `{"error":<raw>}` (no spaces, exactly one key). Returns raw bytes so int64
  /// results survive on the web (no JSON number decode).
  ({Uint8List? result, RpcError? error}) openResponse(RelayFrame f) {
    final inner = _openBody(f.body);
    if (f.cbor) return _openCborResponse(inner);
    if (_hasPrefix(inner, '{"error":')) {
      final m = jsonDecode(utf8.decode(inner)) as Map<String, dynamic>;
      return (
        result: null,
        error: RpcError.fromJson(m['error'] as Map<String, dynamic>),
      );
    }
    const p = '{"result":';
    if (_hasPrefix(inner, p) && inner.last == 0x7d /* } */ ) {
      return (
        result: Uint8List.sublistView(inner, p.length, inner.length - 1),
        error: null,
      );
    }
    throw const FormatException('unexpected sealed response shape');
  }

  ({Uint8List? result, RpcError? error}) _openCborResponse(Uint8List inner) {
    final m = cborToDart(cbor.decode(inner));
    if (m is! Map<String, dynamic>) {
      throw const FormatException('unexpected sealed response shape');
    }
    final err = m['error'];
    if (err is Map<String, dynamic>) {
      return (result: null, error: RpcError.fromJson(err));
    }
    return (
      result: utf8.encode(jsonEncode(jsonSafe(m['result']))),
      error: null,
    );
  }

  static bool _hasPrefix(Uint8List b, String p) {
    if (b.length < p.length) return false;
    for (var i = 0; i < p.length; i++) {
      if (b[i] != p.codeUnitAt(i)) return false;
    }
    return true;
  }

  /// The inner params bytes, encoded as the frame is (JSON or CBOR).
  Uint8List openParams(RelayFrame f) => _openBody(f.body);
}
