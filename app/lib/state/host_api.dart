import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/host_info.dart';
import '../transport/gateway_client.dart';
import 'gateway.dart';

class HostApi {
  HostApi(this._clientOf);

  final GatewayClient? Function() _clientOf;

  GatewayClient get _client =>
      _clientOf() ?? (throw StateError('not connected'));

  Future<HostInfo> info(String nodeId) async {
    final r = await _client.call('host.info', {'node_id': nodeId});
    return HostInfo.fromJson((r as Map).cast<String, dynamic>());
  }

  Future<HostWakelock> setWakelock(String nodeId, String until) async {
    final r = await _client.call('host.setWakelock', {
      'node_id': nodeId,
      'until': until,
    });
    return HostWakelock.fromJson((r as Map).cast<String, dynamic>());
  }
}

final hostApiProvider = Provider<HostApi>(
  (ref) => HostApi(() => ref.read(gatewayProvider)?.client),
);
