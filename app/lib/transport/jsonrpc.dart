import 'dart:convert';

class RpcError {
  final int code;
  final String message;
  const RpcError(this.code, this.message);

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

  const RpcMessage({this.id, this.method, this.params, this.result, this.error});

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
    );
  }

  bool get isNotification => method != null && id == null;
  bool get isResponse => id != null && method == null;
}

String encodeRequest(String id, String method, [Object? params]) {
  final m = <String, Object?>{'jsonrpc': '2.0', 'id': id, 'method': method};
  if (params != null) m['params'] = params;
  return '${jsonEncode(m)}\n';
}
