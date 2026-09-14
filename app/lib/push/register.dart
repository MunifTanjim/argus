import 'push_provider.dart';
import '../transport/gateway_client.dart';

/// Registers [target] for [deviceId], keyed by the stable device id so
/// re-registration replaces the prior endpoint. Retries a few times so a
/// transient RPC failure on connect doesn't leave the device unregistered.
///
/// [pausedUntil] carries the device's current pause preference so the node applies
/// it atomically at register time (no unpaused window on a first connect to a new
/// gateway). Empty or null means enabled.
///
/// Returns true once the gateway acknowledges, false if every attempt failed.
Future<bool> registerWithRetry(
  GatewayClient client,
  String deviceId,
  PushTarget target, {
  String? pausedUntil,
  int attempts = 3,
  Duration delay = const Duration(seconds: 2),
}) async {
  for (var i = 0; i < attempts; i++) {
    try {
      await client.call('push.register', {
        'device_id': deviceId,
        ...target.toParams(),
        if (pausedUntil != null && pausedUntil.isNotEmpty) 'paused_until': pausedUntil,
      });
      return true;
    } catch (_) {
      if (i == attempts - 1) return false;
      await Future<void>.delayed(delay);
    }
  }
  return false;
}

/// Sets this device's pause state on the gateway without re-registering. An empty
/// [pausedUntil] re-enables the device. Fans out to every node via the aggregate.
Future<void> setPauseOnGateway(
  GatewayClient client,
  String deviceId,
  String? pausedUntil,
) async {
  await client.call('push.setPause', {
    'device_id': deviceId,
    if (pausedUntil != null && pausedUntil.isNotEmpty) 'paused_until': pausedUntil,
  });
}

/// Best-effort: tell the gateway to forget this device's push target. Errors are
/// swallowed — the connection may already be closing, and an offline gateway's
/// record is pruned server-side on the next failed send.
Future<void> unregisterFromGateway(GatewayClient client, String deviceId) async {
  try {
    await client.call('push.unregister', {'device_id': deviceId});
  } catch (_) {}
}
