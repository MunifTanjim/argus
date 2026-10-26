import 'dart:convert';
import 'dart:typed_data';

class RpcError {
  final int code;
  final String message;
  const RpcError(this.code, this.message);

  bool get isMethodNotFound => code == -32601;

  factory RpcError.fromJson(Map<String, dynamic> j) =>
      RpcError((j['code'] as num).toInt(), j['message'] as String? ?? '');

  @override
  String toString() => 'RpcError($code, $message)';
}

class RpcMessage {
  final String? id;
  final String? method;
  final Object? params;
  final Object? result;
  final RpcError? error;
  final Object? route; // raw relay routing header (a Map with chan_id) on relayed links
  final Object? body;  // raw sealed body: a base64 String (JSON) or Uint8List (CBOR) on relayed links
  final bool cbor; // arrived as a CBOR frame

  const RpcMessage(
      {this.id,
      this.method,
      this.params,
      this.result,
      this.error,
      this.route,
      this.body,
      this.cbor = false});

  factory RpcMessage.fromJson(Map<String, dynamic> j) {
    final rawId = j['id'];
    return RpcMessage(
      id: rawId?.toString(),
      method: j['method'] as String?,
      params: j['params'],
      result: j['result'],
      error: j['error'] == null
          ? null
          : RpcError.fromJson(j['error'] as Map<String, dynamic>),
      route: j['route'],
      body: j['body'],
    );
  }

  factory RpcMessage.fromCbor(Map m) {
    final j = cborToDart(m) as Map<String, dynamic>;
    return RpcMessage(
      id: j['id']?.toString(),
      method: j['method'] as String?,
      params: j['params'],
      result: j['result'],
      error: j['error'] == null
          ? null
          : RpcError.fromJson(j['error'] as Map<String, dynamic>),
      route: j['route'],
      body: j['body'],
      cbor: true,
    );
  }

  bool get isNotification => method != null && id == null;
  bool get isResponse => id != null && method == null;
}

/// Converts a decoded CBOR value into the shapes JSON decoding yields: string
/// keyed maps and dynamic lists. Byte strings become [Uint8List].
Object? cborToDart(Object? v) {
  if (v is List<int>) return v is Uint8List ? v : Uint8List.fromList(v);
  if (v is Map) {
    return <String, dynamic>{
      for (final e in v.entries) e.key.toString(): cborToDart(e.value),
    };
  }
  if (v is List) return <dynamic>[for (final e in v) cborToDart(e)];
  return v;
}

/// Replaces [Uint8List] values (at any depth) with base64 strings, the JSON
/// encoding of bytes; jsonEncode would otherwise write them as int arrays.
Object? jsonSafe(Object? v) {
  if (v is Uint8List) return base64.encode(v);
  if (v is Map) return {for (final e in v.entries) e.key: jsonSafe(e.value)};
  if (v is List) return [for (final e in v) jsonSafe(e)];
  return v;
}

String encodeRequest(String id, String method, [Object? params]) {
  final m = <String, Object?>{'jsonrpc': '2.0', 'id': id, 'method': method};
  if (params != null) m['params'] = jsonSafe(params);
  return '${jsonEncode(m)}\n';
}
