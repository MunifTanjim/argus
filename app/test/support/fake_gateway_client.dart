import 'dart:async';

import 'package:argus/transport/gateway_client.dart';
import 'package:argus/transport/jsonrpc.dart';

class FakeGatewayClient implements GatewayClient {
  FakeGatewayClient(this.handler);

  final Future<Object?> Function(String method, Object? params) handler;
  final calls = <(String, Object?)>[];
  final _notifications = StreamController<RpcMessage>.broadcast();

  void notify(String method, [Object? params]) =>
      _notifications.add(RpcMessage(method: method, params: params));

  @override
  Future<Object?> call(String method, [Object? params]) {
    calls.add((method, params));
    return handler(method, params);
  }

  @override
  Stream<RpcMessage> get notifications => _notifications.stream;

  @override
  FutureOr<void> close() => _notifications.close();
}
