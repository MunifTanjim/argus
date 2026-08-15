import 'dart:async';

import 'jsonrpc.dart';

/// The gateway RPC surface the app consumes: request/response, a notification
/// stream, and teardown. Implemented by [RpcClient] (cleartext) and by the E2E
/// client (blind-relay). Lets the connection + state layer be transport-agnostic.
abstract class GatewayClient {
  Future<Object?> call(String method, [Object? params]);
  Stream<RpcMessage> get notifications;
  FutureOr<void> close();
}
