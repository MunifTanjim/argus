import 'push_provider.dart';
import '../transport/rpc_client.dart';

/// Registers [target] for [deviceId] with the gateway over [client], keyed by the
/// stable device id so re-registration replaces the prior endpoint. Retries a few
/// times before giving up so a transient RPC failure on connect doesn't leave the
/// device unregistered (the previous code swallowed the error and never retried).
///
/// Returns true once the gateway acknowledges, false if every attempt failed.
Future<bool> registerWithRetry(
  RpcClient client,
  String deviceId,
  PushTarget target, {
  int attempts = 3,
  Duration delay = const Duration(seconds: 2),
}) async {
  for (var i = 0; i < attempts; i++) {
    try {
      await client.call('push.register', {'device_id': deviceId, ...target.toParams()});
      return true;
    } catch (_) {
      if (i == attempts - 1) return false;
      await Future<void>.delayed(delay);
    }
  }
  return false;
}

/// Best-effort: tell the gateway to forget this device's push target. Errors are
/// swallowed — the connection may already be closing, and an offline gateway's
/// record is pruned server-side on the next failed send.
Future<void> unregisterFromGateway(RpcClient client, String deviceId) async {
  try {
    await client.call('push.unregister', {'device_id': deviceId});
  } catch (_) {}
}
