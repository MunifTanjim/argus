import 'dart:async';

import 'jsonrpc.dart';

/// The gateway RPC surface the app consumes, implemented by [RpcClient]
/// (cleartext) and by the E2E client (blind-relay), so the connection + state
/// layer is transport-agnostic.
abstract class GatewayClient {
  Future<Object?> call(String method, [Object? params]);
  Stream<RpcMessage> get notifications;
  FutureOr<void> close();
}
